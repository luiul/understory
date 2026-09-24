package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luiul/understory/internal/worktree"
)

// TestMain redirects the poll-cache seam to a temp dir for the whole
// package: every test that builds a Model via New (nearly all of them)
// loads the cache, and every poll-finishing path saves it — without the
// redirect the suite would read and overwrite the developer's real
// ~/.cache/understory/poll-cache.json.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "understory-tui-test-cache")
	if err != nil {
		panic(err)
	}
	pollCachePath = func() string { return filepath.Join(dir, "poll-cache.json") }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// useTempCache points the poll-cache seam at a fresh per-test file (so
// cache tests don't share state through TestMain's package-wide one)
// and returns its path.
func useTempCache(t *testing.T) string {
	t.Helper()
	orig := pollCachePath
	t.Cleanup(func() { pollCachePath = orig })
	path := filepath.Join(t.TempDir(), "poll-cache.json")
	pollCachePath = func() string { return path }
	return path
}

func TestPollCacheRoundTrip(t *testing.T) {
	useTempCache(t)
	entries := []worktree.Entry{
		{Repo: "understory", Owner: "luiul", Branch: "main", Path: "/w/main", RepoPath: "/repo", IsMain: true, CommitTime: time.Unix(1700000000, 0)},
		{Repo: "canopy", Owner: "luiul", Branch: "feat-x", Path: "/w/feat-x", RepoPath: "/repo2", Dirty: true, MergeStatus: worktree.MergeStatusUnmerged},
	}
	vscode := map[string]vscodeState{"/w/main": vscodeOpen, "/w/feat-x": vscodeClosed}
	strict := map[string]bool{"/w/main": true}

	savePollCache(entries, vscode, strict)

	c := loadPollCache()
	if c == nil {
		t.Fatal("want a loaded cache after a save")
	}
	if c.Version != cacheVersion {
		t.Fatalf("got version %d, want %d", c.Version, cacheVersion)
	}
	if len(c.Entries) != 2 || c.Entries[0].Branch != "main" || c.Entries[1].MergeStatus != worktree.MergeStatusUnmerged {
		t.Fatalf("got entries %+v, want the saved two", c.Entries)
	}
	if c.VSCode["/w/main"] != vscodeOpen || c.VSCode["/w/feat-x"] != vscodeClosed {
		t.Fatalf("got vscode %+v, want the saved states", c.VSCode)
	}
	if !c.VSCodeStrict["/w/main"] {
		t.Fatalf("got strict %+v, want /w/main true", c.VSCodeStrict)
	}
	if c.SavedAt.IsZero() {
		t.Fatal("want SavedAt stamped")
	}
}

func TestLoadPollCacheReturnsNilForMissingFile(t *testing.T) {
	useTempCache(t) // nothing saved
	if c := loadPollCache(); c != nil {
		t.Fatalf("got %+v, want nil for a missing cache file", c)
	}
}

func TestLoadPollCacheIgnoresCorruptJSON(t *testing.T) {
	path := useTempCache(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := loadPollCache(); c != nil {
		t.Fatalf("got %+v, want nil for a corrupt cache", c)
	}
}

func TestLoadPollCacheIgnoresAVersionMismatch(t *testing.T) {
	path := useTempCache(t)
	if err := os.WriteFile(path, []byte(`{"version": 999, "entries": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := loadPollCache(); c != nil {
		t.Fatalf("got %+v, want nil for a foreign schema version", c)
	}
}

func TestNewSeedsTheModelFromTheCache(t *testing.T) {
	useTempCache(t)
	savePollCache(
		[]worktree.Entry{{Repo: "understory", Branch: "feat-y", Path: "/w/feat-y", RepoPath: "/repo"}},
		map[string]vscodeState{"/w/feat-y": vscodeOpen},
		map[string]bool{"/w/feat-y": true},
	)

	m := New(999, false)

	if len(m.worktrees) != 1 || m.worktrees[0].Branch != "feat-y" {
		t.Fatalf("got worktrees %+v, want the cached row", m.worktrees)
	}
	if m.vscode["/w/feat-y"] != vscodeOpen || !m.vscodeStrict["/w/feat-y"] {
		t.Fatalf("got vscode maps %+v / %+v, want the cached states", m.vscode, m.vscodeStrict)
	}
	// The cache is last session's data: until the first poll lands the
	// summary line must say it's refreshing, not present the rows as
	// current.
	if m.pollsLanded != 0 || !m.pollInFlight {
		t.Fatalf("got pollsLanded=%d pollInFlight=%v, want 0/true (first poll still owed)", m.pollsLanded, m.pollInFlight)
	}
	if summary := m.summaryLine(); !strings.Contains(summary, "refreshing…") {
		t.Fatalf("got summary %q, want the refreshing… marker on a warm start", summary)
	}
}

func TestNewWithoutACacheStartsCold(t *testing.T) {
	useTempCache(t) // nothing saved
	m := New(999, false)
	if len(m.worktrees) != 0 || m.vscode != nil || m.vscodeStrict != nil {
		t.Fatalf("got worktrees=%+v vscode=%+v strict=%+v, want an empty cold model", m.worktrees, m.vscode, m.vscodeStrict)
	}
}

func TestRefreshingMarkerDisappearsOnceTheFirstPollLands(t *testing.T) {
	useTempCache(t)
	savePollCache(
		[]worktree.Entry{{Repo: "understory", Branch: "feat-y", Path: "/w/feat-y", RepoPath: "/repo"}},
		nil, nil,
	)
	m := New(999, false)

	updated, _ := m.Update(pollResultMsg{results: []worktree.RepoResult{okResult("/repo")}})
	mm := updated.(Model)

	if summary := mm.summaryLine(); strings.Contains(summary, "refreshing…") {
		t.Fatalf("got summary %q, want the refreshing… marker gone after the first poll", summary)
	}
}
