package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	if got := m.table.Rows()[0][colUpdated]; !strings.Contains(got, cursorSentinel) {
		t.Fatalf("got %q, want cursorSentinel on row 0's Updated cell right after applyWorktrees", got)
	}

	// Moving down (without a poll in between) must move the tag too, not
	// just bubbles/table's own internal cursor.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := updated.(Model)
	if got := mm.table.Rows()[0][colUpdated]; strings.Contains(got, cursorSentinel) {
		t.Fatalf("got %q, want row 0's Updated cell cleared of cursorSentinel after moving down", got)
	}
	if got := mm.table.Rows()[1][colUpdated]; !strings.Contains(got, cursorSentinel) {
		t.Fatalf("got %q, want row 1 to carry cursorSentinel after moving down", got)
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
