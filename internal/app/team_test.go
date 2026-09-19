package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
)

func TestJoinAndMembers(t *testing.T) {
	homeA := env(t)
	src := newSource(t, homeA)
	vaultDir := filepath.Join(homeA, "vault.git")
	a, out := newApp()
	if err := a.Init(InitOptions{Dir: src, VaultURL: vaultDir, KitOut: filepath.Join(homeA, "kit.txt"), Label: "app"}); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if o, err := gitOut(src, "push", "-u", "origin", "--all"); err != nil {
		t.Fatalf("push: %v\n%s", err, o)
	}
	if o, err := gitOut(src, "push", "origin", "--tags"); err != nil {
		t.Fatalf("push tags: %v\n%s", err, o)
	}

	// device B has its own key store and generates its own keys
	homeB := filepath.Join(homeA, "deviceB")
	t.Setenv("SECRETREE_HOME", filepath.Join(homeB, "sg"))
	req := filepath.Join(homeB, "join.txt")
	if err := os.MkdirAll(homeB, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := a.Join(JoinOptions{VaultURL: vaultDir, Name: "laptop-b", Out: req, NoPush: true}); err != nil {
		t.Fatalf("join: %v\n%s", err, out)
	}
	// before approval B cannot clone
	if err := a.Clone(CloneOptions{VaultURL: vaultDir, Dir: filepath.Join(homeB, "early")}); err == nil {
		t.Fatal("clone before approval should fail")
	}

	// A approves: vault.json gains a recipient and signer, and a full generation follows
	t.Setenv("SECRETREE_HOME", filepath.Join(homeA, "sg"))
	out.Reset()
	if err := a.MemberAdd(MemberOptions{Dir: src, Request: req}); err != nil {
		t.Fatalf("member add: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "written (full)") {
		t.Fatalf("expected a full generation after add:\n%s", out)
	}
	out.Reset()
	if err := a.MemberList(src); err != nil {
		t.Fatalf("member list: %v", err)
	}
	if !strings.Contains(out.String(), "laptop-b") || !strings.Contains(out.String(), "(this device)") {
		t.Fatalf("member list:\n%s", out)
	}

	// B clones with its own key: generation 1 is opaque to it, 2 is readable
	t.Setenv("SECRETREE_HOME", filepath.Join(homeB, "sg"))
	b := filepath.Join(homeB, "app")
	out.Reset()
	if err := a.Clone(CloneOptions{VaultURL: vaultDir, Dir: b}); err != nil {
		t.Fatalf("clone as B: %v\n%s", err, out)
	}
	assertCloneOf(t, src, b)
	out.Reset()
	if err := a.Verify(VerifyOptions{Dir: b, Quick: true}); err != nil {
		t.Fatalf("verify as B: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "generation 000001: opaque") {
		t.Fatalf("generation 1 should be opaque to B:\n%s", out)
	}
	// B can push (it is a signer) and A can pull
	commit(t, b, "b.txt", "b\n", "from b")
	if o, err := gitOut(b, "push", "origin", "main"); err != nil {
		t.Fatalf("push as B: %v\n%s", err, o)
	}
	t.Setenv("SECRETREE_HOME", filepath.Join(homeA, "sg"))
	if o, err := gitOut(src, "pull", "--ff-only", "origin", "main"); err != nil {
		t.Fatalf("pull as A: %v\n%s", err, o)
	}

	// A removes B: new full generation to A only; B's next fetch fails on it
	out.Reset()
	if err := a.MemberRemove(MemberOptions{Dir: src, Name: "laptop-b"}); err != nil {
		t.Fatalf("member remove: %v\n%s", err, out)
	}
	commit(t, src, "after.txt", "x\n", "after removal")
	if o, err := gitOut(src, "push", "origin", "main"); err != nil {
		t.Fatalf("push after removal: %v\n%s", err, o)
	}
	t.Setenv("SECRETREE_HOME", filepath.Join(homeB, "sg"))
	if o, err := gitOut(b, "fetch", "origin"); err == nil {
		t.Fatalf("B should no longer be able to sync:\n%s", o)
	}
	if o, err := gitOut(b, "push", "origin", "main"); err == nil {
		t.Fatalf("B should no longer be able to push:\n%s", o)
	}
}

func TestShareAndLedger(t *testing.T) {
	home := env(t)
	src := newSource(t, home)
	vaultDir := filepath.Join(home, "vault.git")
	a, out := newApp()
	if err := a.Init(InitOptions{Dir: src, VaultURL: vaultDir, KitOut: filepath.Join(home, "kit.txt"), Label: "app"}); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	htmlPath := filepath.Join(home, "share.html")
	out.Reset()
	if err := a.Share(ShareOptions{Dir: src, Path: filepath.Join(src, "README.md"), Out: htmlPath, Note: "for review"}); err != nil {
		t.Fatalf("share: %v\n%s", err, out)
	}
	key := regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Z]+`).FindString(out.String())
	if key == "" {
		t.Fatalf("no key printed:\n%s", out)
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(html, []byte("hello")) {
		t.Fatal("plaintext leaked into the share page")
	}
	start := bytes.Index(html, []byte("-----BEGIN AGE ENCRYPTED FILE-----"))
	end := bytes.Index(html, []byte("-----END AGE ENCRYPTED FILE-----"))
	if start < 0 || end < 0 {
		t.Fatal("no armored ciphertext in page")
	}
	id, err := age.ParseX25519Identity(key)
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(armor.NewReader(bytes.NewReader(html[start:end+len("-----END AGE ENCRYPTED FILE-----")])), id)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(r)
	var s snapshot
	if err := json.Unmarshal(plain, &s); err != nil {
		t.Fatal(err)
	}
	if s.Type != "file" || s.Content != "hello\n" || !strings.HasPrefix(s.Subject, "file README.md@") {
		t.Fatalf("snapshot: %+v", s)
	}
	// a diff share, then the ledger lists both
	if err := a.Share(ShareOptions{Dir: src, Diff: "HEAD~1..HEAD", Out: filepath.Join(home, "diff.html")}); err != nil {
		t.Fatalf("share diff: %v\n%s", err, out)
	}
	out.Reset()
	if err := a.Ledger(src); err != nil {
		t.Fatalf("ledger: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "2 disclosure(s)") || !strings.Contains(out.String(), "for review") || !strings.Contains(out.String(), "diff HEAD~1..HEAD") {
		t.Fatalf("ledger:\n%s", out)
	}
}

func TestUIHandlers(t *testing.T) {
	home := env(t)
	src := newSource(t, home)
	s := &uiServer{repo: filepath.Join(src, ".git"), name: "app", work: src, source: "test"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /tree/{rest...}", s.tree)
	mux.HandleFunc("GET /blob/{rest...}", s.blob)
	mux.HandleFunc("GET /raw/{rest...}", s.raw)
	mux.HandleFunc("GET /blame/{rest...}", s.blame)
	mux.HandleFunc("GET /commits/{rest...}", s.commits)
	mux.HandleFunc("GET /commit/{sha}", s.commit)
	mux.HandleFunc("GET /search", s.search)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	get := func(p string) string {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", p, resp.StatusCode, b)
		}
		return string(b)
	}
	head := git(t, src, "rev-parse", "HEAD")
	if h := get("/"); !strings.Contains(h, "main") || !strings.Contains(h, "feature") || !strings.Contains(h, "v1") {
		t.Fatalf("home:\n%s", h)
	}
	if h := get("/tree/main"); !strings.Contains(h, `href="/blob/main/README.md"`) || !strings.Contains(h, `href="/tree/main/a"`) {
		t.Fatalf("tree:\n%s", h)
	}
	if h := get("/blob/main/a/b.txt"); !strings.Contains(h, `id="L1"`) || !strings.Contains(h, ">b<") {
		t.Fatalf("blob:\n%s", h)
	}
	if h := get("/raw/main/README.md"); h != "hello\n" {
		t.Fatalf("raw: %q", h)
	}
	if h := get("/blame/main/README.md"); !strings.Contains(h, "class=\"bl\"") {
		t.Fatalf("blame:\n%s", h)
	}
	if h := get("/commits/main"); !strings.Contains(h, "second") {
		t.Fatalf("commits:\n%s", h)
	}
	if h := get("/commit/" + head); !strings.Contains(h, "class=\"a\"") {
		t.Fatalf("commit diff:\n%s", h)
	}
	if h := get("/search?q=hello&ref=main"); !strings.Contains(h, "/blob/main/README.md#L1") {
		t.Fatalf("search:\n%s", h)
	}
	// a slashed ref uses the /-/ separator
	git(t, src, "branch", "topic/x", "main")
	if h := get("/blob/topic/x/-/README.md"); !strings.Contains(h, "hello") {
		t.Fatalf("slashed ref:\n%s", h)
	}
}
