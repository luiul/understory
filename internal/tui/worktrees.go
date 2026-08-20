package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"

	"github.com/luiul/understory/internal/worktree"
)

// worktreeColWidth constants for the view's fixed columns; Path gets
// whatever's left, floored at minPathWidth.
const (
	repoColWidth     = 16
	branchColWidth   = 20
	ageColWidth      = 8
	worktreeColWidth = 8
	mergeColWidth    = 9
	minPathWidth     = 20
)

// Column indexes into both worktreeColumns' return value and each
// buildWorktreeRows row, in display order. colorizeRows (see colorize.go)
// uses colWorktree/colMerge to recolor those two columns post-render.
const (
	colCursor = iota
	colRepo
	colBranch
	colAge
	colWorktree
	colMerge
	colPath
)

// worktreeColumns builds the view's columns for the given terminal width:
// Repo/Branch/Age/Worktree/Merge are fixed width, Path fills the
// remainder.
func worktreeColumns(width int) []table.Column {
	cols := []table.Column{
		{Title: "", Width: 1}, // cursorMarker
		{Title: "Repo", Width: repoColWidth},
		{Title: "Branch", Width: branchColWidth},
		{Title: "Age", Width: ageColWidth},
		{Title: "Worktree", Width: worktreeColWidth},
		{Title: "Merge", Width: mergeColWidth},
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

// sortWorktrees orders worktrees for display: grouped so every worktree
// of the same repo (by repoLabel) sits together in one contiguous block,
// rather than interleaved purely by recency, which scattered a repo's
// branches throughout the list and made repos sharing the same fallback
// label (see worktree.Entry doc on Owner/Repo) look like repeated,
// ungrouped duplicates. Blocks are themselves ordered by their own most
// recently committed worktree, and rows within a block are ordered by
// recency too, so "most recently active first" still holds at both
// levels. Does not mutate worktrees.
func sortWorktrees(worktrees []worktree.Entry) []worktree.Entry {
	var order []string
	groups := map[string][]worktree.Entry{}
	for _, w := range worktrees {
		key := repoLabel(w)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], w)
	}

	for _, key := range order {
		g := groups[key]
		sort.SliceStable(g, func(i, j int) bool {
			return g[i].CommitTime.After(g[j].CommitTime)
		})
	}

	// Each group is already sorted most-recent-first, so its own first
	// entry's CommitTime is the group's most recent commit.
	sort.SliceStable(order, func(i, j int) bool {
		return groups[order[i]][0].CommitTime.After(groups[order[j]][0].CommitTime)
	})

	sorted := make([]worktree.Entry, 0, len(worktrees))
	for _, key := range order {
		sorted = append(sorted, groups[key]...)
	}
	return sorted
}

// displayedWorktrees is the view's current row set: every known
// worktree, grouped by repo and most-recently-committed first (see
// sortWorktrees), minus each repo's main worktree (see New's doc on
// showMain) unless m.showMain opted back in.
func (m Model) displayedWorktrees() []worktree.Entry {
	worktrees := m.worktrees
	if !m.showMain {
		worktrees = filterOutMain(worktrees)
	}
	return sortWorktrees(worktrees)
}

// filterOutMain returns worktrees minus every entry with IsMain set,
// preserving order. Does not mutate worktrees.
func filterOutMain(worktrees []worktree.Entry) []worktree.Entry {
	filtered := make([]worktree.Entry, 0, len(worktrees))
	for _, w := range worktrees {
		if !w.IsMain {
			filtered = append(filtered, w)
		}
	}
	return filtered
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

	m.table.SetRows(buildWorktreeRows(newDisplayed, m.cursor, m.home, time.Now(), m.hiddenMain(newDisplayed)))
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

// hiddenMain reports whether displayed is empty only because every known
// worktree is a filtered-out main one (see displayedWorktrees), as
// opposed to there genuinely being none at all: buildWorktreeRows uses
// this to point at --show-main instead of the generic empty-registry
// message.
func (m Model) hiddenMain(displayed []worktree.Entry) bool {
	return !m.showMain && len(m.worktrees) > 0 && len(displayed) == 0
}

// buildWorktreeRows constructs the view's rows from an already-sorted
// (see sortWorktrees) worktree list. cursor picks which row's leading
// cell carries cursorMarker. hiddenMain (see Model.hiddenMain) selects
// which placeholder message to show when worktrees is empty.
//
// The Repo cell is only printed on the first row of each repo's block:
// sortWorktrees already guarantees every worktree of the same repo is
// contiguous, so repeating the same label down every one of its rows
// added nothing but visual noise (and, worse, made same-labeled but
// unrelated repos look identical); blanking the repeat turns "first row
// of a block has a label, the rest don't" into the separator between one
// repo's block and the next.
//
// Worktree/Merge replace the single opaque Status glyph column (wt's own
// compact "^|", "c▶", "!⚑" symbols, still available on Entry.Symbols for
// anyone piping raw output, but not self-explanatory in this view) with
// the same two plain-word signals coppice's own worktree table shows:
// whether the working tree itself is dirty/clean/stale, and separately
// whether the branch has been merged into main yet.
func buildWorktreeRows(worktrees []worktree.Entry, cursor int, home string, now time.Time, hiddenMain bool) []table.Row {
	if len(worktrees) == 0 {
		return []table.Row{{"", "", "", "", "", "", noWorktreesMessage(hiddenMain)}}
	}

	rows := make([]table.Row, len(worktrees))
	for i, w := range worktrees {
		marker := ""
		if i == cursor {
			marker = cursorMarker
		}
		label := repoLabel(w)
		if i > 0 && label == repoLabel(worktrees[i-1]) {
			label = ""
		}
		rows[i] = table.Row{
			marker,
			label,
			w.Branch,
			humanizeSince(now.Sub(w.CommitTime)),
			worktreeStatusLabel(w),
			mergeStatusLabel(w),
			shortenHome(w.Path, home),
		}
	}
	return rows
}

// worktreeStatusLabel is the Worktree column's plain-word rendering of
// Entry's working-tree health: "stale" (see Entry.Stale's doc) takes
// priority over dirty/clean, since a prunable worktree's uncommitted-
// changes state is meaningless once its directory is already gone.
func worktreeStatusLabel(w worktree.Entry) string {
	if w.Stale {
		return "stale"
	}
	if w.Dirty {
		return "dirty"
	}
	return "clean"
}

// mergeStatusLabel is the Merge column's rendering of Entry.MergeStatus:
// "-" for its zero value (not applicable to the main worktree, or moot
// for a stale one), the computed label otherwise.
func mergeStatusLabel(w worktree.Entry) string {
	if w.MergeStatus == "" {
		return "-"
	}
	return w.MergeStatus
}

func repoLabel(w worktree.Entry) string {
	if w.Owner != "" {
		return w.Owner + "/" + w.Repo
	}
	return w.Repo
}

// noWorktreesMessage distinguishes "wt (worktrunk) isn't installed" (a
// setup gap, worth naming), "no worktrees found yet" (e.g. the poll just
// hasn't landed), and "every known worktree is a hidden main one" (the
// registry isn't empty, --show-main would reveal them) from each other.
func noWorktreesMessage(hiddenMain bool) string {
	if !worktree.Available() {
		return "wt (worktrunk) is not installed; see https://worktrunk.dev"
	}
	if hiddenMain {
		return "no worktrees to show (pass --show-main to include each repo's main worktree)"
	}
	return "no known worktrees (registered in ~/.cache/wt/known-repos, or the repo you're standing in)"
}

// worktreeSummaryLine returns a one-line "N worktrees: N dirty · N stale ·
// N merged · N clean" breakdown, one mutually-exclusive bucket per
// worktree (most-actionable-first classification, same spirit as
// canopy's own summaryLine over its State column, folded down to a
// single dimension since "needs a look" is really one axis here even
// though it's backed by two Entry fields): Stale wins first (see
// Entry.Stale's doc: Dirty/MergeStatus are meaningless once true), then
// Dirty (uncommitted work, worth a look now), then a merged branch
// (nothing left to do but it's a removal candidate), and everything else
// (still-open, not-yet-merged work with nothing outstanding) falls into
// clean. Colored to match the Worktree/Merge table columns via the same
// style lookups. Returns "" if there are no worktrees, since the
// placeholder row already says so.
func worktreeSummaryLine(entries []worktree.Entry) string {
	if len(entries) == 0 {
		return ""
	}

	counts := map[string]int{}
	for _, e := range entries {
		switch {
		case e.Stale:
			counts["stale"]++
		case e.Dirty:
			counts["dirty"]++
		case e.MergeStatus == worktree.MergeStatusMerged:
			counts["merged"]++
		default:
			counts["clean"]++
		}
	}

	var parts []string
	for _, bucket := range []string{"dirty", "stale", "merged", "clean"} {
		n := counts[bucket]
		if n == 0 {
			continue
		}
		style := worktreeStatusStyle(bucket)
		if bucket == "merged" {
			style = mergeStatusStyle(bucket)
		}
		parts = append(parts, style.Render(fmt.Sprintf("%d %s", n, bucket)))
	}

	label := "worktrees"
	if len(entries) == 1 {
		label = "worktree"
	}
	return subtleStyle.Render(fmt.Sprintf("%d %s: ", len(entries), label)) +
		strings.Join(parts, subtleStyle.Render(" · "))
}
