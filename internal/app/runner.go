package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"secretgit/internal/collab"
	"secretgit/internal/gitx"
)

// RunnerOptions configures Runner.
type RunnerOptions struct {
	Dir      string        // a clone made with `secretgit clone`
	Name     string        // check name (default "ci")
	Cmd      string        // pipeline command; default: .secretgit/ci, then `make ci`
	Branches []string      // always-checked branches besides open PR heads (default: main)
	Interval time.Duration // poll interval (default 60s)
	Timeout  time.Duration // per job (default 30m)
	Once     bool          // one pass, then exit (tests, cron)
	MaxLog   int           // bytes of log kept per check (default 200 KiB)
}

// Runner is the CI agent: it runs on a machine that holds a key, pulls
// new commits through the encrypted remote, executes the repository's own
// pipeline in a throw-away worktree, and records a signed check event
// that every client can verify. The host sees none of it.
func (a *App) Runner(o RunnerOptions) error {
	if o.Name == "" {
		o.Name = "ci"
	}
	if o.Interval == 0 {
		o.Interval = 60 * time.Second
	}
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Minute
	}
	if o.MaxLog == 0 {
		o.MaxLog = 200 << 10
	}
	if len(o.Branches) == 0 {
		o.Branches = []string{"main"}
	}
	for {
		n, err := a.runnerPass(o)
		if err != nil {
			a.logf("runner: %v", err)
		} else if n > 0 {
			a.logf("runner: %d job(s) done", n)
		}
		if o.Once {
			return err
		}
		time.Sleep(o.Interval)
	}
}

func (a *App) runnerPass(o RunnerOptions) (int, error) {
	c, err := a.openPR(o.Dir)
	if err != nil {
		return 0, err
	}
	targets := map[string]string{} // sha -> description
	for i := range c.prs {
		pr := &c.prs[i]
		if pr.State == collab.StateOpen {
			if sha := c.headSHA(pr); sha != "" {
				targets[sha] = fmt.Sprintf("#%d %s", pr.Number, pr.Head)
			}
		}
	}
	for _, br := range o.Branches {
		if sha := c.baseSHA(&collab.PullRequest{Base: br}); sha != "" {
			targets[sha] = br
		}
	}
	done := 0
	for sha, what := range targets {
		if _, ok := collab.Checks(c.events, sha)[o.Name]; ok {
			continue
		}
		a.logf("runner: %s @ %s", what, short(sha))
		status, summary, log := a.runJob(c, o, sha)
		e := &collab.Event{Kind: collab.KindCheck, Commit: sha, Name: o.Name, Status: status, Summary: summary, Log: log}
		if err := c.append(e); err != nil {
			return done, err
		}
		a.logf("runner: %s @ %s → %s (%s)", what, short(sha), status, summary)
		done++
	}
	return done, nil
}

// runJob executes the pipeline for one commit in a detached worktree.
func (a *App) runJob(c *prContext, o RunnerOptions, sha string) (status, summary, log string) {
	wt, err := os.MkdirTemp("", "secretgit-job-")
	if err != nil {
		return collab.StatusFailure, err.Error(), ""
	}
	defer os.RemoveAll(wt)
	defer gitx.Run(c.r.Work, "worktree", "remove", "--force", wt)
	if _, err := gitx.Run(c.r.Work, "worktree", "add", "--quiet", "--detach", wt, sha); err != nil {
		return collab.StatusFailure, "checkout failed", err.Error()
	}
	cmd := o.Cmd
	if cmd == "" {
		switch {
		case isExecutable(filepath.Join(wt, ".secretgit", "ci")):
			cmd = "./.secretgit/ci"
		case hasMakeTarget(wt, "ci"):
			cmd = "make ci"
		default:
			return collab.StatusFailure, "no pipeline: add .secretgit/ci, a `ci` make target, or run the runner with --cmd", ""
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	run := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	run.Dir = wt
	run.Env = append(gitx.Env(), "CI=1", "SECRETGIT_COMMIT="+sha, "SECRETGIT_CHECK="+o.Name)
	var out bytes.Buffer
	run.Stdout, run.Stderr = &out, &out
	start := time.Now()
	err = run.Run()
	dur := time.Since(start).Round(time.Second)
	log = out.String()
	if len(log) > o.MaxLog {
		log = "…(truncated)…\n" + log[len(log)-o.MaxLog:]
	}
	switch {
	case ctx.Err() != nil:
		return collab.StatusFailure, fmt.Sprintf("timed out after %s", o.Timeout), log
	case err != nil:
		return collab.StatusFailure, fmt.Sprintf("%s failed in %s: %v", cmd, dur, err), log
	}
	return collab.StatusSuccess, fmt.Sprintf("%s passed in %s", cmd, dur), log
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func hasMakeTarget(dir, target string) bool {
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil && strings.Contains(string(data), "\n"+target+":") {
			return true
		}
	}
	return false
}
