package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/luiul/understory/internal/worktree"
)

func wtEntry(path, branch string, commitAge time.Duration) worktree.Entry {
	return worktree.Entry{Owner: "acme", Repo: "widgets", Branch: branch, Path: path, CommitTime: time.Now().Add(-commitAge)}
}

func pathsOf(worktrees []worktree.Entry) []string {
	paths := make([]string, len(worktrees))
	for i, w := range worktrees {
		paths[i] = w.Path
	}
	return paths
}

func TestSortWorktreesOrdersMostRecentlyCommittedFirst(t *testing.T) {
	stale := wtEntry("/w/stale", "stale", 30*24*time.Hour)
	fresh := wtEntry("/w/fresh", "fresh", time.Hour)
	freshest := wtEntry("/w/freshest", "freshest", time.Minute)

	got := sortWorktrees([]worktree.Entry{stale, fresh, freshest})

	want := []string{"/w/freshest", "/w/fresh", "/w/stale"}
	if strings.Join(pathsOf(got), ",") != strings.Join(want, ",") {
		t.Fatalf("got order %v, want %v", pathsOf(got), want)
	}
}

func TestDisplayedWorktreesReturnsEveryKnownWorktree(t *testing.T) {
	m := New(999)
	m.worktrees = []worktree.Entry{wtEntry("/w/a", "a", time.Hour), wtEntry("/w/b", "b", time.Minute)}

	got := m.displayedWorktrees()

	if len(got) != 2 {
		t.Fatalf("got %d worktrees, want 2", len(got))
	}
}

func TestApplyWorktreesKeepsThePreviouslySelectedPathSelected(t *testing.T) {
	m := New(999)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", time.Hour), wtEntry("/w/b", "b", time.Minute)})
	m.table.SetCursor(1) // select /w/b (most recently committed, so index 0... adjust below)

	// /w/b is more recent, so it's already at index 0 after sort; select it
	// explicitly by path via the cursor, then re-poll with a reordering
	// that would otherwise shift it.
	m.table.SetCursor(0)
	before, ok := m.selectedWorktree()
	if !ok {
		t.Fatalf("want a selected worktree before re-poll")
	}

	// Re-poll with a new worktree committed even more recently, pushing
	// the previously selected one further down the sorted list.
	newer := wtEntry("/w/newer", "newer", 0)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", time.Hour), wtEntry("/w/b", "b", time.Minute), newer})

	after, ok := m.selectedWorktree()
	if !ok || after.Path != before.Path {
		t.Fatalf("got %+v, want the same path (%s) still selected after re-poll", after, before.Path)
	}
}

func TestBuildWorktreeRowsPlaceholderWhenEmpty(t *testing.T) {
	rows := buildWorktreeRows(nil, 0, "", time.Now())
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 placeholder row", len(rows))
	}
	if len(rows[0]) != 6 {
		t.Fatalf("got %d cells, want 6 to match worktreeColumns", len(rows[0]))
	}
}

func TestBuildWorktreeRowsMarksTheCursorRow(t *testing.T) {
	rows := buildWorktreeRows([]worktree.Entry{wtEntry("/w/a", "a", 0), wtEntry("/w/b", "b", 0)}, 1, "", time.Now())
	if rows[0][0] != "" || rows[1][0] != cursorMarker {
		t.Fatalf("got markers %q, %q, want cursor on row 1", rows[0][0], rows[1][0])
	}
}

func TestWorktreeSummaryLineCountsWorktrees(t *testing.T) {
	got := worktreeSummaryLine([]worktree.Entry{wtEntry("/w/a", "a", 0), wtEntry("/w/b", "b", 0)})
	if !strings.Contains(got, "2 worktrees") {
		t.Fatalf("got %q, want it to mention 2 worktrees", got)
	}
}

func TestWorktreeSummaryLineSingularForOne(t *testing.T) {
	got := worktreeSummaryLine([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	if !strings.Contains(got, "1 worktree") || strings.Contains(got, "1 worktrees") {
		t.Fatalf("got %q, want singular 'worktree'", got)
	}
}

func TestWorktreeSummaryLineEmptyWhenNoWorktrees(t *testing.T) {
	if got := worktreeSummaryLine(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestResolveWorktreeCursorPrefersTheMatchingPath(t *testing.T) {
	displayed := []worktree.Entry{wtEntry("/w/a", "a", 0), wtEntry("/w/b", "b", 0)}
	if got := resolveWorktreeCursor(displayed, "/w/b", 0); got != 1 {
		t.Fatalf("got %d, want 1 (index of the matching path)", got)
	}
}

func TestResolveWorktreeCursorFallsBackWhenPathIsGone(t *testing.T) {
	displayed := []worktree.Entry{wtEntry("/w/a", "a", 0)}
	if got := resolveWorktreeCursor(displayed, "/w/gone", 0); got != 0 {
		t.Fatalf("got %d, want the clamped fallback 0", got)
	}
}

func TestWorktreeColumnsPathNeverShrinksBelowTheFloor(t *testing.T) {
	cols := worktreeColumns(50)
	last := cols[len(cols)-1]
	if last.Width < minPathWidth {
		t.Fatalf("got Path width %d, want at least %d", last.Width, minPathWidth)
	}
}
