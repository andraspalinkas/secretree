// Command secretgit is a zero-knowledge off-site git backup tool.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"secretgit/internal/app"
)

const usage = `secretgit — zero-knowledge git backup onto any dumb storage

usage: secretgit [-C <repo dir>] [-v] <command> [options]

commands:
  init      --vault <url|dir> [--label <name>] [--kit-out <file>] [--from-recovery-kit <file>] [--repo-id <id>] [--force]
  backup    [--full]
  verify    [--generation N] [--quick]
  restore   --vault <url|dir> --to <dir> [--repo-id <id>] [--generation N] [--from-recovery-kit <file>] [--no-state]
  status
  clone     <vault-url> [dir] [--repo-id <id>] [--from-recovery-kit <file>]
  install-helper [--dir <bindir>]     # makes "git clone secretgit::<vault-url>" work
  schedule  --every <duration> | --daily HH:MM | --remove | --show
  version

The vault is a git repository (SSH/HTTPS URL or a local directory) that only
ever sees ciphertext. With the helper installed, a vault is an ordinary git
remote: git remote add origin secretgit::<vault-url>[#<repo-id>] Docs: docs/vault-format.md, docs/restore-by-hand.md.
`

func main() {
	// git invokes us as git-remote-secretgit <name> <url>
	if filepath.Base(os.Args[0]) == app.HelperName && len(os.Args) == 3 {
		a := &app.App{Out: os.Stderr, Err: os.Stderr}
		if err := a.RemoteHelper(os.Args[1], os.Args[2], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "secretgit: %s\n", strings.TrimSpace(err.Error()))
			os.Exit(1)
		}
		return
	}
	global := flag.NewFlagSet("secretgit", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	dir := global.String("C", "", "run as if started in this directory")
	verbose := global.Bool("v", false, "verbose output")
	global.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	if err := global.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	args := global.Args()
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	a := &app.App{Out: os.Stdout, Err: os.Stderr, Verbose: *verbose}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "init":
		fs := flag.NewFlagSet("init", flag.ExitOnError)
		o := app.InitOptions{Dir: *dir}
		fs.StringVar(&o.VaultURL, "vault", "", "vault git URL or directory")
		fs.StringVar(&o.Label, "label", "", "human label stored (encrypted) in manifests")
		fs.StringVar(&o.KitOut, "kit-out", "", "write the recovery kit to this file instead of stdout")
		fs.StringVar(&o.KitIn, "from-recovery-kit", "", "import keys from a recovery kit (joining an existing vault)")
		fs.StringVar(&o.RepoID, "repo-id", "", "continue an existing chain in the vault")
		fs.BoolVar(&o.Force, "force", false, "overwrite an existing configuration")
		must(fs.Parse(rest))
		err = a.Init(o)
	case "backup":
		fs := flag.NewFlagSet("backup", flag.ExitOnError)
		o := app.BackupOptions{Dir: *dir}
		fs.BoolVar(&o.Full, "full", false, "force a full bundle")
		must(fs.Parse(rest))
		err = a.Backup(o)
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		o := app.VerifyOptions{Dir: *dir}
		fs.IntVar(&o.Generation, "generation", 0, "generation to rebuild (default latest)")
		fs.BoolVar(&o.Quick, "quick", false, "check signatures, hashes and chain only")
		must(fs.Parse(rest))
		err = a.Verify(o)
	case "restore":
		fs := flag.NewFlagSet("restore", flag.ExitOnError)
		o := app.RestoreOptions{}
		fs.StringVar(&o.VaultURL, "vault", "", "vault git URL or directory")
		fs.StringVar(&o.To, "to", "", "new directory for the restored repository")
		fs.StringVar(&o.RepoID, "repo-id", "", "which repository (when the vault holds several)")
		fs.IntVar(&o.Generation, "generation", 0, "generation to restore (default latest)")
		fs.StringVar(&o.KitIn, "from-recovery-kit", "", "recovery kit file")
		fs.BoolVar(&o.NoState, "no-state", false, "do not unpack the state archive")
		must(fs.Parse(rest))
		err = a.Restore(o)
	case "status":
		err = a.Status(*dir)
	case "clone":
		fs := flag.NewFlagSet("clone", flag.ExitOnError)
		o := app.CloneOptions{}
		fs.StringVar(&o.RepoID, "repo-id", "", "which repository (when the vault holds several)")
		fs.StringVar(&o.KitIn, "from-recovery-kit", "", "recovery kit file to import first")
		must(fs.Parse(rest))
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: secretgit clone <vault-url> [dir]")
			os.Exit(2)
		}
		o.VaultURL = fs.Arg(0)
		o.Dir = fs.Arg(1)
		err = a.Clone(o)
	case "install-helper":
		fs := flag.NewFlagSet("install-helper", flag.ExitOnError)
		d := fs.String("dir", "", "directory for the git-remote-secretgit symlink (default: next to this binary)")
		must(fs.Parse(rest))
		err = a.InstallHelperCmd(*d)
	case "remote-helper":
		if len(rest) != 2 {
			fmt.Fprintln(os.Stderr, "usage: secretgit remote-helper <name> <url>   (normally invoked by git)")
			os.Exit(2)
		}
		a.Out = os.Stderr
		err = a.RemoteHelper(rest[0], rest[1], os.Stdin, os.Stdout)
	case "schedule":
		fs := flag.NewFlagSet("schedule", flag.ExitOnError)
		o := app.ScheduleOptions{Dir: *dir}
		fs.DurationVar(&o.Every, "every", 0, "interval, e.g. 1h")
		fs.StringVar(&o.Daily, "daily", "", "time of day, HH:MM")
		fs.BoolVar(&o.Remove, "remove", false, "remove the schedule")
		fs.BoolVar(&o.Show, "show", false, "print the installed schedule")
		must(fs.Parse(rest))
		err = a.Schedule(o)
	case "version":
		fmt.Println("secretgit", app.Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "secretgit %s: %s\n", cmd, strings.TrimSpace(err.Error()))
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		os.Exit(2)
	}
}
