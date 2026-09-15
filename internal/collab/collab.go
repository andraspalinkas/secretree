// Package collab stores pull requests, reviews, checks and deployments as
// signed JSON events in a git ref, so they travel through the encrypted
// vault like any other object and no server ever holds them in plaintext.
//
// Layout of the tree at refs/secretgit/collab:
//
//	pr/<id>/<unix>-<eventid>.json      the event
//	pr/<id>/<unix>-<eventid>.json.sig  OpenSSH signature over the event bytes
//
// Every event file name is unique (random id), so two devices appending
// concurrently produce trees that merge as a plain union.
package collab

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"secretgit/internal/gitx"
)

const (
	Ref       = "refs/secretgit/collab"
	OriginRef = "refs/secretgit/collab-origin"
	Format    = "secretgit-collab/1"

	KindPR      = "pr"
	KindComment = "comment"
	KindReview  = "review"
	KindState   = "state"
	KindCheck   = "check"
	KindDeploy  = "deploy"

	VerdictApprove        = "approve"
	VerdictRequestChanges = "request_changes"
	VerdictComment        = "comment"

	StateOpen   = "open"
	StateMerged = "merged"
	StateClosed = "closed"

	StatusPending = "pending"
	StatusSuccess = "success"
	StatusFailure = "failure"
)

// Event is one signed fact. Fields outside the common set are used by
// the kinds noted.
type Event struct {
	Format    string    `json:"format"`
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	PR        string    `json:"pr,omitempty"`
	Created   time.Time `json:"created"`
	Actor     string    `json:"actor"` // signer fingerprint (filled on read)
	ActorName string    `json:"actor_name,omitempty"`

	// pr
	Number  int    `json:"number,omitempty"`
	Title   string `json:"title,omitempty"`
	Body    string `json:"body,omitempty"`
	Base    string `json:"base,omitempty"`
	Head    string `json:"head,omitempty"`
	HeadSHA string `json:"head_sha,omitempty"`

	// comment / review
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Verdict string `json:"verdict,omitempty"`

	// state
	State       string `json:"state,omitempty"`
	MergeCommit string `json:"merge_commit,omitempty"`

	// check / deploy
	Name    string `json:"name,omitempty"`
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
	Log     string `json:"log,omitempty"`
	Target  string `json:"target,omitempty"`
}

// Signer signs event bytes; Verifier checks them and returns the signer's
// fingerprint. Both are supplied by the caller (the vault knows the roster).
type Signer func(data []byte) ([]byte, error)
type Verifier func(data, sig []byte, at time.Time) (string, error)

// Store is the collab ref of one repository.
type Store struct {
	Dir  string // repository (git dir or work tree)
	Sign Signer
	Ver  Verifier
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Append writes one event to the local collab ref.
func (s *Store) Append(e *Event) error {
	if e.ID == "" {
		e.ID = newID()
	}
	e.Format = Format
	if e.Created.IsZero() {
		e.Created = time.Now().UTC().Truncate(time.Second)
	}
	if e.PR == "" && e.Kind != KindCheck && e.Kind != KindDeploy {
		return errors.New("event needs a pr")
	}
	dir := "pr/" + e.PR
	if e.PR == "" {
		dir = "repo"
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	sig, err := s.Sign(data)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%s/%d-%s.json", dir, e.Created.Unix(), e.ID)
	return s.commitFiles(map[string][]byte{name: data, name + ".sig": sig}, "collab: "+e.Kind)
}

// commitFiles adds files to the collab tree in a new commit, using a
// temporary index so the user's work tree and index are untouched.
func (s *Store) commitFiles(files map[string][]byte, msg string) error {
	tmp, err := os.CreateTemp("", "secretgit-index-")
	if err != nil {
		return err
	}
	tmp.Close()
	os.Remove(tmp.Name())
	defer os.Remove(tmp.Name())
	env := []string{"GIT_INDEX_FILE=" + tmp.Name()}
	parent, _ := gitx.Run(s.Dir, "rev-parse", "--verify", "-q", Ref)
	parent = strings.TrimSpace(parent)
	if parent != "" {
		if _, err := gitx.RunEnv(s.Dir, env, nil, "read-tree", parent); err != nil {
			return err
		}
	} else {
		if _, err := gitx.RunEnv(s.Dir, env, nil, "read-tree", "--empty"); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		blob, err := gitx.RunIn(s.Dir, files[n], "hash-object", "-w", "--stdin")
		if err != nil {
			return err
		}
		if _, err := gitx.RunEnv(s.Dir, env, nil, "update-index", "--add", "--cacheinfo", "100644,"+strings.TrimSpace(blob)+","+n); err != nil {
			return err
		}
	}
	tree, err := gitx.RunEnv(s.Dir, env, nil, "write-tree")
	if err != nil {
		return err
	}
	args := []string{"-c", "user.name=secretgit", "-c", "user.email=secretgit@localhost", "commit-tree", strings.TrimSpace(tree), "-m", msg}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	commit, err := gitx.Run(s.Dir, args...)
	if err != nil {
		return err
	}
	updArgs := []string{"update-ref", Ref, strings.TrimSpace(commit)}
	if parent != "" {
		updArgs = append(updArgs, parent)
	}
	_, err = gitx.Run(s.Dir, updArgs...)
	return err
}

// Events reads and verifies every event, oldest first. Events whose
// signature does not verify are dropped with a warning line in bad.
func (s *Store) Events() (events []Event, bad []string, err error) {
	if _, err := gitx.Run(s.Dir, "rev-parse", "--verify", "-q", Ref); err != nil {
		return nil, nil, nil
	}
	out, err := gitx.Run(s.Dir, "ls-tree", "-r", "--name-only", Ref)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := gitx.Run(s.Dir, "show", Ref+":"+name)
		if err != nil {
			return nil, nil, err
		}
		sig, err := gitx.Run(s.Dir, "show", Ref+":"+name+".sig")
		if err != nil {
			bad = append(bad, name+": unsigned")
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			bad = append(bad, name+": "+err.Error())
			continue
		}
		fp, err := s.Ver([]byte(data), []byte(sig), e.Created)
		if err != nil {
			bad = append(bad, name+": "+err.Error())
			continue
		}
		e.Actor = fp
		// the directory is authoritative for the pr id
		if strings.HasPrefix(name, "pr/") {
			e.PR = strings.Split(name, "/")[1]
		}
		events = append(events, e)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Created.Before(events[j].Created) })
	return events, bad, nil
}

// PullRequest is the folded view of one PR's events.
type PullRequest struct {
	ID          string
	Number      int
	Title       string
	Body        string
	Base        string
	Head        string
	HeadSHA     string // at open; callers may refresh from the branch
	Author      string
	Created     time.Time
	State       string
	MergeCommit string
	Events      []Event
}

// Fold groups events into pull requests, newest number first.
func Fold(events []Event) []PullRequest {
	byID := map[string]*PullRequest{}
	var order []string
	for _, e := range events {
		if e.PR == "" {
			continue
		}
		pr, ok := byID[e.PR]
		if !ok {
			pr = &PullRequest{ID: e.PR, State: StateOpen}
			byID[e.PR] = pr
			order = append(order, e.PR)
		}
		switch e.Kind {
		case KindPR:
			pr.Number, pr.Title, pr.Body, pr.Base, pr.Head, pr.HeadSHA = e.Number, e.Title, e.Body, e.Base, e.Head, e.HeadSHA
			pr.Author, pr.Created = e.ActorName, e.Created
			if pr.Author == "" {
				pr.Author = e.Actor
			}
		case KindState:
			pr.State, pr.MergeCommit = e.State, e.MergeCommit
		}
		pr.Events = append(pr.Events, e)
	}
	var out []PullRequest
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out
}

// Approvals returns the actors whose latest review of headSHA approves.
func (pr *PullRequest) Approvals(headSHA string) (approved, changesRequested []string) {
	latest := map[string]Event{}
	for _, e := range pr.Events {
		if e.Kind == KindReview && e.Commit == headSHA {
			latest[e.Actor] = e
		}
	}
	for _, e := range latest {
		name := e.ActorName
		if name == "" {
			name = e.Actor
		}
		switch e.Verdict {
		case VerdictApprove:
			approved = append(approved, name)
		case VerdictRequestChanges:
			changesRequested = append(changesRequested, name)
		}
	}
	sort.Strings(approved)
	sort.Strings(changesRequested)
	return
}

// Checks returns the latest check per name for a commit, from all events.
func Checks(events []Event, commit string) map[string]Event {
	out := map[string]Event{}
	for _, e := range events {
		if e.Kind == KindCheck && e.Commit == commit {
			out[e.Name] = e
		}
	}
	return out
}

// Resolve finds a PR by "#12", "12", or an id prefix.
func Resolve(prs []PullRequest, ref string) (*PullRequest, error) {
	ref = strings.TrimPrefix(ref, "#")
	var hits []*PullRequest
	for i := range prs {
		if prs[i].ID == ref || strings.HasPrefix(prs[i].ID, ref) {
			hits = append(hits, &prs[i])
		}
	}
	if n, err := strconv.Atoi(ref); err == nil {
		hits = nil
		for i := range prs {
			if prs[i].Number == n {
				hits = append(hits, &prs[i])
			}
		}
	}
	switch len(hits) {
	case 0:
		return nil, fmt.Errorf("no pull request %q", ref)
	case 1:
		return hits[0], nil
	default:
		var ids []string
		for _, h := range hits {
			ids = append(ids, h.ID)
		}
		return nil, fmt.Errorf("ambiguous: %s", strings.Join(ids, ", "))
	}
}

// NextNumber picks the next PR number.
func NextNumber(prs []PullRequest) int {
	n := 0
	for _, pr := range prs {
		if pr.Number > n {
			n = pr.Number
		}
	}
	return n + 1
}

// NewPRID builds a directory id: "<number>-<random>" so concurrent opens
// never collide.
func NewPRID(number int) string { return fmt.Sprintf("%d-%s", number, newID()[:4]) }

// Sync merges the local collab ref with origin's and pushes it back.
// Divergence is resolved by a tree union (event files are unique), so two
// devices commenting at the same time never conflict. remote is the git
// remote name; a missing remote ref is fine.
func (s *Store) Sync(remote string) error {
	if remote == "" {
		return nil
	}
	if _, err := gitx.Run(s.Dir, "fetch", "--quiet", remote, "+"+Ref+":"+OriginRef); err != nil {
		var ge *gitx.Error
		if errors.As(err, &ge) && strings.Contains(ge.Stderr, "couldn't find remote ref") {
			_, _ = gitx.Run(s.Dir, "update-ref", "-d", OriginRef)
		} else {
			return fmt.Errorf("fetch collab: %w", err)
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		local, _ := gitx.Run(s.Dir, "rev-parse", "--verify", "-q", Ref)
		origin, _ := gitx.Run(s.Dir, "rev-parse", "--verify", "-q", OriginRef)
		local, origin = strings.TrimSpace(local), strings.TrimSpace(origin)
		switch {
		case origin == "":
			// nothing remote yet
		case local == "":
			if _, err := gitx.Run(s.Dir, "update-ref", Ref, origin); err != nil {
				return err
			}
			local = origin
		case local == origin:
		default:
			if _, err := gitx.Run(s.Dir, "merge-base", "--is-ancestor", local, origin); err == nil {
				if _, err := gitx.Run(s.Dir, "update-ref", Ref, origin, local); err != nil {
					return err
				}
				local = origin
			} else if _, err := gitx.Run(s.Dir, "merge-base", "--is-ancestor", origin, local); err == nil {
				// local ahead: push below
			} else {
				tree, err := gitx.Run(s.Dir, "merge-tree", "--write-tree", local, origin)
				if err != nil {
					return fmt.Errorf("collab merge conflict (should not happen with unique event files): %w", err)
				}
				commit, err := gitx.Run(s.Dir, "-c", "user.name=secretgit", "-c", "user.email=secretgit@localhost",
					"commit-tree", strings.TrimSpace(tree), "-p", local, "-p", origin, "-m", "collab: merge")
				if err != nil {
					return err
				}
				if _, err := gitx.Run(s.Dir, "update-ref", Ref, strings.TrimSpace(commit), local); err != nil {
					return err
				}
				local = strings.TrimSpace(commit)
			}
		}
		if local == "" || local == origin {
			return nil
		}
		_, err := gitx.Run(s.Dir, "push", "--quiet", remote, Ref+":"+Ref)
		if err == nil {
			_, _ = gitx.Run(s.Dir, "update-ref", OriginRef, local)
			return nil
		}
		var ge *gitx.Error
		if errors.As(err, &ge) && (strings.Contains(ge.Stderr, "rejected") || strings.Contains(ge.Stderr, "fetch first")) {
			if _, err := gitx.Run(s.Dir, "fetch", "--quiet", remote, "+"+Ref+":"+OriginRef); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("push collab: %w", err)
	}
	return errors.New("collab push kept being rejected; try again")
}

// Policy is .secretgit/policy.json at the base branch.
type Policy struct {
	RequiredApprovals int      `json:"required_approvals"`
	RequiredChecks    []string `json:"required_checks"`
}

// LoadPolicy reads the policy at a ref; absent means no requirements.
func LoadPolicy(dir, ref string) Policy {
	var p Policy
	out, err := gitx.Run(dir, "show", ref+":"+path.Join(".secretgit", "policy.json"))
	if err != nil {
		return p
	}
	_ = json.Unmarshal([]byte(out), &p)
	return p
}

// FilePath helper for tests and docs.
func PolicyPath(work string) string { return filepath.Join(work, ".secretgit", "policy.json") }
