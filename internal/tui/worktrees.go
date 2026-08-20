package tui

import (
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/table"

	"github.com/luiul/understory/internal/worktree"
)

// worktreeColWidth constants for the view's fixed columns; Path gets
// whatever's left, floored at minPathWidth.
const (
	repoColWidth    = 16
	branchColWidth  = 20
	updatedColWidth = 8
	statusColWidth  = 8
	minPathWidth    = 20
)

// worktreeColumns builds the view's columns for the given terminal width:
// Repo/Branch/Updated/Status are fixed width, Path fills the remainder.
func worktreeColumns(width int) []table.Column {
	cols := []table.Column{
		{Title: "", Width: 1}, // cursorMarker
		{Title: "Repo", Width: repoColWidth},
		{Title: "Branch", Width: branchColWidth},
		{Title: "Updated", Width: updatedColWidth},
		{Title: "Status", Width: statusColWidth},
	}

	fixedWidth := 0
	for _, c := range cols {
		fixedWidth += c.Width
	}
	totalCells := len(cols) + 1 // + Path
	remaining := width - fixedWidth - 2*totalCells
	if remaining < minPathWidth {
		remaining = minPathWidth
	}
	return append(cols, table.Column{Title: "Path", Width: remaining})
}

// sortWorktrees orders worktrees by most recently committed first. Does
// not mutate worktrees.
func sortWorktrees(worktrees []worktree.Entry) []worktree.Entry {
	sorted := make([]worktree.Entry, len(worktrees))
	copy(sorted, worktrees)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].CommitTime.After(sorted[j].CommitTime)
	})
	return sorted
}

// displayedWorktrees is the view's current row set: every known
// worktree, most-recently-committed first.
func (m Model) displayedWorktrees() []worktree.Entry {
	return sortWorktrees(m.worktrees)
}

// resolveWorktreeCursor finds path's index in displayed, or falls back to
// fallback (clamped): "prefer the same key, else keep roughly where you
// were" resolution for a fresh poll that may have reordered rows.
func resolveWorktreeCursor(displayed []worktree.Entry, path string, fallback int) int {
	cursor := fallback
	if path != "" {
		for i, w := range displayed {
			if w.Path == path {
				cursor = i
				break
			}
		}
	}
	return clampCursor(cursor, len(displayed))
}

// applyWorktrees stores a fresh worktree poll, keeps whichever worktree
// (by path) was previously selected selected, and rebuilds the table's
// rows.
func (m *Model) applyWorktrees(fresh []worktree.Entry) {
	oldCursor := clampCursor(m.table.Cursor(), len(m.displayedWorktrees()))
	oldDisplayed := m.displayedWorktrees() // still against the OLD m.worktrees
	var previousPath string
	if oldCursor >= 0 && oldCursor < len(oldDisplayed) {
		previousPath = oldDisplayed[oldCursor].Path
	}

	m.worktrees = fresh

	newDisplayed := m.displayedWorktrees()
	m.cursor = resolveWorktreeCursor(newDisplayed, previousPath, oldCursor)

	m.table.SetRows(buildWorktreeRows(newDisplayed, m.cursor, m.home, time.Now()))
	m.table.SetCursor(m.cursor)
}

// selectedWorktree returns the worktree.Entry backing the currently
// highlighted row (table.Cursor(), the live ground truth), or ok=false if
// there are none showing (e.g. only the placeholder row).
func (m Model) selectedWorktree() (worktree.Entry, bool) {
	displayed := m.displayedWorktrees()
	if len(displayed) == 0 {
		return worktree.Entry{}, false
	}
	idx := m.table.Cursor()
	if idx < 0 || idx >= len(displayed) {
		return worktree.Entry{}, false
	}
	return displayed[idx], true
}

// buildWorktreeRows constructs the view's rows from an already-sorted
// worktree list. cursor picks which row's leading cell carries
// cursorMarker.
func buildWorktreeRows(worktrees []worktree.Entry, cursor int, home string, now time.Time) []table.Row {
	if len(worktrees) == 0 {
		return []table.Row{{"", "", "", "", "", noWorktreesMessage()}}
	}

	rows := make([]table.Row, len(worktrees))
	for i, w := range worktrees {
		marker := ""
		if i == cursor {
			marker = cursorMarker
		}
		rows[i] = table.Row{
			marker,
			repoLabel(w),
			w.Branch,
			humanizeSince(now.Sub(w.CommitTime)),
			w.Symbols,
			shortenHome(w.Path, home),
		}
	}
	return rows
}

func repoLabel(w worktree.Entry) string {
	if w.Owner != "" {
		return w.Owner + "/" + w.Repo
	}
	return w.Repo
}

// noWorktreesMessage distinguishes "wt (worktrunk) isn't installed" (a
// setup gap, worth naming) from "no worktrees found yet" (e.g. the poll
// just hasn't landed).
func noWorktreesMessage() string {
	if !worktree.Available() {
		return "wt (worktrunk) is not installed; see https://worktrunk.dev"
	}
	return "no known worktrees (registered in ~/.cache/wt/known-repos, or the repo you're standing in)"
}

// worktreeSummaryLine is a one-line "N worktrees" breakdown. Empty when
// there are no worktrees, since the placeholder row already says so.
func worktreeSummaryLine(worktrees []worktree.Entry) string {
	if len(worktrees) == 0 {
		return ""
	}
	label := "worktrees"
	if len(worktrees) == 1 {
		label = "worktree"
	}
	return subtleStyle.Render(fmt.Sprintf("%d %s", len(worktrees), label))
}
