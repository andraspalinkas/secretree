// Package keys generates, encodes and parses the two keys a vault needs:
// an age X25519 identity (decryption) and an Ed25519 OpenSSH key (signing).
package keys

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"
)

// Namespace is the SSHSIG namespace for every secretree v1 signature.
const Namespace = "secretree-v1"

// Principal is the allowed_signers principal used for every secretree signer.
const Principal = "secretree"

// Bundle holds the secret material of one vault. It lives in the OS key store
// and on the printed recovery kit, nowhere else.
type Bundle struct {
	VaultID       string
	AgeIdentity   string // "AGE-SECRET-KEY-1..."
	SigningKeyPEM string // OpenSSH private key, PEM armored
}

// NewVaultID returns 16 hex characters from the CSPRNG.
func NewVaultID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Generate creates a fresh key bundle for a new vault.
func Generate(vaultID string) (*Bundle, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("generate age identity: %w", err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "secretree vault "+vaultID)
	if err != nil {
		return nil, fmt.Errorf("encode signing key: %w", err)
	}
	return &Bundle{
		VaultID:       vaultID,
		AgeIdentity:   id.String(),
		SigningKeyPEM: string(pem.EncodeToMemory(block)),
	}, nil
}

// Identity parses the age identity.
func (b *Bundle) Identity() (*age.X25519Identity, error) {
	return age.ParseX25519Identity(strings.TrimSpace(b.AgeIdentity))
}

// Recipient returns the age public key string ("age1...").
func (b *Bundle) Recipient() (string, error) {
	id, err := b.Identity()
	if err != nil {
		return "", err
	}
	return id.Recipient().String(), nil
}

// Signer parses the OpenSSH signing key.
func (b *Bundle) Signer() (ssh.Signer, error) {
	raw, err := ssh.ParseRawPrivateKey([]byte(b.SigningKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("parse signing key: %w", err)
	}
	return ssh.NewSignerFromKey(raw)
}

// PublicKey returns the signing public key.
func (b *Bundle) PublicKey() (ssh.PublicKey, error) {
	s, err := b.Signer()
	if err != nil {
		return nil, err
	}
	return s.PublicKey(), nil
}

// Fingerprint returns "SHA256:..." of the signing public key.
func (b *Bundle) Fingerprint() (string, error) {
	pub, err := b.PublicKey()
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(pub), nil
}

// AllowedSignersLine renders this signing key in OpenSSH allowed_signers
// syntax, restricted to the secretree namespace.
func (b *Bundle) AllowedSignersLine(comment string) (string, error) {
	pub, err := b.PublicKey()
	if err != nil {
		return "", err
	}
	return AllowedSignersLine(pub, comment), nil
}

// AllowedSignersLine renders any public key as a secretree allowed_signers line.
func AllowedSignersLine(pub ssh.PublicKey, comment string) string {
	auth := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	line := fmt.Sprintf("%s namespaces=\"%s\" %s", Principal, Namespace, auth)
	if comment != "" {
		line += " " + comment
	}
	return line
}

// ParseAllowedSigners parses lines in OpenSSH allowed_signers syntax and
// returns the public keys that are valid for the secretree namespace.
func ParseAllowedSigners(lines []string) ([]ssh.PublicKey, error) {
	var out []ssh.PublicKey
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("allowed_signers: malformed line %q", line)
		}
		if fields[0] != Principal {
			continue
		}
		i := 1
		nsOK := true // absent namespaces option means any namespace
		for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "ssh-") && !strings.HasPrefix(fields[i], "ecdsa-") && !strings.HasPrefix(fields[i], "sk-") {
			opt := fields[i]
			if strings.HasPrefix(opt, "namespaces=") {
				v := strings.Trim(strings.TrimPrefix(opt, "namespaces="), "\"")
				nsOK = false
				for _, ns := range strings.Split(v, ",") {
					if strings.TrimSpace(ns) == Namespace {
						nsOK = true
					}
				}
			}
			i++
		}
		if !nsOK {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.Join(fields[i:], " ")))
		if err != nil {
			return nil, fmt.Errorf("allowed_signers: bad key in %q: %w", line, err)
		}
		out = append(out, pub)
	}
	if len(out) == 0 {
		return nil, errors.New("allowed_signers: no usable signer for namespace " + Namespace)
	}
	return out, nil
}

// RecoveryKit renders the printable recovery kit. Everything needed to
// restore on a fresh machine is on it; keep it on paper, not in the cloud.
func (b *Bundle) RecoveryKit(vaultURL string) (string, error) {
	fp, err := b.Fingerprint()
	if err != nil {
		return "", err
	}
	var w bytes.Buffer
	fmt.Fprintf(&w, "secretree recovery kit\n")
	fmt.Fprintf(&w, "======================\n")
	fmt.Fprintf(&w, "Vault ID:                %s\n", b.VaultID)
	fmt.Fprintf(&w, "Vault URL:               %s\n", vaultURL)
	fmt.Fprintf(&w, "Signing key fingerprint: %s\n", fp)
	fmt.Fprintf(&w, "Printed:                 %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&w, "\nKeep this page offline. Anyone holding it can read every backup.\n")
	fmt.Fprintf(&w, "Restore: docs/restore-by-hand.md in the secretree repository, or the\n")
	fmt.Fprintf(&w, "README.md inside the vault. Or: secretree init --from-recovery-kit <file>\n")
	fmt.Fprintf(&w, "\n--- age identity ---\n%s\n", strings.TrimSpace(b.AgeIdentity))
	fmt.Fprintf(&w, "\n--- signing key (OpenSSH) ---\n%s", b.SigningKeyPEM)
	if !strings.HasSuffix(b.SigningKeyPEM, "\n") {
		w.WriteString("\n")
	}
	return w.String(), nil
}

// ParseRecoveryKit reads a recovery kit back. It is lenient: only the vault
// id, the age identity and the PEM block are required.
func ParseRecoveryKit(text string) (*Bundle, string, error) {
	b := &Bundle{}
	vaultURL := ""
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "Vault ID:"):
			b.VaultID = strings.TrimSpace(strings.TrimPrefix(t, "Vault ID:"))
		case strings.HasPrefix(t, "Vault URL:"):
			vaultURL = strings.TrimSpace(strings.TrimPrefix(t, "Vault URL:"))
		case strings.HasPrefix(t, "AGE-SECRET-KEY-1"):
			b.AgeIdentity = t
		}
	}
	start := strings.Index(text, "-----BEGIN OPENSSH PRIVATE KEY-----")
	end := strings.Index(text, "-----END OPENSSH PRIVATE KEY-----")
	if start < 0 || end < 0 {
		return nil, "", errors.New("recovery kit: signing key block not found")
	}
	b.SigningKeyPEM = text[start:end+len("-----END OPENSSH PRIVATE KEY-----")] + "\n"
	if b.VaultID == "" || b.AgeIdentity == "" {
		return nil, "", errors.New("recovery kit: vault id or age identity missing")
	}
	if _, err := b.Identity(); err != nil {
		return nil, "", fmt.Errorf("recovery kit: %w", err)
	}
	if _, err := b.Signer(); err != nil {
		return nil, "", fmt.Errorf("recovery kit: %w", err)
	}
	return b, vaultURL, nil
}
