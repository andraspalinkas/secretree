package app

import (
	"fmt"
	"time"

	"secretgit/internal/config"
	"secretgit/internal/keystore"
)

// Status prints the local view: what was backed up, what was proven.
func (a *App) Status(dir string) error {
	work, gitDir, err := locateRepo(dir)
	if err != nil {
		return err
	}
	paths := config.NewPaths(gitDir)
	cfg, err := config.Load(paths)
	if err != nil {
		return err
	}
	st, err := config.LoadStatus(paths)
	if err != nil {
		return err
	}
	store, _ := keystore.Open()
	keysOK := "missing"
	if store != nil {
		if _, err := store.Get(cfg.VaultID); err == nil {
			keysOK = "present"
		}
	}
	a.logf("repository:      %s (%s, id %s)", work, cfg.Label, cfg.RepoID)
	a.logf("vault:           %s (id %s, branch %s)", cfg.VaultURL, cfg.VaultID, cfg.VaultBranch)
	if store != nil {
		a.logf("keys:            %s in %s", keysOK, store.Describe())
	}
	a.logf("generations:     %d (%s on the remote)", st.Generations, humanBytes(st.ChainBytes))
	a.logf("last backup:     %s", ago(st.LastBackup, st.LastGeneration))
	a.logf("last proven:     %s", ago(st.LastProof, st.LastProofGeneration))
	if len(cfg.State.Include) > 0 || cfg.State.PreHook != "" {
		a.logf("state archive:   %d include path(s), pre-hook %q", len(cfg.State.Include), cfg.State.PreHook)
	} else {
		a.logf("state archive:   none configured")
	}
	if st.LastError != "" {
		a.logf("LAST ERROR:      %s (%s)", st.LastError, ago(st.LastErrorTime, 0))
	}
	switch {
	case st.LastGeneration == 0:
		a.logf("\nno backup yet: run secretgit backup")
	case st.LastProofGeneration < st.LastGeneration:
		a.logf("\nWARNING: generation %06d has NOT been proven restorable; run secretgit verify", st.LastGeneration)
	}
	return nil
}

func ago(t *time.Time, gen int) string {
	if t == nil {
		return "never"
	}
	d := time.Since(*t).Round(time.Minute)
	s := fmt.Sprintf("%s (%s ago)", t.Local().Format("2006-01-02 15:04"), d)
	if gen > 0 {
		s = fmt.Sprintf("generation %06d, %s", gen, s)
	}
	return s
}
