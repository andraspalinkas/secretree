package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"secretgit/internal/archive"
	"secretgit/internal/config"
	"secretgit/internal/crypt"
	"secretgit/internal/gitx"
	"secretgit/internal/vault"
)

// BackupOptions configures Backup.
type BackupOptions struct {
	Dir     string
	Full    bool
	NoProof bool // skip the restore proof (tests of failure paths only)
}

// Backup writes one generation and proves it restorable.
func (a *App) Backup(o BackupOptions) (err error) {
	r, err := a.openRepo(o.Dir)
	if err != nil {
		return err
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	status, err := config.LoadStatus(r.Paths)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			now := time.Now().UTC()
			status.LastError = err.Error()
			status.LastErrorTime = &now
			_ = config.SaveStatus(r.Paths, status)
		}
	}()

	head, err := gitx.Head(r.Work)
	if err != nil {
		return err
	}
	refs, err := gitx.RefMap(r.Work)
	if err != nil {
		return err
	}

	branch, empty, err := syncCache(r.Cfg.VaultURL, r.Paths.Cache, r.Cfg.VaultBranch)
	if err != nil {
		return err
	}
	if empty {
		return errors.New("vault is empty; run secretgit init first")
	}
	reader := &vault.Reader{Dir: r.Paths.Cache}
	pub, _ := r.Keys.PublicKey()
	meta, signers, err := vault.LoadMeta(reader, pub)
	if err != nil {
		return err
	}
	if meta.VaultID != r.Cfg.VaultID {
		return fmt.Errorf("remote holds vault %s, config expects %s", meta.VaultID, r.Cfg.VaultID)
	}
	recipients, err := crypt.ParseRecipients(meta.Recipients)
	if err != nil {
		return err
	}
	chain, err := vault.LoadChain(reader, meta.VaultID, r.Cfg.RepoID, r.identity, signers)
	if err != nil {
		return fmt.Errorf("existing chain failed verification, refusing to append: %w", err)
	}

	tmp, cleanup, err := r.tmpDir("backup")
	if err != nil {
		return err
	}
	defer cleanup()

	last := chain.Last()
	n := 1
	if last != nil {
		n = last.Num + 1
	}
	kind, reason := a.decideKind(r, chain, status, refs, o.Full)
	a.debugf("generation %06d: %s (%s)", n, kind, reason)

	// state archive
	var stateFile string
	stateChanged := false
	if len(r.Cfg.State.Include) > 0 || r.Cfg.State.PreHook != "" {
		stateFile = filepath.Join(tmp, "state.tar.zst")
		ok, err := archive.Build(archive.Spec{
			Root: r.Work, Include: r.Cfg.State.Include, Exclude: r.Cfg.State.Exclude, PreHook: r.Cfg.State.PreHook,
		}, stateFile, filepath.Join(tmp, "stage"))
		if err != nil {
			return err
		}
		if !ok {
			stateFile = ""
		} else {
			h, err := crypt.SHA256File(stateFile)
			if err != nil {
				return err
			}
			stateChanged = last == nil || last.Manifest.File(vault.RoleState) == nil || last.Manifest.File(vault.RoleState).PlaintextSHA256 != h
		}
	}

	// bundle
	bundleFile := filepath.Join(tmp, "repo.bundle")
	var prereqs []string
	var base *int
	switch kind {
	case vault.KindFull:
		if err := gitx.BundleCreate(r.Work, bundleFile, nil); err != nil {
			return fmt.Errorf("bundle: %w", err)
		}
	case vault.KindIncremental:
		b := last.Num
		base = &b
		exclude := uniqueCommits(r.Work, last.Manifest.Refs)
		err := gitx.BundleCreate(r.Work, bundleFile, exclude)
		switch {
		case errors.Is(err, gitx.ErrEmptyBundle):
			bundleFile = ""
		case err != nil:
			return fmt.Errorf("bundle: %w", err)
		default:
			if prereqs, err = gitx.BundlePrerequisites(bundleFile); err != nil {
				return err
			}
		}
	}
	refsChanged := last == nil || gitx.DiffRefs(last.Manifest.Refs, refs) != "" || last.Manifest.Source.Head != head
	if kind == vault.KindIncremental && bundleFile == "" && !refsChanged && !stateChanged {
		a.logf("nothing to back up (generation %06d is current)", last.Num)
		return nil
	}

	// encrypt payloads into the cache work tree
	repoDir := vault.RepoDir(r.Cfg.RepoID)
	if err := os.MkdirAll(filepath.Join(r.Paths.Cache, repoDir), 0o700); err != nil {
		return err
	}
	var files []vault.FileEntry
	var written []string
	if bundleFile != "" {
		name := vault.BundleName(n)
		d, err := crypt.EncryptFile(filepath.Join(r.Paths.Cache, repoDir, name), bundleFile, recipients)
		if err != nil {
			return err
		}
		_ = os.Remove(bundleFile)
		files = append(files, vault.FileEntry{Name: name, Role: vault.RoleBundle, ContentEncoding: vault.EncodingNone,
			PlaintextSHA256: d.PlaintextSHA256, CiphertextSHA256: d.CiphertextSHA256, Size: d.Size})
		written = append(written, repoDir+"/"+name)
	}
	if stateFile != "" {
		name := vault.StateName(n)
		d, err := crypt.EncryptFile(filepath.Join(r.Paths.Cache, repoDir, name), stateFile, recipients)
		if err != nil {
			return err
		}
		_ = os.Remove(stateFile)
		files = append(files, vault.FileEntry{Name: name, Role: vault.RoleState, ContentEncoding: vault.EncodingZstd,
			PlaintextSHA256: d.PlaintextSHA256, CiphertextSHA256: d.CiphertextSHA256, Size: d.Size})
		written = append(written, repoDir+"/"+name)
	}

	// manifest
	m := vault.Manifest{
		Format:        vault.FormatManifest,
		VaultID:       meta.VaultID,
		RepoID:        r.Cfg.RepoID,
		Generation:    n,
		Kind:          kind,
		Base:          base,
		Created:       time.Now().UTC().Truncate(time.Second),
		Source:        vault.Source{Label: r.Cfg.Label, Head: head},
		Refs:          refs,
		Prerequisites: prereqs,
		Files:         files,
		Tool:          "secretgit/" + Version,
	}
	if prereqs == nil {
		m.Prerequisites = []string{}
	}
	if last != nil {
		h := last.ManifestCipherHash
		m.PrevManifestSHA256 = &h
	}
	plain, err := json.MarshalIndent(&m, "", "  ")
	if err != nil {
		return err
	}
	cipher, _, err := crypt.EncryptBytes(plain, recipients)
	if err != nil {
		return err
	}
	sig, err := crypt.Sign(cipher, r.signer)
	if err != nil {
		return err
	}
	mp := vault.ManifestPath(r.Cfg.RepoID, n)
	if err := os.WriteFile(filepath.Join(r.Paths.Cache, mp), cipher, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.Paths.Cache, mp+".sig"), sig, 0o600); err != nil {
		return err
	}
	written = append(written, mp, mp+".sig")

	if err := commitPush(r.Paths.Cache, branch, written); err != nil {
		return err
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	a.logf("generation %06d pushed: %s, %d refs, %s", n, kind, len(refs), humanBytes(total))

	now := time.Now().UTC()
	status.LastGeneration = n
	status.LastBackup = &now
	status.Generations = n
	status.ChainBytes = chain.Bytes() + total
	status.LastError = ""
	status.LastErrorTime = nil
	if err := config.SaveStatus(r.Paths, status); err != nil {
		return err
	}
	if o.NoProof {
		return nil
	}

	// restore proof: fetch back from the remote, rebuild, compare
	res, err := a.prove(r, n, refs, crypt.SHA256Bytes(cipher))
	if err != nil {
		return fmt.Errorf("RESTORE PROOF FAILED for generation %06d: %w", n, err)
	}
	now = time.Now().UTC()
	status.LastProof = &now
	status.LastProofGeneration = n
	if err := config.SaveStatus(r.Paths, status); err != nil {
		return err
	}
	a.logf("restore proof OK: generation %06d rebuilt from the remote (%s)", n, res)
	return nil
}

// decideKind picks full vs incremental and explains why.
func (a *App) decideKind(r *repo, chain *vault.Chain, status *config.Status, refs map[string]string, force bool) (string, string) {
	last := chain.Last()
	if last == nil {
		return vault.KindFull, "first generation"
	}
	if force {
		return vault.KindFull, "--full"
	}
	if status.LastProofGeneration < status.LastGeneration && status.LastError != "" {
		return vault.KindFull, "previous generation was not proven restorable"
	}
	var lastFull *vault.Generation
	incCount := 0
	var incBytes, fullBytes int64
	for i := range chain.Gens {
		g := &chain.Gens[i]
		if g.Manifest.Kind == vault.KindFull {
			lastFull, incCount, incBytes = g, 0, 0
			fullBytes = 0
			if f := g.Manifest.File(vault.RoleBundle); f != nil {
				fullBytes = f.Size
			}
			continue
		}
		incCount++
		if f := g.Manifest.File(vault.RoleBundle); f != nil {
			incBytes += f.Size
		}
	}
	if lastFull == nil {
		return vault.KindFull, "no full generation in chain"
	}
	if incCount >= r.Cfg.FullEvery {
		return vault.KindFull, fmt.Sprintf("%d incrementals since last full", incCount)
	}
	if fullBytes > 0 && float64(incBytes) > r.Cfg.FullRatio*float64(fullBytes) {
		return vault.KindFull, "incrementals outgrew the last full"
	}
	for _, id := range last.Manifest.Refs {
		if !gitx.HasObject(r.Work, id) {
			return vault.KindFull, "previous tip " + id[:8] + " no longer exists in the source"
		}
	}
	return vault.KindIncremental, "base " + vault.GenName(last.Num)
}

// uniqueCommits returns the distinct commit/tag ids among ref targets.
func uniqueCommits(work string, refs map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range refs {
		if seen[id] {
			continue
		}
		seen[id] = true
		t, err := gitx.Run(work, "cat-file", "-t", id)
		if err != nil {
			continue
		}
		switch t[:len(t)-1] {
		case "commit", "tag":
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
