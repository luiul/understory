package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luiul/dashkit/loam"
	"github.com/luiul/dashkit/mycelium"
	"github.com/luiul/understory/internal/worktree"
)

func TestEnterOnARowOpensTheSelectedWorktree(t *testing.T) {
	target := wtEntry("/w/target", "feature", 0)
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{target})
	m.table.SetCursor(0)

	selected, ok := m.selectedWorktree()
	if !ok {
		t.Fatal("want a selected worktree")
	}
	if selected.Path != target.Path {
		t.Fatalf("got %+v, want %+v", selected, target)
	}
	if m.enterCmd() == nil {
		t.Fatal("want enterCmd to return a command for a valid selection")
	}
}

func TestEnterCmdIsNilWhenNothingIsSelected(t *testing.T) {
	m := New(999, false)
	if cmd := m.enterCmd(); cmd != nil {
		t.Fatal("want no command when the placeholder row is showing")
	}
}

func TestEnterCmdThreadsTheSelectedRowsPathAndBranchToMycelium(t *testing.T) {
	// mycelium matches windows on rootName+branch together and can find a
	// nested subpackage window by the branch in its title — but only if
	// understory actually hands the branch over, not just the path.
	orig := openVSCode
	t.Cleanup(func() { openVSCode = orig })
	var gotPath, gotBranch string
	openVSCode = func(path, branch string) mycelium.Result {
		gotPath, gotBranch = path, branch
		return mycelium.Result{OK: true, Message: "Focused VS Code window for " + path + "."}
	}

	target := wtEntry("/w/tardis-community", "patch/ISA-18409", 0)
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{target})
	m.table.SetCursor(0)

	cmd := m.enterCmd()
	if cmd == nil {
		t.Fatal("want enterCmd to return a command for a valid selection")
	}
	msg := cmd()
	if _, ok := msg.(openResultMsg); !ok {
		t.Fatalf("got %T, want openResultMsg", msg)
	}
	if gotPath != "/w/tardis-community" || gotBranch != "patch/ISA-18409" {
		t.Fatalf("got (%q, %q), want (%q, %q)", gotPath, gotBranch, "/w/tardis-community", "patch/ISA-18409")
	}
}

func TestOpenResultMsgSetsNotificationAndSchedulesClear(t *testing.T) {
	m := New(999, false)
	updated, cmd := m.Update(openResultMsg{result: mycelium.Result{OK: true, Message: "Focused VS Code window."}})
	mm := updated.(Model)

	if mm.notification != "Focused VS Code window." {
		t.Fatalf("got notification %q", mm.notification)
	}
	if mm.notifyIsError {
		t.Fatal("want notifyIsError=false for a successful open")
	}
	if cmd == nil {
		t.Fatal("want a clear-notification command to be scheduled")
	}

	cleared, _ := mm.Update(clearNotifyMsg{token: mm.notifyToken})
	if cleared.(Model).notification != "" {
		t.Fatal("want notification cleared once its own token fires")
	}
}

func TestStaleClearNotifyMsgIsIgnored(t *testing.T) {
	m := New(999, false)
	updated, _ := m.Update(openResultMsg{result: mycelium.Result{OK: false, Message: "nope"}})
	mm := updated.(Model)

	stale, _ := mm.Update(clearNotifyMsg{token: mm.notifyToken - 1})
	if stale.(Model).notification != "nope" {
		t.Fatal("want notification to survive a stale clear token")
	}
}

func TestFocusMsgTriggersAnImmediatePoll(t *testing.T) {
	// Switching to understory's window must refresh right away (see the
	// tea.FocusMsg case in Update): a worktree created in another window
	// can't wait up to interval for the next tick.
	m := New(999, false)
	_, cmd := m.Update(tea.FocusMsg{})
	if cmd == nil {
		t.Fatal("want a poll command on focus")
	}
}

func TestQuitKeyStopsTheProgram(t *testing.T) {
	m := New(999, false)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !updated.(Model).quitting {
		t.Fatal("want quitting=true after q")
	}
	if cmd == nil {
		t.Fatal("want tea.Quit to be returned")
	}
}

func TestShortenHomeReplacesTheHomePrefixWithATilde(t *testing.T) {
	cases := []struct {
		path, home, want string
	}{
		{"/Users/luis/worktrees/understory", "/Users/luis", "~/worktrees/understory"},
		{"/Users/luis", "/Users/luis", "~"},
		{"/Users/luisandro/projects", "/Users/luis", "/Users/luisandro/projects"}, // no false-positive on a prefix match that isn't a path boundary
		{"/Users/luis/projects", "", "/Users/luis/projects"},                      // unknown home: leave untouched
	}
	for _, c := range cases {
		if got := shortenHome(c.path, c.home); got != c.want {
			t.Errorf("shortenHome(%q, %q) = %q, want %q", c.path, c.home, got, c.want)
		}
	}
}

func TestCursorSentinelFollowsArrowKeysBetweenPolls(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", time.Minute), wtEntry("/w/b", "b", 2*time.Minute)})
	if got := m.table.Rows()[0][colCreated]; !strings.Contains(got, loam.Sentinel) {
		t.Fatalf("got %q, want loam.Sentinel on row 0's Created cell right after applyWorktrees", got)
	}

	// Moving down (without a poll in between) must move the tag too, not
	// just bubbles/table's own internal cursor.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := updated.(Model)
	if got := mm.table.Rows()[0][colCreated]; strings.Contains(got, loam.Sentinel) {
		t.Fatalf("got %q, want row 0's Created cell cleared of loam.Sentinel after moving down", got)
	}
	if got := mm.table.Rows()[1][colCreated]; !strings.Contains(got, loam.Sentinel) {
		t.Fatalf("got %q, want row 1 to carry loam.Sentinel after moving down", got)
	}
}

func TestRKeyTriggersAPoll(t *testing.T) {
	m := New(999, false)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("want r to return a poll command")
	}
}

func TestClampCursor(t *testing.T) {
	cases := []struct {
		idx, n, want int
	}{
		{5, 0, 0},
		{-1, 3, 0},
		{5, 3, 2},
		{1, 3, 1},
	}
	for _, c := range cases {
		if got := clampCursor(c.idx, c.n); got != c.want {
			t.Errorf("clampCursor(%d, %d) = %d, want %d", c.idx, c.n, got, c.want)
		}
	}
}

// mergeBorderX returns the on-screen X of the Merge column's own
// right-hand border — the Merge/Path border, the one actually adjacent
// to Path — given cols in the same order/widths New builds them.
func mergeBorderX(cols []table.Column) int {
	off := loam.ColumnOffsets(cols)[colMerge]
	return off.Start + off.Width
}

// TestRenderHeaderOriginYMatchesTheTablesActualHeaderRow is a regression
// test for an off-by-one that makes every mouse-drag test in this file
// pass while resizing is completely broken against a real terminal:
// renderHeader's own tableOriginY must equal the index (0-based, within
// View()'s full output) of the line where the table's own header row
// actually lands — not one more than that, however plausible an extra
// "+1" reads in isolation (View() joins header and the table with a
// single "\n\n", i.e. exactly one blank separator line, not two).
// Handle's own Y check (tea.MouseActionPress) is exact-match, not a
// range, so being off by even one line silently drops every click on
// the table's own header row — a drag can never even start, with no
// error or other symptom besides "nothing happens".
func TestRenderHeaderOriginYMatchesTheTablesActualHeaderRow(t *testing.T) {
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})

	lines := strings.Split(m.View(), "\n")
	want := -1
	for i, line := range lines {
		if strings.Contains(line, "Repo") && strings.Contains(line, "Path") {
			want = i
			break
		}
	}
	if want < 0 {
		t.Fatalf("could not find the table's own header row in View()'s output: %q", m.View())
	}

	_, got := m.renderHeader()
	if got != want {
		t.Fatalf("renderHeader's tableOriginY = %d, want %d (the actual line index of the table's header row in View()'s output)", got, want)
	}
}

// repoBorderX returns the on-screen X of the Repo column's own
// right-hand border, i.e. the Repo/Branch border.
func repoBorderX(cols []table.Column) int {
	off := loam.ColumnOffsets(cols)[colRepo]
	return off.Start + off.Width
}

func TestMouseDragOnlyResizesTheTwoColumnsStraddlingTheDraggedBorder(t *testing.T) {
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := mergeBorderX(cols)
	oldMergeWidth, oldVSCodeWidth := cols[colMerge].Width, cols[colVSCode].Width
	oldRepoWidth, oldCreatedWidth := cols[colRepo].Width, cols[colCreated].Width
	oldPathWidth := cols[colPath].Width

	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 4, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)

	gotCols := m.table.Columns()
	if got, want := gotCols[colMerge].Width, oldMergeWidth+4; got != want {
		t.Fatalf("Merge width = %d, want %d", got, want)
	}
	if got, want := gotCols[colVSCode].Width, oldVSCodeWidth-4; got != want {
		t.Fatalf("VS Code width = %d, want %d (its own left-hand neighbor absorbs the drag)", got, want)
	}
	// Every column not touching the dragged border must stay put.
	if got := gotCols[colRepo].Width; got != oldRepoWidth {
		t.Fatalf("Repo width = %d, want unchanged %d", got, oldRepoWidth)
	}
	if got := gotCols[colCreated].Width; got != oldCreatedWidth {
		t.Fatalf("Created width = %d, want unchanged %d", got, oldCreatedWidth)
	}
	if got := gotCols[colPath].Width; got != oldPathWidth {
		t.Fatalf("Path width = %d, want unchanged %d", got, oldPathWidth)
	}
}

func TestMouseDragBetweenTwoAlreadyMinimalColumnsIsANoOp(t *testing.T) {
	// Repo and Branch are both already at their own content-driven floor
	// (no worktrees means both sit at repoColWidth/branchColWidth), so
	// their shared border has nothing to give in either direction — and,
	// crucially, doesn't silently resize Path instead the way a single
	// global "flex" sink column once would have.
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := repoBorderX(cols)
	oldPathWidth := cols[colPath].Width

	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 4, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)

	gotCols := m.table.Columns()
	if got, want := gotCols[colRepo].Width, cols[colRepo].Width; got != want {
		t.Fatalf("Repo width = %d, want unchanged %d (Branch has nothing to give)", got, want)
	}
	if got, want := gotCols[colPath].Width, oldPathWidth; got != want {
		t.Fatalf("Path width = %d, want unchanged %d (it isn't this border's neighbor)", got, want)
	}
}

func TestMouseDragRepoBranchBorderActuallyMoves(t *testing.T) {
	// Regression test for the frozen-border bug: Repo/Branch's drag
	// minimums were pinned at their content-grown widths, so neither had
	// room to give and their shared border could never move. The minimums
	// are the default floors now, so a drag on the border trades width
	// between the two like any other border.
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()
	long := wtEntry("/w/a", "issue/ISA-18408_dedupe-satellite-replay-echoes", 0)
	m.applyWorktrees([]worktree.Entry{long})

	cols := m.table.Columns()
	if cols[colBranch].Width <= branchColWidth {
		t.Fatalf("Branch width = %d, want it content-grown past %d for this test", cols[colBranch].Width, branchColWidth)
	}
	_, originY := m.renderHeader()
	borderX := repoBorderX(cols)
	oldRepoWidth, oldBranchWidth := cols[colRepo].Width, cols[colBranch].Width

	// Drag the Repo/Branch border right: Repo widens, Branch narrows.
	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 4, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)

	gotCols := m.table.Columns()
	if got, want := gotCols[colRepo].Width, oldRepoWidth+4; got != want {
		t.Fatalf("Repo width = %d, want %d (it absorbed the drag)", got, want)
	}
	if got, want := gotCols[colBranch].Width, oldBranchWidth-4; got != want {
		t.Fatalf("Branch width = %d, want %d (the drag narrowed it)", got, want)
	}
}

func TestMouseDragNarrowerThanContentSurvivesTheNextPoll(t *testing.T) {
	// A drag that narrows a content-grown column below its content width
	// is a deliberate pin: the next poll's column rebuild must keep it
	// (truncating the label with an ellipsis) rather than snapping back
	// to the content width.
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()
	long := wtEntry("/w/a", "issue/ISA-18408_dedupe-satellite-replay-echoes", 0)
	m.applyWorktrees([]worktree.Entry{long})

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := repoBorderX(cols)

	// Drag right: Branch narrows below its content width, Repo widens.
	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 4, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	resized := m.table.Columns()[colBranch].Width
	if resized >= branchColumnWidth(m.displayedWorktrees()) {
		t.Fatalf("Branch width = %d after the drag, want it narrowed below the content width %d", resized, branchColumnWidth(m.displayedWorktrees()))
	}

	m.applyWorktrees([]worktree.Entry{long})
	if got := m.table.Columns()[colBranch].Width; got != resized {
		t.Fatalf("Branch width = %d after a poll, want %d (the user's pin, undiscarded)", got, resized)
	}
}

// worktreeBorderX returns the on-screen X of the Worktree column's own
// right-hand border, i.e. the Worktree/Merge border.
func worktreeBorderX(cols []table.Column) int {
	off := loam.ColumnOffsets(cols)[colWorktree]
	return off.Start + off.Width
}

func TestMouseDragWorktreeBorderMovesDownToItsContentFloor(t *testing.T) {
	// Regression test for the frozen-Worktree bug: Created/Worktree/Merge
	// used to floor their drag minimums at their own default widths, so
	// all three sat exactly at their minimums and neither of Worktree's
	// borders had any room to move in either direction. The minimums are
	// the content floors now (values always fit; only the title may
	// truncate), so the Worktree/Merge border trades width like any
	// other.
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := worktreeBorderX(cols)
	oldWorktreeWidth, oldMergeWidth := cols[colWorktree].Width, cols[colMerge].Width

	// Drag the Worktree/Merge border left, past the point where Worktree
	// hits its content floor: Worktree narrows only that far, and Merge
	// grows by exactly what Worktree actually gave up.
	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX - 10, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)

	gotCols := m.table.Columns()
	wantWorktree := worktreeContentWidth // clamped at the content floor
	if got := gotCols[colWorktree].Width; got != wantWorktree {
		t.Fatalf("Worktree width = %d, want %d (clamped at its content floor)", got, wantWorktree)
	}
	if got, want := gotCols[colMerge].Width, oldMergeWidth+(oldWorktreeWidth-wantWorktree); got != want {
		t.Fatalf("Merge width = %d, want %d (it absorbed exactly what Worktree gave up)", got, want)
	}

	// And the pin survives the next poll's column rebuild.
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	if got := m.table.Columns()[colWorktree].Width; got != wantWorktree {
		t.Fatalf("Worktree width = %d after a poll, want %d (the user's pin, undiscarded)", got, wantWorktree)
	}
}

func TestMouseDragRecordsOnlyTheTwoDraggedColumns(t *testing.T) {
	// colOverrides must only ever pin the two columns a drag actually
	// moved: recording every column's width would freeze Repo/Branch's
	// grow-to-fit sizing from the first drag on.
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := mergeBorderX(cols)

	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 4, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)

	if m.colOverrides == nil {
		t.Fatal("want colOverrides recorded after a drag")
	}
	for i := range m.colOverrides {
		if i != colMerge && i != colVSCode {
			t.Fatalf("colOverrides pins column %d, want only the dragged pair (%d, %d)", i, colMerge, colVSCode)
		}
	}

	// An unpinned Branch must still grow to fit a freshly polled longer
	// branch name, drag or no drag.
	long := wtEntry("/w/a", "issue/ISA-18408_dedupe-satellite-replay-echoes", 0)
	m.applyWorktrees([]worktree.Entry{long})
	if got, want := m.table.Columns()[colBranch].Width, branchColumnWidth(m.displayedWorktrees()); got != want {
		t.Fatalf("Branch width = %d after a poll, want %d (still growing to fit, unpinned)", got, want)
	}
}

func TestMouseDragSurvivesTheNextPoll(t *testing.T) {
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := mergeBorderX(cols)

	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 4, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	resized := m.table.Columns()[colMerge].Width

	// A fresh poll rebuilds columns from scratch (worktreeColumns);
	// without colOverrides being carried through, Merge would silently
	// revert to its own fixed default.
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	if got := m.table.Columns()[colMerge].Width; got != resized {
		t.Fatalf("Merge width = %d after a poll, want %d (the drag override, undiscarded)", got, resized)
	}
}

func TestWindowSizeMsgClearsColumnOverrides(t *testing.T) {
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := mergeBorderX(cols)

	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 4, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	if m.colOverrides[colMerge] == 0 {
		t.Fatal("want a Merge override recorded after the drag")
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	m = updated.(Model)
	if m.colOverrides != nil {
		t.Fatalf("colOverrides = %v after a terminal resize, want nil", m.colOverrides)
	}
}

func TestMouseClickOffTheHeaderRowDoesNotStartADrag(t *testing.T) {
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()

	cols := m.table.Columns()
	borderX := mergeBorderX(cols)
	oldMergeWidth := cols[colMerge].Width

	// Well below the header row, e.g. a click on a data row.
	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: 50, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 4, Y: 50, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)

	if got := m.table.Columns()[colMerge].Width; got != oldMergeWidth {
		t.Fatalf("Merge width = %d, want unchanged %d (click was off the header row)", got, oldMergeWidth)
	}
}

func TestViewMarksColumnBordersOnTheHeaderRowSoThereIsSomethingToDrag(t *testing.T) {
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})

	lines := strings.Split(m.View(), "\n")
	var headerLine string
	for _, line := range lines {
		if strings.Contains(line, "Repo") {
			headerLine = line
			break
		}
	}
	if headerLine == "" {
		t.Fatalf("View() = %q, want a header line containing %q", m.View(), "Repo")
	}
	// 7 columns, so 6 internal borders.
	if n := strings.Count(headerLine, loam.BorderGlyph); n != 6 {
		t.Fatalf("header line has %d border glyphs, want 6 (one per internal column border): %q", n, headerLine)
	}
}
