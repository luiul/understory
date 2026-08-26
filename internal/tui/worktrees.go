package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/mattn/go-runewidth"

	"github.com/luiul/dashkit/loam"
	"github.com/luiul/understory/internal/worktree"
)

// worktreeColWidth constants for the view's columns. repoColWidth and
// branchColWidth are only floors (see repoColumnWidth/branchColumnWidth):
// the Repo and Branch columns each grow to fit whichever displayed
// label/branch name is longest, so a long owner/repo name or branch name
// is never truncated the way a fixed width would. Created/Worktree/
// Merge stay fixed; Path gets whatever's left after all of those,
// floored at minPathWidth.
const (
	repoColWidth     = 16
	branchColWidth   = 20
	createdColWidth  = 8
	worktreeColWidth = 8
	mergeColWidth    = 9
	minPathWidth     = 20
)

// Column indexes into both worktreeColumns' return value and each
// buildWorktreeRows row, in display order. colorizeRows (see colorize.go)
// uses colWorktree/colMerge to recolor those two columns post-render.
// There's no dedicated cursor column: see loam.Sentinel's doc (loam pkg)
// for how the selected row is identified instead now that the whole row
// is highlighted (colorize.go) rather than a leading marker glyph.
const (
	colRepo = iota
	colBranch
	colCreated
	colWorktree
	colMerge
	colPath
)

// repoColumnWidth is the Repo column's width for the given worktree set:
// repoColWidth's floor, or the longest displayed repo label's own display
// width if that's wider. Every entry is measured, not just the ones whose
// label actually renders (buildWorktreeRows blanks a repeated label within
// a group), since the un-blanked first row of that same group still needs
// room for it. runewidth.StringWidth (not len) so a multi-byte owner/repo
// name is measured the same way bubbles/table itself lays out cells.
func repoColumnWidth(worktrees []worktree.Entry) int {
	width := repoColWidth
	for _, w := range worktrees {
		if lw := runewidth.StringWidth(repoLabel(w)); lw > width {
			width = lw
		}
	}
	return width
}

// branchColumnWidth is the Branch column's width for the given worktree
// set: branchColWidth's floor, or the longest branch name's own display
// width if that's wider (same reasoning as repoColumnWidth: a fixed
// width truncated long branch names instead of showing them in full).
func branchColumnWidth(worktrees []worktree.Entry) int {
	width := branchColWidth
	for _, w := range worktrees {
		if lw := runewidth.StringWidth(w.Branch); lw > width {
			width = lw
		}
	}
	return width
}

// worktreeColumns builds the view's columns for the given terminal width
// and worktree set: Repo and Branch each grow to fit their widest
// displayed value (see repoColumnWidth/branchColumnWidth),
// Created/Worktree/Merge are fixed width, and Path fills whatever's
// left.
//
// overrides carries forward whichever columns a mouse drag (see
// github.com/luiul/dashkit/trellis and Model.colOverrides' own doc) has
// actually widened past their own natural floor — keyed by the Column
// indexes above, same map Model.colOverrides holds. An override only
// applies when it's still wider than that column's own freshly computed
// floor: Repo/Branch's floor moves with the data (a longer owner/repo
// label or branch name than the last poll raises it), so a stale
// override recorded against a shorter one is dropped rather than
// truncating the new, longer value; Created/Worktree/Merge's floor never
// moves, so for them this is simply "only apply an override that grew
// the column, never one that would shrink it below its own default".
// Path never takes an override at all: it always absorbs whatever's
// left after every other column's own effective (possibly overridden)
// width is accounted for, the same invariant a mouse drag itself already
// keeps (see trellis.Model.Handle's doc) — the row's total width never
// changes no matter which column a user actually resized.
func worktreeColumns(width int, worktrees []worktree.Entry, overrides map[int]int) []table.Column {
	cols := []table.Column{
		{Title: "Repo", Width: repoColumnWidth(worktrees)},
		{Title: "Branch", Width: branchColumnWidth(worktrees)},
		{Title: "Created", Width: createdColWidth},
		{Title: "Worktree", Width: worktreeColWidth},
		{Title: "Merge", Width: mergeColWidth},
	}

	fixedWidth := 0
	for i := range cols {
		if w, ok := overrides[i]; ok && w > cols[i].Width {
			cols[i].Width = w
		}
		fixedWidth += cols[i].Width
	}
	totalCells := len(cols) + 1 // + Path
	remaining := width - fixedWidth - 2*totalCells
	if remaining < minPathWidth {
		remaining = minPathWidth
	}
	return append(cols, table.Column{Title: "Path", Width: remaining})
}

// columnMinWidths returns each column's own minimum width, in the same
// order/index worktreeColumns builds them, for trellis' mouse-resize
// handling (see Update's tea.MouseMsg case). Repo and Branch floor at
// their own currently displayed width (see repoColumnWidth/
// branchColumnWidth): both already grow to fit whichever label/branch
// name is currently longest, so there's no room to shrink either without
// truncating it. Created/Worktree/Merge floor at their own fixed default
// for the same reason — every value they ever show already fits exactly
// there. Only Path, which absorbs whatever's left of the terminal width,
// has real room to give up; its floor is minPathWidth, the same one
// worktreeColumns' own leftover-space computation already respects.
func columnMinWidths(worktrees []worktree.Entry) []int {
	return []int{
		repoColumnWidth(worktrees),
		branchColumnWidth(worktrees),
		createdColWidth,
		worktreeColWidth,
		mergeColWidth,
		minPathWidth,
	}
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
// sortWorktrees), with each repo's main worktree (Entry.IsMain, the base
// branch checkout `wt`/coppice created the others alongside) dropped
// unless m.showMain is set. Hiding it by default keeps the view focused
// on the worktrees actually being worked in: main rarely has anything to
// do (see worktreeSummaryLine/buildWorktreeRows' "-" Merge cell for it),
// and having it always take the first row of every repo's block was
// mostly just noise.
func (m Model) displayedWorktrees() []worktree.Entry {
	sorted := sortWorktrees(m.worktrees)
	if m.showMain {
		return sorted
	}
	filtered := make([]worktree.Entry, 0, len(sorted))
	for _, w := range sorted {
		if w.IsMain {
			continue
		}
		filtered = append(filtered, w)
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
// columns and rows. Columns are rebuilt here too, not just in resize:
// repoColumnWidth/branchColumnWidth depend on the worktree set itself,
// so a freshly polled repo with a longer owner/repo label or branch
// name needs the Repo/Branch column widened immediately, not just on
// the next terminal resize.
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

	// Clear rows before changing columns: see resize's own comment on why
	// (bubbles/table re-renders immediately against whatever's currently
	// set, so a column/row count mismatch mid-update panics).
	m.table.SetRows(nil)
	m.table.SetColumns(worktreeColumns(m.width, newDisplayed, m.colOverrides))
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
// (see sortWorktrees) worktree list. cursor picks which row gets tagged
// with loam.Sentinel (see loam pkg doc) so colorize.go's
// colorizeRows knows to highlight that row's whole line; there's no
// dedicated cursor column/glyph to place it in any more, now that the
// row highlight itself is the selection indicator.
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
func buildWorktreeRows(worktrees []worktree.Entry, cursor int, home string, now time.Time) []table.Row {
	if len(worktrees) == 0 {
		return []table.Row{{"", "", "", "", "", noWorktreesMessage()}}
	}

	rows := make([]table.Row, len(worktrees))
	for i, w := range worktrees {
		label := repoLabel(w)
		if i > 0 && label == repoLabel(worktrees[i-1]) {
			label = ""
		}
		created := humanizeSince(now.Sub(w.CreatedTime))
		if i == cursor {
			// Prepended, not appended: bubbles/table truncates a
			// too-long cell from the tail (runewidth.Truncate keeps the
			// head + an ellipsis), so a leading zero-width tag always
			// survives regardless of how long the cell's real content
			// is, where a trailing one could get truncated away along
			// with the tail. Created's own content is always short
			// (humanizeSince, e.g. "12s"/"3d") and never truncated in
			// practice, but the tag's placement is written to hold even
			// if that ever changed.
			created = loam.Sentinel + created
		}
		rows[i] = table.Row{
			label,
			w.Branch,
			created,
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
// setup gap, worth naming) from "no worktrees found yet" (e.g. the poll
// just hasn't landed).
func noWorktreesMessage() string {
	if !worktree.Available() {
		return "wt (worktrunk) is not installed; see https://worktrunk.dev"
	}
	return "no known worktrees (registered in ~/.cache/wt/known-repos, or the repo you're standing in)"
}

// worktreeSummaryLine returns a one-line "N worktrees: N dirty · N stale ·
// N merged · N clean · N unknown" breakdown, one mutually-exclusive bucket
// per worktree (most-actionable-first classification, same spirit as
// canopy's own summaryLine over its State column, folded down to a
// single dimension since "needs a look" is really one axis here even
// though it's backed by two Entry fields): Stale wins first (see
// Entry.Stale's doc: Dirty/MergeStatus are meaningless once true), then
// Dirty (uncommitted work, worth a look now), then a merged branch
// (nothing left to do but it's a removal candidate), then clean
// (still-open work with nothing outstanding). unknown is its own bucket
// rather than folded into clean: when `wt` couldn't determine a branch's
// relationship to main at all (Entry.MergeStatus's MergeStatusUnknown),
// that's not confidently "clean" (it might be safe to remove, might not,
// wt just doesn't know), so it's counted and labeled separately instead
// of silently overstating certainty. It sorts last since it's exactly as
// low-priority as clean in the Merge column's own coloring (colorize.go's
// mergeStatusStyles gives "unknown" the same dim grey as "-"), just
// without a confirmed merge relationship to back that up. Colored to
// match the Worktree/Merge table columns via the same style lookups.
// Returns "" if there are no worktrees, since the placeholder row
// already says so.
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
		case e.MergeStatus == worktree.MergeStatusUnknown:
			counts["unknown"]++
		default:
			counts["clean"]++
		}
	}

	var parts []string
	for _, bucket := range []string{"dirty", "stale", "merged", "clean", "unknown"} {
		n := counts[bucket]
		if n == 0 {
			continue
		}
		// merged/unknown are Merge-column words (mergeStatusStyles),
		// dirty/stale/clean are Worktree-column words
		// (worktreeStatusStyles); looking each up in its own map rather
		// than reusing one for both keeps this tied to the same source
		// of truth the table itself renders from, even though "clean"
		// and "unknown" happen to resolve to the same dim grey today.
		style := worktreeStatusStyle(bucket)
		if bucket == "merged" || bucket == "unknown" {
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
