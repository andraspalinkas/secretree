package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJoinThroughVault: the request travels inside the vault, a member
// approves it, the newcomer clones; deny removes a request.
func TestJoinThroughVault(t *testing.T) {
	homeA := env(t)
	src := newSource(t, homeA)
	vaultDir := filepath.Join(homeA, "vault.git")
	a, out := newApp()
	if err := a.Init(InitOptions{Dir: src, VaultURL: vaultDir, KitOut: filepath.Join(homeA, "kit.txt"), Push: true, Label: "alice", Name: "alice"}); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	homeB := filepath.Join(homeA, "bob")
	os.MkdirAll(homeB, 0o755)
	t.Setenv("SECRETREE_HOME", filepath.Join(homeB, "sg"))
	out.Reset()
	if err := a.Join(JoinOptions{VaultURL: vaultDir, Name: "bob-laptop"}); err != nil {
		t.Fatalf("join: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "pushed to the vault") {
		t.Fatalf("expected the request to go through the vault:\n%s", out)
	}
	// a second device asks too, and gets denied
	homeC := filepath.Join(homeA, "carol")
	os.MkdirAll(homeC, 0o755)
	t.Setenv("SECRETREE_HOME", filepath.Join(homeC, "sg"))
	if err := a.Join(JoinOptions{VaultURL: vaultDir, Name: "carol"}); err != nil {
		t.Fatalf("join carol: %v", err)
	}
	t.Setenv("SECRETREE_HOME", filepath.Join(homeA, "sg"))
	out.Reset()
	if err := a.MemberPending(src); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "bob-laptop") || !strings.Contains(out.String(), "carol") {
		t.Fatalf("pending:\n%s", out)
	}
	if err := a.MemberDeny(src, "carol"); err != nil {
		t.Fatalf("deny: %v", err)
	}
	out.Reset()
	if err := a.MemberApprove(src, "bob-laptop", ""); err != nil {
		t.Fatalf("approve: %v\n%s", err, out)
	}
	out.Reset()
	_ = a.MemberPending(src)
	if !strings.Contains(out.String(), "no pending") {
		t.Fatalf("requests should be gone:\n%s", out)
	}
	// the request files are gone from the vault too
	rp, _ := a.openRepo(src)
	vs, _ := a.loadVault(rp)
	if vs.Reader.Exists("requests") {
		t.Fatal("requests directory still on the vault")
	}
	t.Setenv("SECRETREE_HOME", filepath.Join(homeB, "sg"))
	if err := a.Clone(CloneOptions{VaultURL: vaultDir, Dir: filepath.Join(homeB, "app")}); err != nil {
		t.Fatalf("bob clone: %v\n%s", err, out)
	}
	t.Setenv("SECRETREE_HOME", filepath.Join(homeC, "sg"))
	if err := a.Clone(CloneOptions{VaultURL: vaultDir, Dir: filepath.Join(homeC, "app")}); err == nil {
		t.Fatal("carol was denied and must not be able to clone")
	}
}
