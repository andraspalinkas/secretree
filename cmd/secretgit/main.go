// Command secretgit is a zero-knowledge off-site git backup tool.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
  join      --vault <url|dir> [--name <device>] [--out <file>]   # new device: keys + join request
  member    list | add --request <file> | add --recipient age1... [--signer "ssh-ed25519 ..."] --name <n> | remove --name <n> | request
  share     <path> [--ref <ref>] | --diff <a..b> [--expires 7d] [--note <why>] [--out <file.html>]
  ledger                                # every deliberate disclosure, signed and chained
  ui        [--listen 127.0.0.1:7391] [--open]   # local code browser + pull requests over the vault mirror
  pr        open --title <t> [--base main] [--head <branch>] | list [--all] | show <#n> | comment <#n> -m <text> [--path f --line n]
            approve <#n> [-m] | request-changes <#n> -m | merge <#n> [--method merge|squash|ff] | close <#n>
  policy    [--approvals 1] [--checks ci]        # writes .secretgit/policy.json (commit it on the base branch)
  runner    [--name ci] [--cmd <sh>] [--branches main] [--interval 60s] [--once]   # CI agent on a key-holding machine
  deploy-agent --to <dir> [--branch main] [--cmd <sh>] [--require-check ci] [--once]  # pull-based CD on the target host
  link      <path>[:<line>] [--ref <ref>]        # permalink into the local UI
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
		pos := parseAll(fs, rest)
		if len(pos) < 1 {
			fmt.Fprintln(os.Stderr, "usage: secretgit clone <vault-url> [dir]")
			os.Exit(2)
		}
		o.VaultURL = pos[0]
		if len(pos) > 1 {
			o.Dir = pos[1]
		}
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
	case "join":
		fs := flag.NewFlagSet("join", flag.ExitOnError)
		o := app.JoinOptions{}
		fs.StringVar(&o.VaultURL, "vault", "", "vault git URL or directory")
		fs.StringVar(&o.Name, "name", "", "this device's name (shown to members)")
		fs.StringVar(&o.Out, "out", "", "write the join request to a file")
		pos := parseAll(fs, rest)
		if o.VaultURL == "" && len(pos) == 1 {
			o.VaultURL = pos[0]
		}
		err = a.Join(o)
	case "member":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "usage: secretgit member list|add|remove|request")
			os.Exit(2)
		}
		sub, subrest := rest[0], rest[1:]
		fs := flag.NewFlagSet("member "+sub, flag.ExitOnError)
		o := app.MemberOptions{Dir: *dir}
		fs.StringVar(&o.Request, "request", "", "join request file")
		fs.StringVar(&o.Recipient, "recipient", "", "age public key")
		fs.StringVar(&o.Signer, "signer", "", "ssh public key line")
		fs.StringVar(&o.Name, "name", "", "member name")
		must(fs.Parse(subrest))
		switch sub {
		case "list":
			err = a.MemberList(*dir)
		case "add":
			err = a.MemberAdd(o)
		case "remove":
			err = a.MemberRemove(o)
		case "request":
			err = a.MemberRequest(*dir)
		default:
			fmt.Fprintln(os.Stderr, "usage: secretgit member list|add|remove|request")
			os.Exit(2)
		}
	case "share":
		fs := flag.NewFlagSet("share", flag.ExitOnError)
		o := app.ShareOptions{Dir: *dir}
		fs.StringVar(&o.Ref, "ref", "", "ref or commit (default HEAD)")
		fs.StringVar(&o.Diff, "diff", "", "share a diff: <a..b> or a commit")
		fs.DurationVar(&o.Expires, "expires", 7*24*time.Hour, "advisory expiry (0 = none)")
		fs.StringVar(&o.Note, "note", "", "why this is being shared (goes into the ledger)")
		fs.StringVar(&o.Out, "out", "", "output HTML file")
		fs.BoolVar(&o.NoLedger, "no-ledger", false, "do not record the disclosure (not recommended)")
		pos := parseAll(fs, rest)
		if len(pos) > 0 {
			o.Path = pos[0]
		}
		err = a.Share(o)
	case "ledger":
		err = a.Ledger(*dir)
	case "ui":
		fs := flag.NewFlagSet("ui", flag.ExitOnError)
		o := app.UIOptions{Dir: *dir}
		fs.StringVar(&o.Listen, "listen", app.DefaultUIAddr, "address to listen on (keep it loopback unless on a private network)")
		fs.BoolVar(&o.Open, "open", false, "open in the browser")
		must(fs.Parse(rest))
		err = a.UI(o)
	case "link":
		fs := flag.NewFlagSet("link", flag.ExitOnError)
		ref := fs.String("ref", "", "ref (default: current commit, for a permanent link)")
		pos := parseAll(fs, rest)
		if len(pos) != 1 {
			fmt.Fprintln(os.Stderr, "usage: secretgit link <path>[:<line>] [--ref <ref>]")
			os.Exit(2)
		}
		err = a.Link(*dir, pos[0], *ref)
	case "pr":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "usage: secretgit pr open|list|show|comment|approve|request-changes|merge|close")
			os.Exit(2)
		}
		sub, subrest := rest[0], rest[1:]
		fs := flag.NewFlagSet("pr "+sub, flag.ExitOnError)
		title := fs.String("title", "", "title")
		body := fs.String("body", "", "description")
		msg := fs.String("m", "", "message")
		base := fs.String("base", "", "base branch (default main)")
		head := fs.String("head", "", "head branch (default current)")
		path := fs.String("path", "", "file the comment refers to")
		line := fs.Int("line", 0, "line the comment refers to")
		all := fs.Bool("all", false, "include merged and closed")
		method := fs.String("method", "merge", "merge | squash | ff")
		pos := parseAll(fs, subrest)
		ref := ""
		if len(pos) > 0 {
			ref = pos[0]
		}
		switch sub {
		case "open":
			if *title == "" && ref != "" {
				*title = ref
			}
			err = a.PROpen(app.PROpenOptions{Dir: *dir, Title: *title, Body: *body, Base: *base, Head: *head})
		case "list":
			err = a.PRList(*dir, *all)
		case "show":
			err = a.PRShow(*dir, ref)
		case "comment":
			err = a.PRComment(*dir, ref, *msg, *path, *line)
		case "approve":
			err = a.PRReview(*dir, ref, "approve", *msg)
		case "request-changes":
			err = a.PRReview(*dir, ref, "request_changes", *msg)
		case "merge":
			err = a.PRMerge(*dir, ref, *method)
		case "close":
			err = a.PRClose(*dir, ref)
		default:
			fmt.Fprintln(os.Stderr, "usage: secretgit pr open|list|show|comment|approve|request-changes|merge|close")
			os.Exit(2)
		}
	case "policy":
		fs := flag.NewFlagSet("policy", flag.ExitOnError)
		approvals := fs.Int("approvals", 1, "required approvals")
		checks := fs.String("checks", "ci", "comma-separated required checks (\"\" for none)")
		must(fs.Parse(rest))
		var cs []string
		for _, c := range strings.Split(*checks, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cs = append(cs, c)
			}
		}
		err = a.PolicyInit(*dir, *approvals, cs)
	case "runner":
		fs := flag.NewFlagSet("runner", flag.ExitOnError)
		o := app.RunnerOptions{Dir: *dir}
		fs.StringVar(&o.Name, "name", "ci", "check name")
		fs.StringVar(&o.Cmd, "cmd", "", "pipeline command (default: .secretgit/ci, then make ci)")
		branches := fs.String("branches", "main", "comma-separated branches to always check")
		fs.DurationVar(&o.Interval, "interval", 60*time.Second, "poll interval")
		fs.DurationVar(&o.Timeout, "timeout", 30*time.Minute, "per-job timeout")
		fs.BoolVar(&o.Once, "once", false, "one pass, then exit")
		must(fs.Parse(rest))
		o.Branches = strings.Split(*branches, ",")
		err = a.Runner(o)
	case "deploy-agent":
		fs := flag.NewFlagSet("deploy-agent", flag.ExitOnError)
		o := app.DeployOptions{Dir: *dir}
		fs.StringVar(&o.Branch, "branch", "main", "branch to deploy")
		fs.StringVar(&o.To, "to", "", "target directory")
		fs.StringVar(&o.Cmd, "cmd", "", "command to run in the target after export")
		fs.StringVar(&o.RequireCheck, "require-check", "ci", "deploy only when this check is green (\"\" for none)")
		fs.DurationVar(&o.Interval, "interval", 60*time.Second, "poll interval")
		fs.BoolVar(&o.Once, "once", false, "one pass, then exit")
		must(fs.Parse(rest))
		err = a.DeployAgent(o)
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

// parseAll lets flags and positional arguments be interleaved, the way
// git's own commands behave: `secretgit share src/x.go --out page.html`.
func parseAll(fs *flag.FlagSet, args []string) []string {
	var positional []string
	for {
		must(fs.Parse(args))
		if fs.NArg() == 0 {
			return positional
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
