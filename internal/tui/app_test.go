package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luiul/loam"
	"github.com/luiul/mycelium"
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
	if got := m.table.Rows()[0][colUpdated]; !strings.Contains(got, loam.Sentinel) {
		t.Fatalf("got %q, want loam.Sentinel on row 0's Updated cell right after applyWorktrees", got)
	}

	// Moving down (without a poll in between) must move the tag too, not
	// just bubbles/table's own internal cursor.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := updated.(Model)
	if got := mm.table.Rows()[0][colUpdated]; strings.Contains(got, loam.Sentinel) {
		t.Fatalf("got %q, want row 0's Updated cell cleared of loam.Sentinel after moving down", got)
	}
	if got := mm.table.Rows()[1][colUpdated]; !strings.Contains(got, loam.Sentinel) {
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

// repoBorderX returns the on-screen X of the Repo column's own right-hand
// border, given cols in the same order/widths worktreeColumns builds them.
func repoBorderX(cols []table.Column) int {
	off := loam.ColumnOffsets(cols)[colRepo]
	return off.Start + off.Width
}

func TestMouseDragWidensRepoColumnAndShrinksPathToMatch(t *testing.T) {
	m := New(999, false)
	m.width = 200
	m.table.SetWidth(200)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", time.Minute)})

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := repoBorderX(cols)
	oldRepoWidth, oldPathWidth := cols[colRepo].Width, cols[colPath].Width

	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 5, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)

	gotCols := m.table.Columns()
	if got, want := gotCols[colRepo].Width, oldRepoWidth+5; got != want {
		t.Fatalf("Repo width = %d, want %d", got, want)
	}
	if got, want := gotCols[colPath].Width, oldPathWidth-5; got != want {
		t.Fatalf("Path width = %d, want %d (flex column absorbs the drag)", got, want)
	}
}

func TestMouseDragSurvivesTheNextPoll(t *testing.T) {
	m := New(999, false)
	m.width = 200
	m.table.SetWidth(200)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", time.Minute)})

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := repoBorderX(cols)

	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 5, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	resized := m.table.Columns()[colRepo].Width

	// A fresh poll re-derives Repo's width from content (repoColumnWidth)
	// on every call; the user's own drag must still win.
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", time.Minute)})
	if got := m.table.Columns()[colRepo].Width; got != resized {
		t.Fatalf("Repo width = %d after a poll, want %d (the drag override, undiscarded)", got, resized)
	}
}

func TestWindowResizeClearsColumnOverrides(t *testing.T) {
	m := New(999, false)
	m.width = 200
	m.table.SetWidth(200)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", time.Minute)})

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := repoBorderX(cols)

	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 5, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	if m.colOverrides[colRepo] == 0 {
		t.Fatal("want a Repo override recorded after the drag")
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	m = updated.(Model)
	if m.colOverrides != nil {
		t.Fatalf("colOverrides = %v after a terminal resize, want nil", m.colOverrides)
	}
}

func TestMouseClickOffTheHeaderRowDoesNotStartADrag(t *testing.T) {
	m := New(999, false)
	m.width = 200
	m.table.SetWidth(200)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", time.Minute)})

	cols := m.table.Columns()
	borderX := repoBorderX(cols)
	oldRepoWidth := cols[colRepo].Width

	// Well below the header row, e.g. a click on a data row.
	updated, _ := m.Update(tea.MouseMsg{X: borderX, Y: 50, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 5, Y: 50, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)

	if got := m.table.Columns()[colRepo].Width; got != oldRepoWidth {
		t.Fatalf("Repo width = %d, want unchanged %d (click was off the header row)", got, oldRepoWidth)
	}
}
