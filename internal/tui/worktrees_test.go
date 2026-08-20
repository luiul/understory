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

func otherRepoEntry(path, branch string, commitAge time.Duration) worktree.Entry {
	return worktree.Entry{Owner: "other", Repo: "gizmos", Branch: branch, Path: path, CommitTime: time.Now().Add(-commitAge)}
}

func TestSortWorktreesGroupsEachRepoIntoOneContiguousBlock(t *testing.T) {
	// Interleaved input: acme/widgets, other/gizmos, acme/widgets again.
	// Sorting by pure recency would keep them interleaved; grouping should
	// pull every acme/widgets row together regardless of where it sits in
	// the input.
	acmeOld := wtEntry("/w/acme-old", "old", 10*time.Hour)
	gizmo := otherRepoEntry("/w/gizmo", "gizmo-branch", 5*time.Hour)
	acmeNew := wtEntry("/w/acme-new", "new", time.Hour)

	got := sortWorktrees([]worktree.Entry{acmeOld, gizmo, acmeNew})

	// acme/widgets' most recent commit (acmeNew, 1h ago) beats gizmo's
	// (5h ago), so the acme/widgets block sorts first, most-recent-first
	// within it; gizmo's block follows.
	want := []string{"/w/acme-new", "/w/acme-old", "/w/gizmo"}
	if strings.Join(pathsOf(got), ",") != strings.Join(want, ",") {
		t.Fatalf("got order %v, want %v (grouped by repo)", pathsOf(got), want)
	}
}

func TestSortWorktreesOrdersGroupsByTheirOwnMostRecentCommit(t *testing.T) {
	// gizmo's only commit (2h ago) is more recent than acme/widgets' most
	// recent commit (3h ago), so gizmo's block should sort first even
	// though it appears second in the input.
	acme := wtEntry("/w/acme", "acme-branch", 3*time.Hour)
	gizmo := otherRepoEntry("/w/gizmo", "gizmo-branch", 2*time.Hour)

	got := sortWorktrees([]worktree.Entry{acme, gizmo})

	want := []string{"/w/gizmo", "/w/acme"}
	if strings.Join(pathsOf(got), ",") != strings.Join(want, ",") {
		t.Fatalf("got order %v, want %v (gizmo's block first, it's more recent)", pathsOf(got), want)
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
	rows := buildWorktreeRows(nil, 0, "", nil, time.Now())
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 placeholder row", len(rows))
	}
	if len(rows[0]) != 8 {
		t.Fatalf("got %d cells, want 8 to match worktreeColumns", len(rows[0]))
	}
}

func TestBuildWorktreeRowsMarksTheCursorRow(t *testing.T) {
	rows := buildWorktreeRows([]worktree.Entry{wtEntry("/w/a", "a", 0), wtEntry("/w/b", "b", 0)}, 1, "", nil, time.Now())
	if rows[0][0] != "" || rows[1][0] != cursorMarker {
		t.Fatalf("got markers %q, %q, want cursor on row 1", rows[0][0], rows[1][0])
	}
}

func TestBuildWorktreeRowsBlanksTheRepeatedRepoLabelWithinAGroup(t *testing.T) {
	// Same repo (acme/widgets) back to back: only the first row should
	// carry the label, so the group reads as one block instead of
	// repeating the same text down every row.
	rows := buildWorktreeRows([]worktree.Entry{wtEntry("/w/a", "a", 0), wtEntry("/w/b", "b", time.Hour)}, 0, "", nil, time.Now())
	if rows[0][1] != "acme/widgets" {
		t.Fatalf("got %q, want the first row of a group to carry its repo label", rows[0][1])
	}
	if rows[1][1] != "" {
		t.Fatalf("got %q, want the repeated repo label blanked on the second row", rows[1][1])
	}
}

func TestBuildWorktreeRowsRelabelsWhenTheRepoChanges(t *testing.T) {
	rows := buildWorktreeRows([]worktree.Entry{wtEntry("/w/a", "a", 0), otherRepoEntry("/w/b", "b", time.Hour)}, 0, "", nil, time.Now())
	if rows[0][1] != "acme/widgets" || rows[1][1] != "other/gizmos" {
		t.Fatalf("got %q, %q, want both distinct repo labels shown", rows[0][1], rows[1][1])
	}
}

func TestWorktreeStatusLabelPrefersStaleOverDirty(t *testing.T) {
	if got := worktreeStatusLabel(worktree.Entry{Stale: true, Dirty: true}); got != "stale" {
		t.Fatalf("got %q, want stale to win over dirty", got)
	}
}

func TestWorktreeStatusLabelDirtyOrClean(t *testing.T) {
	if got := worktreeStatusLabel(worktree.Entry{Dirty: true}); got != "dirty" {
		t.Fatalf("got %q, want dirty", got)
	}
	if got := worktreeStatusLabel(worktree.Entry{}); got != "clean" {
		t.Fatalf("got %q, want clean", got)
	}
}

func TestMergeStatusLabelFallsBackToADashWhenNotApplicable(t *testing.T) {
	if got := mergeStatusLabel(worktree.Entry{}); got != "-" {
		t.Fatalf("got %q, want \"-\" for the zero value (main worktree or stale)", got)
	}
}

func TestMergeStatusLabelPassesThroughAComputedStatus(t *testing.T) {
	if got := mergeStatusLabel(worktree.Entry{MergeStatus: worktree.MergeStatusMerged}); got != worktree.MergeStatusMerged {
		t.Fatalf("got %q, want %q", got, worktree.MergeStatusMerged)
	}
}

func TestBuildWorktreeRowsShowsWorktreeAndMergeColumns(t *testing.T) {
	w := wtEntry("/w/a", "a", 0)
	w.Dirty = true
	w.MergeStatus = worktree.MergeStatusUnmerged
	rows := buildWorktreeRows([]worktree.Entry{w}, 0, "", nil, time.Now())
	if rows[0][colWorktree] != "dirty" {
		t.Fatalf("got %q, want the Worktree cell to read dirty", rows[0][colWorktree])
	}
	if rows[0][colMerge] != worktree.MergeStatusUnmerged {
		t.Fatalf("got %q, want the Merge cell to read %q", rows[0][colMerge], worktree.MergeStatusUnmerged)
	}
}

func TestBuildWorktreeRowsClosedColumnBlankWhenNeverObservedClosed(t *testing.T) {
	rows := buildWorktreeRows([]worktree.Entry{wtEntry("/w/a", "a", 0)}, 0, "", nil, time.Now())
	if rows[0][colClosed] != "" {
		t.Fatalf("got %q, want a blank Closed cell with no closedAt entry", rows[0][colClosed])
	}
}

func TestBuildWorktreeRowsClosedColumnShowsElapsedTime(t *testing.T) {
	now := time.Now()
	closedAt := map[string]time.Time{"/w/a": now.Add(-3 * time.Minute)}
	rows := buildWorktreeRows([]worktree.Entry{wtEntry("/w/a", "a", 0)}, 0, "", closedAt, now)
	if rows[0][colClosed] != "3m" {
		t.Fatalf("got %q, want %q", rows[0][colClosed], "3m")
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

func TestWorktreeSummaryLineBreaksDownByStatusInSalienceOrder(t *testing.T) {
	dirty := wtEntry("/w/dirty", "dirty-branch", 0)
	dirty.Dirty = true

	stale := wtEntry("/w/stale", "stale-branch", 0)
	stale.Stale = true

	merged := wtEntry("/w/merged", "merged-branch", 0)
	merged.MergeStatus = worktree.MergeStatusMerged

	clean := wtEntry("/w/clean", "clean-branch", 0)

	got := worktreeSummaryLine([]worktree.Entry{dirty, stale, merged, clean})

	// Order matters: dirty, stale, merged, clean, most actionable first.
	dirtyAt := strings.Index(got, "1 dirty")
	staleAt := strings.Index(got, "1 stale")
	mergedAt := strings.Index(got, "1 merged")
	cleanAt := strings.Index(got, "1 clean")
	if dirtyAt < 0 || staleAt < 0 || mergedAt < 0 || cleanAt < 0 {
		t.Fatalf("got %q, want all four buckets present", got)
	}
	if !(dirtyAt < staleAt && staleAt < mergedAt && mergedAt < cleanAt) {
		t.Fatalf("got %q, want dirty, stale, merged, clean in that left-to-right order", got)
	}
}

func TestWorktreeSummaryLineStaleWinsOverDirtyAndMerged(t *testing.T) {
	// Entry.Stale's doc: Dirty/MergeStatus are meaningless once a worktree's
	// directory is gone, so a stale entry must classify as stale even if it
	// also happens to carry a stray Dirty/MergeStatus value.
	w := wtEntry("/w/a", "a", 0)
	w.Stale = true
	w.Dirty = true
	w.MergeStatus = worktree.MergeStatusMerged

	got := worktreeSummaryLine([]worktree.Entry{w})

	if !strings.Contains(got, "1 stale") {
		t.Fatalf("got %q, want it classified as stale", got)
	}
	if strings.Contains(got, "dirty") || strings.Contains(got, "merged") {
		t.Fatalf("got %q, want no dirty/merged bucket once stale wins", got)
	}
}

func TestWorktreeSummaryLineOmitsZeroCountBuckets(t *testing.T) {
	got := worktreeSummaryLine([]worktree.Entry{wtEntry("/w/a", "a", 0)}) // clean only
	for _, bucket := range []string{"dirty", "stale", "merged"} {
		if strings.Contains(got, bucket) {
			t.Fatalf("got %q, want no %q bucket when its count is 0", got, bucket)
		}
	}
	if !strings.Contains(got, "1 clean") {
		t.Fatalf("got %q, want the clean bucket", got)
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
	cols := worktreeColumns(50, nil)
	last := cols[len(cols)-1]
	if last.Width < minPathWidth {
		t.Fatalf("got Path width %d, want at least %d", last.Width, minPathWidth)
	}
}

func TestWorktreeColumnsRepoGrowsToFitTheLongestLabelInsteadOfTruncating(t *testing.T) {
	long := wtEntry("/w/a", "a", 0)
	long.Owner = "hellofresh"
	long.Repo = "isa-orchestration-and-something-long"
	wantWidth := len(repoLabel(long)) // ASCII-only label; runewidth.StringWidth == len here

	cols := worktreeColumns(200, []worktree.Entry{long})

	if got := cols[colRepo].Width; got != wantWidth {
		t.Fatalf("got Repo width %d, want %d (the full label's width, untruncated)", got, wantWidth)
	}
}

func TestWorktreeColumnsRepoNeverShrinksBelowItsFloor(t *testing.T) {
	short := wtEntry("/w/a", "a", 0) // "acme/widgets", shorter than repoColWidth

	cols := worktreeColumns(200, []worktree.Entry{short})

	if got := cols[colRepo].Width; got != repoColWidth {
		t.Fatalf("got Repo width %d, want the floor %d for a short label", got, repoColWidth)
	}
}
