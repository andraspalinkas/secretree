// Package gitx shells out to git. secretgit never parses pack data itself.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Error carries git's stderr.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.Args, " "), e.Err, strings.TrimSpace(e.Stderr))
}

func (e *Error) Unwrap() error { return e.Err }

// Run executes git in dir and returns stdout.
func Run(dir string, args ...string) (string, error) {
	out, _, err := run(dir, nil, args...)
	return out, err
}

// RunIn executes git with stdin.
func RunIn(dir string, stdin []byte, args ...string) (string, error) {
	out, _, err := run(dir, stdin, args...)
	return out, err
}

// Env is the environment for every git we spawn: the caller's environment
// minus repository-selecting variables (a remote helper inherits GIT_DIR
// from git, which must not leak into commands we run on other repos).
func Env() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
			"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX",
			"GIT_COMMON_DIR", "GIT_IMPLICIT_WORK_TREE", "GIT_QUARANTINE_PATH":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}

func run(dir string, stdin []byte, args ...string) (string, string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(Env(),
		"GIT_CONFIG_PARAMETERS='commit.gpgsign=false' 'tag.gpgsign=false' 'core.hooksPath=/dev/null'",
	)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), errb.String(), &Error{Args: args, Stderr: errb.String(), Err: err}
	}
	return out.String(), errb.String(), nil
}

// RunToFile streams git's stdout into a file (for large blobs).
func RunToFile(dir, path string, args ...string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = Env()
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = f, &errb
	err = cmd.Run()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return &Error{Args: args, Stderr: errb.String(), Err: err}
	}
	return nil
}

// GitDir returns the absolute common git dir of the repository containing dir.
func GitDir(dir string) (string, error) {
	out, err := Run(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p), nil
}

// TopLevel returns the working tree root.
func TopLevel(dir string) (string, error) {
	out, err := Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RefMap returns every ref under refs/ with its object id.
func RefMap(dir string) (map[string]string, error) {
	out, err := Run(dir, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("for-each-ref: unexpected line %q", line)
		}
		m[f[0]] = f[1]
	}
	return m, nil
}

// Head returns HEAD as a ref name when symbolic, otherwise the commit id.
func Head(dir string) (string, error) {
	out, err := Run(dir, "symbolic-ref", "-q", "HEAD")
	if err == nil {
		return strings.TrimSpace(out), nil
	}
	out, err = Run(dir, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return "", errors.New("repository has no commits")
	}
	return strings.TrimSpace(out), nil
}

// HasObject reports whether an object exists.
func HasObject(dir, id string) bool {
	_, err := Run(dir, "cat-file", "-e", id)
	return err == nil
}

// ErrEmptyBundle is returned when nothing would go into the bundle.
var ErrEmptyBundle = errors.New("empty bundle")

// BundleCreate writes a bundle of all refs, excluding objects reachable from
// the given ids. Returns ErrEmptyBundle if there is nothing to write.
func BundleCreate(dir, out string, exclude []string) error {
	args := []string{"bundle", "create", out, "--all"}
	for _, e := range exclude {
		args = append(args, "^"+e)
	}
	_, stderr, err := run(dir, nil, args...)
	if err != nil {
		if strings.Contains(stderr, "empty bundle") {
			return ErrEmptyBundle
		}
		return err
	}
	return nil
}

// BundlePrerequisites lists the commit ids a bundle requires, read from
// the bundle header ("-<id> <subject>" lines before the blank line).
func BundlePrerequisites(bundle string) ([]string, error) {
	f, err := os.Open(bundle)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	n, _ := f.Read(buf)
	head := string(buf[:n])
	end := strings.Index(head, "\n\n")
	if end < 0 {
		return nil, fmt.Errorf("bundle %s: header not terminated", bundle)
	}
	var pre []string
	for _, line := range strings.Split(head[:end], "\n") {
		if strings.HasPrefix(line, "-") {
			if f := strings.Fields(line[1:]); len(f) > 0 {
				pre = append(pre, f[0])
			}
		}
	}
	sort.Strings(pre)
	return pre, nil
}

// BundleVerify checks that a bundle can be applied to the repo in dir.
func BundleVerify(dir, bundle string) error {
	_, err := Run(dir, "bundle", "verify", bundle)
	return err
}

// FetchBundle fetches every ref from a bundle into a bare repo.
func FetchBundle(dir, bundle string) error {
	_, err := Run(dir, "fetch", "--quiet", "--no-tags", "--update-head-ok", bundle, "+refs/*:refs/*")
	return err
}

// ApplyRefs makes the repo's refs exactly equal to want.
func ApplyRefs(dir string, want map[string]string) error {
	have, err := RefMap(dir)
	if err != nil {
		return err
	}
	var sb strings.Builder
	for ref, id := range want {
		fmt.Fprintf(&sb, "update %s %s\n", ref, id)
	}
	for ref := range have {
		if _, ok := want[ref]; !ok {
			fmt.Fprintf(&sb, "delete %s\n", ref)
		}
	}
	if sb.Len() == 0 {
		return nil
	}
	_, err = RunIn(dir, []byte(sb.String()), "update-ref", "--stdin")
	return err
}

// SetHead points HEAD at a ref name or detaches it at a commit.
func SetHead(dir, head string) error {
	if strings.HasPrefix(head, "refs/") {
		_, err := Run(dir, "symbolic-ref", "HEAD", head)
		return err
	}
	_, err := Run(dir, "update-ref", "--no-deref", "HEAD", head)
	return err
}

// Fsck runs a connectivity check.
func Fsck(dir string) error {
	_, err := Run(dir, "fsck", "--connectivity-only", "--no-dangling")
	return err
}

// SortedRefs renders a ref map deterministically, for comparison and display.
func SortedRefs(m map[string]string) []string {
	var lines []string
	for k, v := range m {
		lines = append(lines, k+" "+v)
	}
	sort.Strings(lines)
	return lines
}

// DiffRefs returns a human description of how a and b differ, or "".
func DiffRefs(a, b map[string]string) string {
	var out []string
	for k, v := range a {
		if bv, ok := b[k]; !ok {
			out = append(out, "missing "+k)
		} else if bv != v {
			out = append(out, fmt.Sprintf("%s: %s != %s", k, v, bv))
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, "extra "+k)
		}
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

// IsLocalPath reports whether a remote URL is a filesystem path.
func IsLocalPath(url string) bool {
	if strings.HasPrefix(url, "file://") {
		return true
	}
	if strings.Contains(url, "://") {
		return false
	}
	// scp-like "host:path" is remote unless it is an absolute/relative path
	if strings.HasPrefix(url, "/") || strings.HasPrefix(url, ".") || strings.HasPrefix(url, "~") {
		return true
	}
	return !strings.Contains(url, ":")
}
