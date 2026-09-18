// Package keystore stores key bundles in the OS key store.
//
// macOS: the login Keychain via the `security` CLI (service "secretgit").
// Elsewhere, and whenever SECRETGIT_KEYSTORE=file: a 0600 JSON file under
// $SECRETGIT_HOME (default ~/.config/secretgit). The file store is also what
// tests use so they never touch a real Keychain.
package keystore

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/andraspalinkas/secretgit/internal/keys"
)

// ErrNotFound is returned when no bundle exists for a vault id.
var ErrNotFound = errors.New("no keys for this vault in the key store")

// Store persists key bundles.
type Store interface {
	Get(vaultID string) (*keys.Bundle, error)
	Put(b *keys.Bundle) error
	Delete(vaultID string) error
	// Describe names the backend for status output.
	Describe() string
}

// Open picks the backend from the environment and platform.
func Open() (Store, error) {
	switch os.Getenv("SECRETGIT_KEYSTORE") {
	case "file":
		return newFileStore()
	case "keychain":
		return keychainStore{}, nil
	case "":
		if runtime.GOOS == "darwin" {
			return keychainStore{}, nil
		}
		return newFileStore()
	default:
		return nil, fmt.Errorf("SECRETGIT_KEYSTORE: unknown backend %q", os.Getenv("SECRETGIT_KEYSTORE"))
	}
}

// Home returns the secretgit config directory ($SECRETGIT_HOME or ~/.config/secretgit).
func Home() (string, error) {
	if h := os.Getenv("SECRETGIT_HOME"); h != "" {
		return h, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "secretgit"), nil
}

// ---- file store ----

type fileStore struct{ dir string }

func newFileStore() (Store, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "keys")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return fileStore{dir: dir}, nil
}

func (f fileStore) path(id string) string { return filepath.Join(f.dir, id+".json") }

func (f fileStore) Get(vaultID string) (*keys.Bundle, error) {
	data, err := os.ReadFile(f.path(vaultID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var b keys.Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (f fileStore) Put(b *keys.Bundle) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f.path(b.VaultID), data, 0o600)
}

func (f fileStore) Delete(vaultID string) error {
	err := os.Remove(f.path(vaultID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (f fileStore) Describe() string { return "file (" + f.dir + ")" }

// ---- macOS Keychain ----

type keychainStore struct{}

const service = "secretgit"

func account(vaultID, role string) string { return vaultID + "/" + role }

func (keychainStore) Get(vaultID string) (*keys.Bundle, error) {
	ageID, err := keychainGet(account(vaultID, "age-identity"))
	if err != nil {
		return nil, err
	}
	sigB64, err := keychainGet(account(vaultID, "signing-key"))
	if err != nil {
		return nil, err
	}
	pem, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, fmt.Errorf("keychain: signing key not base64: %w", err)
	}
	return &keys.Bundle{VaultID: vaultID, AgeIdentity: ageID, SigningKeyPEM: string(pem)}, nil
}

func (keychainStore) Put(b *keys.Bundle) error {
	if err := keychainPut(account(b.VaultID, "age-identity"), strings.TrimSpace(b.AgeIdentity)); err != nil {
		return err
	}
	return keychainPut(account(b.VaultID, "signing-key"), base64.StdEncoding.EncodeToString([]byte(b.SigningKeyPEM)))
}

func (keychainStore) Delete(vaultID string) error {
	for _, role := range []string{"age-identity", "signing-key"} {
		cmd := exec.Command("security", "delete-generic-password", "-s", service, "-a", account(vaultID, role))
		_ = cmd.Run()
	}
	return nil
}

func (keychainStore) Describe() string { return "macOS Keychain (service secretgit)" }

func keychainGet(acct string) (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", service, "-a", acct, "-w")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if strings.Contains(errb.String(), "could not be found") {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("keychain read failed: %s", strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func keychainPut(acct, secret string) error {
	// -U updates in place; the value travels in argv (briefly visible in ps),
	// same trade-off as the `security` CLI itself. A Security.framework
	// binding can replace this later without changing the item layout.
	cmd := exec.Command("security", "add-generic-password", "-U", "-s", service, "-a", acct,
		"-l", "secretgit "+acct, "-j", "secretgit vault key; do not delete", "-w", secret)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("keychain write failed: %s", strings.TrimSpace(errb.String()))
	}
	return nil
}
