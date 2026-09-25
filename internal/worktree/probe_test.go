package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The probe tests use real temp git repos: the probe's whole job is
// reading git's on-disk admin layout, so the fixture that can't drift
// from reality is a repo git itself made.

func runProbeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// newProbeRepo builds a real repo with one commit and returns its root.
func newProbeRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runProbeGit(t, repo, "init", "-q")
	runProbeGit(t, repo, "-c", "user.email=test@test", "-c", "user.name=test",
		"commit", "-q", "--allow-empty", "-m", "init")
	return repo
}

// resolve symlinks the way git does when it writes paths (macOS temp
// dirs live under /var, a symlink to /private/var, and the gitdir file
// the probe reads carries the resolved spelling).
func resolve(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestProbeFindsLinkedWorktrees(t *testing.T) {
	repo := newProbeRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "wt1")
	runProbeGit(t, repo, "worktree", "add", "-q", "-b", "feature/isa-1-x", wt)

	entries, ok := Probe([]string{repo})[repo]
	if !ok {
		t.Fatal("want the repo present in the probe result")
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want the one linked worktree", len(entries))
	}
	e := entries[0]
	if want := resolve(t, wt); e.Path != want {
		t.Fatalf("got path %q, want %q", e.Path, want)
	}
	if e.Branch != "feature/isa-1-x" {
		t.Fatalf("got branch %q, want feature/isa-1-x", e.Branch)
	}
	if e.CreatedTime.IsZero() || time.Since(e.CreatedTime) > time.Minute {
		t.Fatalf("got CreatedTime %v, want a fresh birth time", e.CreatedTime)
	}
}

func TestProbeRepoWithNoLinkedWorktreesIsPresentAndEmpty(t *testing.T) {
	repo := newProbeRepo(t)
	entries, ok := Probe([]string{repo})[repo]
	if !ok {
		t.Fatal("want a healthy repo present in the probe result")
	}
	if len(entries) != 0 {
		t.Fatalf("got %v, want no linked worktrees", entries)
	}
}

func TestProbeOmitsReposItCannotRead(t *testing.T) {
	// A missing key is the probe's "unknown", never "empty" (see
	// Probe's doc): a repo that was deleted, or a path that was never a
	// repo, must not read as "all worktrees removed".
	probed := Probe([]string{filepath.Join(t.TempDir(), "never-existed")})
	if _, ok := probed[filepath.Join(t.TempDir(), "never-existed")]; ok {
		t.Fatal("want a nonexistent path omitted")
	}
	notARepo := t.TempDir()
	if _, ok := Probe([]string{notARepo})[notARepo]; ok {
		t.Fatal("want a non-repo directory omitted")
	}
}

func TestProbeSkipsAnIncompleteAdminDir(t *testing.T) {
	// git worktree add creates the admin dir before its gitdir/HEAD
	// files; a probe landing mid-creation must skip the entry (the next
	// probe reads it complete) rather than fail the repo or report a
	// bogus path.
	repo := newProbeRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".git", "worktrees", "partial"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, ok := Probe([]string{repo})[repo]
	if !ok {
		t.Fatal("want the repo still readable")
	}
	if len(entries) != 0 {
		t.Fatalf("got %v, want the incomplete admin dir skipped", entries)
	}
}

func TestProbeReportsDetachedHeadWithNoBranch(t *testing.T) {
	repo := newProbeRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "detached")
	runProbeGit(t, repo, "worktree", "add", "-q", "--detach", wt)

	entries, ok := Probe([]string{repo})[repo]
	if !ok || len(entries) != 1 {
		t.Fatalf("got %v (ok=%v), want the one detached worktree", entries, ok)
	}
	if entries[0].Branch != "" {
		t.Fatalf("got branch %q, want \"\" for a detached HEAD", entries[0].Branch)
	}
	if want := resolve(t, wt); entries[0].Path != want {
		t.Fatalf("got path %q, want %q", entries[0].Path, want)
	}
}

func TestProbeWorksFromALinkedWorktreeCheckout(t *testing.T) {
	// A registered path whose .git is a FILE (the checkout is itself a
	// linked worktree): the probe resolves the commondir indirection
	// and reports the shared admin area's worktrees.
	repo := newProbeRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "wt1")
	runProbeGit(t, repo, "worktree", "add", "-q", "-b", "feature", wt)

	key := resolve(t, wt)
	entries, ok := Probe([]string{key})[key]
	if !ok {
		t.Fatal("want the linked worktree checkout to probe successfully")
	}
	if len(entries) != 1 || entries[0].Path != key {
		t.Fatalf("got %v, want the shared admin area's one worktree at %q", entries, key)
	}
}
