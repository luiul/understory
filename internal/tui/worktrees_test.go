package tui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/luiul/understory/internal/worktree"
)

func wtEntry(path, branch string, commitAge time.Duration) worktree.Entry {
	t := time.Now().Add(-commitAge)
	return worktree.Entry{Owner: "acme", Repo: "widgets", Branch: branch, Path: path, CommitTime: t, CreatedTime: t}
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
	t := time.Now().Add(-commitAge)
	return worktree.Entry{Owner: "other", Repo: "gizmos", Branch: branch, Path: path, CommitTime: t, CreatedTime: t}
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
	m := New(999, false)
	m.worktrees = []worktree.Entry{wtEntry("/w/a", "a", time.Hour), wtEntry("/w/b", "b", time.Minute)}

	got := m.displayedWorktrees()

	if len(got) != 2 {
		t.Fatalf("got %d worktrees, want 2", len(got))
	}
}

func TestDisplayedWorktreesHidesMainByDefault(t *testing.T) {
	main := wtEntry("/w/main", "main", time.Hour)
	main.IsMain = true
	branch := wtEntry("/w/branch", "feature", time.Minute)

	m := New(999, false)
	m.worktrees = []worktree.Entry{main, branch}

	got := m.displayedWorktrees()

	want := []string{"/w/branch"}
	if strings.Join(pathsOf(got), ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v (main worktree hidden)", pathsOf(got), want)
	}
}

func TestDisplayedWorktreesShowsMainWhenRequested(t *testing.T) {
	main := wtEntry("/w/main", "main", time.Hour)
	main.IsMain = true
	branch := wtEntry("/w/branch", "feature", time.Minute)

	m := New(999, true)
	m.worktrees = []worktree.Entry{main, branch}

	got := m.displayedWorktrees()

	if len(got) != 2 {
		t.Fatalf("got %d worktrees, want 2 (--show-main should include the main worktree)", len(got))
	}
}

func TestApplyWorktreesKeepsThePreviouslySelectedPathSelected(t *testing.T) {
	m := New(999, false)
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

func TestBuildWorktreeRowsCreatedColumnUsesCreatedTimeNotCommitTime(t *testing.T) {
	// A branch can go a long time between commits while the worktree
	// itself is much younger (or vice versa): the Created column must
	// track Entry.CreatedTime specifically, not fall back to reading
	// CommitTime, or a stale-but-recently-created worktree (or a
	// long-lived worktree with a very fresh commit) would show the wrong
	// age entirely.
	w := worktree.Entry{
		Owner:       "acme",
		Repo:        "widgets",
		Branch:      "feature",
		Path:        "/w/a",
		CommitTime:  time.Now(),
		CreatedTime: time.Now().Add(-3 * 24 * time.Hour),
	}

	rows := buildWorktreeRows([]worktree.Entry{w}, -1, "", time.Now())

	if got := rows[0][colCreated]; got != "3d" {
		t.Fatalf("got %q, want \"3d\" (from CreatedTime, not CommitTime's ~0s)", got)
	}
}

func TestBuildWorktreeRowsTagsTheCursorRowsCreatedCell(t *testing.T) {
	rows := buildWorktreeRows([]worktree.Entry{wtEntry("/w/a", "a", 0), wtEntry("/w/b", "b", 0)}, 1, "", time.Now())
	if strings.Contains(rows[0][colCreated], cursorSentinel) {
		t.Fatalf("got cursorSentinel on non-cursor row 0's Created cell %q, want it absent", rows[0][colCreated])
	}
	if !strings.Contains(rows[1][colCreated], cursorSentinel) {
		t.Fatalf("got %q, want row 1's Created cell to carry cursorSentinel (it's the cursor row)", rows[1][colCreated])
	}
}

func TestBuildWorktreeRowsBlanksTheRepeatedRepoLabelWithinAGroup(t *testing.T) {
	// Same repo (acme/widgets) back to back: only the first row should
	// carry the label, so the group reads as one block instead of
	// repeating the same text down every row.
	rows := buildWorktreeRows([]worktree.Entry{wtEntry("/w/a", "a", 0), wtEntry("/w/b", "b", time.Hour)}, 0, "", time.Now())
	if rows[0][colRepo] != "acme/widgets" {
		t.Fatalf("got %q, want the first row of a group to carry its repo label", rows[0][colRepo])
	}
	if rows[1][colRepo] != "" {
		t.Fatalf("got %q, want the repeated repo label blanked on the second row", rows[1][colRepo])
	}
}

func TestBuildWorktreeRowsRelabelsWhenTheRepoChanges(t *testing.T) {
	rows := buildWorktreeRows([]worktree.Entry{wtEntry("/w/a", "a", 0), otherRepoEntry("/w/b", "b", time.Hour)}, 0, "", time.Now())
	if rows[0][colRepo] != "acme/widgets" || rows[1][colRepo] != "other/gizmos" {
		t.Fatalf("got %q, %q, want both distinct repo labels shown", rows[0][colRepo], rows[1][colRepo])
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
	rows := buildWorktreeRows([]worktree.Entry{w}, 0, "", time.Now())
	if rows[0][colWorktree] != "dirty" {
		t.Fatalf("got %q, want the Worktree cell to read dirty", rows[0][colWorktree])
	}
	if rows[0][colMerge] != worktree.MergeStatusUnmerged {
		t.Fatalf("got %q, want the Merge cell to read %q", rows[0][colMerge], worktree.MergeStatusUnmerged)
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
	for _, bucket := range []string{"dirty", "stale", "merged", "unknown"} {
		if strings.Contains(got, bucket) {
			t.Fatalf("got %q, want no %q bucket when its count is 0", got, bucket)
		}
	}
	if !strings.Contains(got, "1 clean") {
		t.Fatalf("got %q, want the clean bucket", got)
	}
}

func TestWorktreeSummaryLineUnknownOnly(t *testing.T) {
	// Entry.MergeStatus's doc: MergeStatusUnknown covers anything `wt`
	// reports beyond empty/integrated/ahead. It must render as its own
	// "unknown" bucket, not get silently folded into (and overstate the
	// confidence of) "clean".
	w := wtEntry("/w/a", "a", 0)
	w.MergeStatus = worktree.MergeStatusUnknown

	got := worktreeSummaryLine([]worktree.Entry{w})

	if !strings.Contains(got, "1 unknown") {
		t.Fatalf("got %q, want the unknown bucket", got)
	}
	if strings.Contains(got, "clean") {
		t.Fatalf("got %q, want no clean bucket for an unknown-only entry", got)
	}
}

func TestWorktreeSummaryLineCleanAndUnknownStayDistinctBucketsWhenBothPresent(t *testing.T) {
	// A mix of clean and unknown entries must keep separate, exact counts
	// ("N clean · M unknown") rather than merging into one ambiguous
	// combined count that hides how many of each there actually are.
	clean := wtEntry("/w/clean", "clean-branch", 0)
	unknownA := wtEntry("/w/unknown-a", "unknown-branch-a", 0)
	unknownA.MergeStatus = worktree.MergeStatusUnknown
	unknownB := wtEntry("/w/unknown-b", "unknown-branch-b", 0)
	unknownB.MergeStatus = worktree.MergeStatusUnknown

	got := worktreeSummaryLine([]worktree.Entry{clean, unknownA, unknownB})

	if !strings.Contains(got, "1 clean") {
		t.Fatalf("got %q, want an exact 1 clean count", got)
	}
	if !strings.Contains(got, "2 unknown") {
		t.Fatalf("got %q, want an exact 2 unknown count", got)
	}
	cleanAt := strings.Index(got, "1 clean")
	unknownAt := strings.Index(got, "2 unknown")
	if cleanAt < 0 || unknownAt < 0 || !(cleanAt < unknownAt) {
		t.Fatalf("got %q, want clean to sort before unknown", got)
	}
}

func TestWorktreeSummaryLineBucketCountsSumToTotal(t *testing.T) {
	// Regression guard for the classification switch's mutual exclusivity:
	// every entry must land in exactly one bucket, so the digits across all
	// rendered buckets must always add up to the total worktree count, no
	// matter how the five-way classification evolves later.
	dirty := wtEntry("/w/dirty", "dirty-branch", 0)
	dirty.Dirty = true
	stale := wtEntry("/w/stale", "stale-branch", 0)
	stale.Stale = true
	merged := wtEntry("/w/merged", "merged-branch", 0)
	merged.MergeStatus = worktree.MergeStatusMerged
	clean := wtEntry("/w/clean", "clean-branch", 0)
	unknown := wtEntry("/w/unknown", "unknown-branch", 0)
	unknown.MergeStatus = worktree.MergeStatusUnknown

	entries := []worktree.Entry{dirty, stale, merged, clean, unknown}
	got := worktreeSummaryLine(entries)

	sum := 0
	for _, bucket := range []string{"dirty", "stale", "merged", "clean", "unknown"} {
		matches := regexp.MustCompile(`(\d+) ` + bucket).FindStringSubmatch(got)
		if matches == nil {
			t.Fatalf("got %q, want a %q bucket present", got, bucket)
		}
		n, err := strconv.Atoi(matches[1])
		if err != nil {
			t.Fatalf("got %q, unparseable %q bucket count: %v", got, bucket, err)
		}
		sum += n
	}
	if sum != len(entries) {
		t.Fatalf("got bucket counts summing to %d, want %d (len(entries))", sum, len(entries))
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

func TestWorktreeColumnsPathKeepsItsFloorWheneverThereIsRoom(t *testing.T) {
	// With no worktrees, every column sits at its default floor, so any
	// terminal wide enough for the defaults plus minPathWidth must give
	// Path at least minPathWidth.
	need := repoColWidth + branchColWidth + createdColWidth + worktreeColWidth + mergeColWidth + 2*6
	cols := worktreeColumns(need+minPathWidth, nil, nil)
	last := cols[len(cols)-1]
	if last.Width < minPathWidth {
		t.Fatalf("got Path width %d, want at least %d", last.Width, minPathWidth)
	}
}

func TestWorktreeColumnsNeverOverflowsTheTerminal(t *testing.T) {
	// A wide Repo label and a wide Branch name together used to push the
	// table past the terminal's right edge: Path floored at minPathWidth
	// regardless, and the terminal clipped the overflow away. The layout
	// must shed Repo/Branch's growth instead.
	longRepo := wtEntry("/w/a", "main", 0)
	longRepo.Owner = "hellofresh"
	longRepo.Repo = "tardis-community"                                                 // Repo content width: 27
	longBranch := wtEntry("/w/b", "issue/ISA-18408_dedupe-satellite-replay-echoes", 0) // Branch content width: 46
	longBranch.Owner = "hellofresh"
	longBranch.Repo = "tardis-community"
	worktrees := []worktree.Entry{longRepo, longBranch}

	const width = 120
	cols := worktreeColumns(width, worktrees, nil)

	total := 2 * len(cols)
	for _, c := range cols {
		total += c.Width
	}
	if total > width {
		t.Fatalf("got total table width %d, want <= terminal width %d", total, width)
	}
	// Path keeps its full floor; the 10 missing columns come out of
	// Branch's growth (the widest growable column), Repo stays untouched.
	if got := cols[colPath].Width; got != minPathWidth {
		t.Fatalf("got Path width %d, want %d", got, minPathWidth)
	}
	if got, want := cols[colRepo].Width, 27; got != want {
		t.Fatalf("got Repo width %d, want %d (untouched, it isn't the widest)", got, want)
	}
	if got, want := cols[colBranch].Width, 35; got != want {
		t.Fatalf("got Branch width %d, want %d (its growth shed to fit)", got, want)
	}
}

func TestWorktreeColumnsWaterFillsRepoAndBranchWhenBothMustShrink(t *testing.T) {
	// When one column's growth alone can't cover the shortfall, both
	// growable columns shrink, equalizing from the top: truncation always
	// hits the longest displayed value first, whichever column it's in.
	longRepo := wtEntry("/w/a", "main", 0)
	longRepo.Owner = "hellofresh"
	longRepo.Repo = "tardis-community"                                                 // 27
	longBranch := wtEntry("/w/b", "issue/ISA-18408_dedupe-satellite-replay-echoes", 0) // 46
	longBranch.Owner = "hellofresh"
	longBranch.Repo = "tardis-community"

	const width = 100
	cols := worktreeColumns(width, []worktree.Entry{longRepo, longBranch}, nil)

	total := 2 * len(cols)
	for _, c := range cols {
		total += c.Width
	}
	if total > width {
		t.Fatalf("got total table width %d, want <= terminal width %d", total, width)
	}
	if got := cols[colPath].Width; got != minPathWidth {
		t.Fatalf("got Path width %d, want %d", got, minPathWidth)
	}
	// 31 columns short: Branch sheds 46→27, then the two alternate down
	// to 21 each (ties break toward Repo first).
	if got, want := cols[colRepo].Width, 21; got != want {
		t.Fatalf("got Repo width %d, want %d", got, want)
	}
	if got, want := cols[colBranch].Width, 21; got != want {
		t.Fatalf("got Branch width %d, want %d", got, want)
	}
}

func TestWorktreeColumnsPathDipsBelowItsFloorOnlyOnceGrowthIsExhausted(t *testing.T) {
	// Past the point where Repo/Branch have shed all their growth (both
	// sit at their default floors), Path itself dips below minPathWidth
	// rather than the table overflowing.
	longRepo := wtEntry("/w/a", "main", 0)
	longRepo.Owner = "hellofresh"
	longRepo.Repo = "tardis-community"                                                 // 27
	longBranch := wtEntry("/w/b", "issue/ISA-18408_dedupe-satellite-replay-echoes", 0) // 46
	longBranch.Owner = "hellofresh"
	longBranch.Repo = "tardis-community"

	const width = 90
	cols := worktreeColumns(width, []worktree.Entry{longRepo, longBranch}, nil)

	if got, want := cols[colRepo].Width, repoColWidth; got != want {
		t.Fatalf("got Repo width %d, want its default floor %d", got, want)
	}
	if got, want := cols[colBranch].Width, branchColWidth; got != want {
		t.Fatalf("got Branch width %d, want its default floor %d", got, want)
	}
	if got, want := cols[colPath].Width, 16; got != want {
		t.Fatalf("got Path width %d, want %d (below its floor, but the table fits)", got, want)
	}
}

func TestWorktreeColumnsNeverCrushesAnythingBelowTheHardFloor(t *testing.T) {
	// On an absurdly narrow terminal even the default floors don't fit:
	// Repo/Branch shrink to hardMinColWidth and Path floors there too,
	// accepting the remaining overflow rather than crushing columns to
	// nothing.
	cols := worktreeColumns(50, nil, nil)
	if got := cols[colPath].Width; got != hardMinColWidth {
		t.Fatalf("got Path width %d, want the hard floor %d", got, hardMinColWidth)
	}
	if got := cols[colRepo].Width; got != hardMinColWidth {
		t.Fatalf("got Repo width %d, want the hard floor %d", got, hardMinColWidth)
	}
	if got := cols[colBranch].Width; got != hardMinColWidth {
		t.Fatalf("got Branch width %d, want the hard floor %d", got, hardMinColWidth)
	}
}

func TestWorktreeColumnsNeverAutoShrinksAnOverriddenColumn(t *testing.T) {
	// A column the user widened by hand is pinned: when the layout runs
	// out of room, Path absorbs the shortfall rather than the override
	// being silently shrunk.
	longBranch := wtEntry("/w/b", "issue/ISA-18408_dedupe-satellite-replay-echoes", 0) // 46
	overrides := map[int]int{colBranch: 46}

	const width = 90
	cols := worktreeColumns(width, []worktree.Entry{longBranch}, overrides)

	if got := cols[colBranch].Width; got != 46 {
		t.Fatalf("got Branch width %d, want the pinned 46", got)
	}
	if got := cols[colPath].Width; got >= minPathWidth {
		t.Fatalf("got Path width %d, want it below %d (it absorbed the shortfall)", got, minPathWidth)
	}
}

func TestWorktreeColumnsRepoGrowsToFitTheLongestLabelInsteadOfTruncating(t *testing.T) {
	long := wtEntry("/w/a", "a", 0)
	long.Owner = "hellofresh"
	long.Repo = "isa-orchestration-and-something-long"
	wantWidth := len(repoLabel(long)) // ASCII-only label; runewidth.StringWidth == len here

	cols := worktreeColumns(200, []worktree.Entry{long}, nil)

	if got := cols[colRepo].Width; got != wantWidth {
		t.Fatalf("got Repo width %d, want %d (the full label's width, untruncated)", got, wantWidth)
	}
}

func TestWorktreeColumnsRepoNeverShrinksBelowItsFloor(t *testing.T) {
	short := wtEntry("/w/a", "a", 0) // "acme/widgets", shorter than repoColWidth

	cols := worktreeColumns(200, []worktree.Entry{short}, nil)

	if got := cols[colRepo].Width; got != repoColWidth {
		t.Fatalf("got Repo width %d, want the floor %d for a short label", got, repoColWidth)
	}
}

func TestWorktreeColumnsTitlesNeverTouchTheirRightBorder(t *testing.T) {
	// The header's column-border glyph (loam.DrawHeaderBorders) sits
	// immediately right of each column's content area, so a column
	// exactly as wide as its title renders "Title│" with the border
	// touching the text. Every column must be at least title+1 wide.
	for _, c := range worktreeColumns(200, nil, nil) {
		if got, need := c.Width, runewidth.StringWidth(c.Title)+1; got < need {
			t.Errorf("column %q width %d, want at least %d (title + 1 space before the border)", c.Title, got, need)
		}
	}
}

func TestWorktreeColumnsAppliesAnOverrideWiderThanTheNaturalFloor(t *testing.T) {
	overrides := map[int]int{colRepo: repoColWidth + 10}

	cols := worktreeColumns(200, nil, overrides)

	if got, want := cols[colRepo].Width, repoColWidth+10; got != want {
		t.Fatalf("got Repo width %d, want %d (the override, wider than the floor)", got, want)
	}
}

func TestWorktreeColumnsAppliesAnOverrideNarrowerThanTheContent(t *testing.T) {
	// An override applies absolutely, in both directions: a drag is a
	// deliberate pin, so a column the user narrowed stays narrow even
	// when a freshly polled longer label would have grown it — the label
	// truncates with an ellipsis until the user drags wider again or
	// resizes the terminal (which clears every override). Dropping the
	// override instead would make every shrink drag snap back on the
	// next poll, which is exactly the "resizing doesn't work" bug this
	// test guards.
	long := wtEntry("/w/a", "a", 0)
	long.Owner = "hellofresh"
	long.Repo = "isa-orchestration-and-something-long"

	overrides := map[int]int{colRepo: repoColWidth + 2} // narrower than the label's own width

	cols := worktreeColumns(200, []worktree.Entry{long}, overrides)

	if got, want := cols[colRepo].Width, repoColWidth+2; got != want {
		t.Fatalf("got Repo width %d, want %d (the user's pin, not the label's own width)", got, want)
	}
}

func TestWorktreeColumnsNeverAppliesAnOverrideToPath(t *testing.T) {
	// Path always absorbs whatever's left over, the same invariant a
	// mouse drag itself already keeps (see trellis.Model.Handle's doc):
	// an override recorded against it would make no sense and must be
	// ignored entirely.
	overrides := map[int]int{colPath: 9999}

	cols := worktreeColumns(200, nil, overrides)

	last := cols[len(cols)-1]
	if last.Width == 9999 {
		t.Fatalf("got Path width %d, want it computed from leftover space, not the override", last.Width)
	}
}
