package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luiul/understory/internal/worktree"
)

// typeRunes feeds each rune of s as its own keypress, the way a terminal
// delivers typed text.
func typeRunes(m Model, s string) Model {
	for _, r := range s {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	return m
}

func TestSlashEntersFilterModeAndTypingFiltersRows(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{
		wtEntry("/w/widgets-a", "a", time.Minute),
		otherRepoEntry("/w/gizmos-b", "b", 2*time.Minute),
	})

	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	if !m.filtering {
		t.Fatal("want filtering=true after /")
	}
	if got := m.footerView(); !strings.Contains(got, "filter> ") {
		t.Fatalf("footer = %q, want the filter input prompt", got)
	}

	m = typeRunes(m, "gizmos")
	rows := m.table.Rows()
	if len(rows) != 1 || rows[0][colBranch] != "b" {
		t.Fatalf("rows = %v, want only the gizmos row left", rows)
	}
}

func TestFilterModeSwallowsActionKeys(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	updated, _ := m.Update(key("/"))
	m = updated.(Model)

	// "x" would arm a removal, "q" would quit, were they bindings;
	// mid-filter they are query text.
	m = typeRunes(m, "xq")
	if m.confirm.Active() {
		t.Fatal("want x swallowed into the query, not arming a confirmation")
	}
	if m.quitting {
		t.Fatal("want q swallowed into the query, not quitting")
	}
	if m.filterQuery != "xq" {
		t.Fatalf("query = %q, want %q", m.filterQuery, "xq")
	}
}

func TestEscLeavesFilterModeKeepingTheQueryApplied(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{
		wtEntry("/w/widgets-a", "a", time.Minute),
		otherRepoEntry("/w/gizmos-b", "b", 2*time.Minute),
	})
	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	m = typeRunes(m, "gizmos")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.filtering {
		t.Fatal("want filtering=false after esc")
	}
	if len(m.table.Rows()) != 1 {
		t.Fatalf("rows = %v, want the gizmos filter still applied", m.table.Rows())
	}
	if got := m.footerView(); !strings.Contains(got, "filter: gizmos") {
		t.Fatalf("footer = %q, want the applied-filter readout", got)
	}
}

func TestEscInNormalModeClearsAnAppliedFilter(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{
		wtEntry("/w/widgets-a", "a", time.Minute),
		otherRepoEntry("/w/gizmos-b", "b", 2*time.Minute),
	})
	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	m = typeRunes(m, "gizmos")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.filterQuery != "" {
		t.Fatalf("query = %q, want cleared", m.filterQuery)
	}
	if len(m.table.Rows()) != 2 {
		t.Fatalf("rows = %v, want both rows back", m.table.Rows())
	}
}

func TestEnterMidFilterStillOpens(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{
		wtEntry("/w/widgets-a", "a", time.Minute),
		otherRepoEntry("/w/gizmos-b", "b", 2*time.Minute),
	})
	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	m = typeRunes(m, "gizmos")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if cmd == nil {
		t.Fatal("want enter to keep its row action (an open command) mid-filter")
	}
	if m.filtering {
		t.Fatal("want the filter input left once the open is underway")
	}
	if m.filterQuery != "gizmos" || len(m.table.Rows()) != 1 {
		t.Fatalf("query = %q, rows = %v, want the filter still applied", m.filterQuery, m.table.Rows())
	}
}

func TestArrowsStillMoveTheCursorMidFilter(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{
		wtEntry("/w/widgets-a", "a", time.Minute),
		otherRepoEntry("/w/gizmos-b", "b", 2*time.Minute),
	})
	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	m = typeRunes(m, "w") // both paths contain "w"

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)

	if got, ok := m.selectedWorktree(); !ok || got.Path != "/w/gizmos-b" {
		t.Fatalf("selected = %+v, want the down arrow to have moved to /w/gizmos-b mid-filter", got)
	}
}

func TestPollKeepsTheFilterApplied(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{
		wtEntry("/w/widgets-a", "a", time.Minute),
		otherRepoEntry("/w/gizmos-b", "b", 2*time.Minute),
	})
	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	m = typeRunes(m, "gizmos")

	m.applyWorktrees([]worktree.Entry{
		wtEntry("/w/widgets-a", "a", time.Minute),
		otherRepoEntry("/w/gizmos-b", "b", 2*time.Minute),
	})

	if len(m.worktrees) != 2 {
		t.Fatalf("worktrees = %v, want the full set kept underneath the filter", m.worktrees)
	}
	rows := m.table.Rows()
	if len(rows) != 1 || rows[0][colBranch] != "b" {
		t.Fatalf("rows = %v, want the filter re-applied across the poll", rows)
	}
}

func TestFilterCursorFollowsTheSameWorktreeWhileTyping(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{
		wtEntry("/w/widgets-a", "a", time.Minute),
		wtEntry("/w/widgets-b", "b", 2*time.Minute),
		otherRepoEntry("/w/gizmos-c", "c", 3*time.Minute),
	})

	// Select /w/widgets-b, then filter the gizmos row away: widgets-b is
	// still displayed, so the cursor must follow it rather than staying
	// on a row index that now means something else.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if got := m.selectedPath(); got != "/w/widgets-b" {
		t.Fatalf("selected = %q before filtering, want /w/widgets-b", got)
	}

	updated, _ = m.Update(key("/"))
	m = updated.(Model)
	m = typeRunes(m, "widgets")

	if got := m.selectedPath(); got != "/w/widgets-b" {
		t.Fatalf("selected = %q after filtering, want the cursor to have followed /w/widgets-b", got)
	}
}

func TestFilterPlaceholderNamesTheQueryWhenNothingMatches(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	m = typeRunes(m, "zzz")

	rows := m.table.Rows()
	if len(rows) != 1 || !strings.Contains(rows[0][colPath], `no worktrees match filter "zzz"`) {
		t.Fatalf("rows = %v, want the no-match placeholder naming the query", rows)
	}
	if _, ok := m.selectedWorktree(); ok {
		t.Fatal("want no selectable worktree while only the placeholder is showing")
	}
}

func TestCtrlUClearsTheQueryMidFilter(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{
		wtEntry("/w/widgets-a", "a", time.Minute),
		otherRepoEntry("/w/gizmos-b", "b", 2*time.Minute),
	})
	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	m = typeRunes(m, "gizmos")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = updated.(Model)

	if !m.filtering {
		t.Fatal("want the input still focused after ctrl+u")
	}
	if m.filterQuery != "" || len(m.table.Rows()) != 2 {
		t.Fatalf("query = %q, rows = %v, want the filter cleared in place", m.filterQuery, m.table.Rows())
	}
}

func TestColumnWidthsStayPutWhileTyping(t *testing.T) {
	// The widest branch lives on the row the filter is about to hide:
	// sizing the columns off the text-filtered set would narrow Branch
	// on that keystroke. They must not move (see visibleWorktrees' doc).
	m := New(999, false)
	m.width = 120
	m.applyWorktrees([]worktree.Entry{
		wtEntry("/w/widgets-short", "s", time.Minute),
		wtEntry("/w/widgets-long", "a-very-long-branch-name-here", 2*time.Minute),
	})
	before := m.table.Columns()

	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	m = typeRunes(m, "short") // only the short-branch row still matches

	after := m.table.Columns()
	if len(before) != len(after) {
		t.Fatalf("columns = %v, want %v while typing a filter", after, before)
	}
	for i := range before {
		if before[i].Width != after[i].Width {
			t.Fatalf("column %d width = %d, want %d while typing a filter (columns: %v -> %v)", i, after[i].Width, before[i].Width, before, after)
		}
	}
}
