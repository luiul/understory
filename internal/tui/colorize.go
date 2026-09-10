// Worktree/Merge column coloring, the Branch column's mismatch-suffix
// coloring, and the selected row's whole-line highlight are all handled
// by github.com/luiul/dashkit/loam, the rendering substrate this and
// canopy's own internal/tui/colorize.go share (see loam's package doc
// for why post-processing an already-rendered bubbles/table view,
// rather than styling table.Row values directly, is necessary at all).
// This file only holds what's specific to understory: which words map
// to which color, how the mismatch suffix splits into styled segments,
// and the row highlight's own look.
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/luiul/dashkit/loam"
)

var worktreeStatusStyles = map[string]lipgloss.Style{
	"dirty": lipgloss.NewStyle().Foreground(lipgloss.Color("11")),           // uncommitted changes: worth a look
	"stale": lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")), // directory's gone: a removal candidate
	"clean": lipgloss.NewStyle().Foreground(lipgloss.Color("240")),          // nothing to do here
	// task-complete and set aside for follow-up: a calm but legible blue,
	// the one bright hue no other word on screen uses (yellow/red signal
	// work, green merged, grey noise, magenta the mismatch segment). Faint
	// was the first pick and read as nearly invisible, especially under
	// the selected row's grey highlight band. Adaptive, like the row
	// highlight itself, so light themes get a darker blue that survives
	// the white background (see rowHighlightStyle).
	"parked": lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "4", Dark: "12"}),
}

var mergeStatusStyles = map[string]lipgloss.Style{
	"merged":   lipgloss.NewStyle().Foreground(lipgloss.Color("10")),  // safe to remove
	"unmerged": lipgloss.NewStyle().Foreground(lipgloss.Color("11")),  // still has commits main doesn't, merges cleanly
	"conflict": lipgloss.NewStyle().Foreground(lipgloss.Color("9")),   // merging would conflict: act before main moves further
	"unknown":  lipgloss.NewStyle().Foreground(lipgloss.Color("240")), // wt couldn't tell
	"-":        lipgloss.NewStyle().Foreground(lipgloss.Color("240")), // not applicable (main, or stale)
}

// The Branch column's mismatch suffix ("branch @ dir/", see branchLabel
// in worktrees.go) gets the same treatment coppice gives it
// ("[dim]@[/] [magenta]dir/[/]"), so the two tools read consistently:
// the "@" dim, the directory segment magenta. understory's palette is
// the bright set (9/10/11 above), so the segment is bright magenta (13)
// where coppice's rich "magenta" is the standard one. The branch name
// itself stays plain: the suffix is context (the worktree sits at
// another branch's path), not identity.
var (
	mismatchAtStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	mismatchSegmentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
)

// rowHighlightStyle marks the entire selected row rather than a leading
// marker glyph: with rows grouped by repo (see sortWorktrees), most of a
// block's rows look alike (blank Repo cell, similar Branch/Worktree/
// Merge text), so a 1-wide marker glyph was easy to lose track of, and
// once the whole row is highlighted the marker itself becomes redundant
// (removed; see cursorSentinel in app.go for how the row is identified
// instead now). A muted grey background band reads as "current row" the
// way most modern list UIs (editor gutters, lazygit, k9s) already do;
// full-invert Reverse(true) worked but read harsher and more dated, and
// fought a bit with the Worktree/Merge foreground colors nested inside
// it (see loam.HighlightRow) since reversing also inverts *their*
// colors, not just the row's background. AdaptiveColor picks a shade
// lighter on a light terminal and a shade darker on a dark one, rather
// than a single fixed grey that could wash out on one theme or the
// other.
var rowHighlightStyle = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "254", Dark: "237"})

// worktreeStatusStyle and mergeStatusStyle share coppice's own color
// choices for the same three/four words ([yellow]dirty[/], [dim]clean[/],
// [green]merged[/], [yellow]unmerged[/], [dim]unknown[/]), so the two
// tools read consistently; "stale" (a removal candidate) gets its own
// louder red, since coppice never renders that word itself (its `_age_days`
// returns it unstyled, folded into the Age column rather than a status
// column of its own). "conflict" is understory-only the same way (coppice
// never surfaces wt's would_conflict state as its own word) and borrows
// the same bright red: it is the one Merge state that gets worse on its
// own the longer main moves, so it should shout louder than unmerged's
// yellow.
func worktreeStatusStyle(word string) lipgloss.Style {
	if s, ok := worktreeStatusStyles[word]; ok {
		return s
	}
	return lipgloss.NewStyle()
}

func mergeStatusStyle(word string) lipgloss.Style {
	if s, ok := mergeStatusStyles[word]; ok {
		return s
	}
	return lipgloss.NewStyle()
}

// branchSegments is the Branch column's WordColumn.Segment: a mismatch
// row's label gets its " @ dir/" suffix styled per coppice (dim "@",
// magenta segment), while a plain branch name returns nil and renders
// unstyled. The parse keys on the label's own shape, the last " @ " plus
// the trailing slash that marks the segment as a directory name, which
// a real branch name can never produce: git ref names forbid both
// spaces and a trailing slash.
func branchSegments(word string) []loam.Segment {
	at := strings.LastIndex(word, " @ ")
	if at < 0 || !strings.HasSuffix(word, "/") {
		return nil
	}
	segment := word[at+len(" @ ") : len(word)-1]
	if segment == "" || strings.Contains(segment, "/") {
		return nil
	}
	plain := lipgloss.NewStyle()
	return []loam.Segment{
		{Text: word[:at] + " ", Style: plain},
		{Text: "@", Style: mismatchAtStyle},
		{Text: " ", Style: plain},
		{Text: segment + "/", Style: mismatchSegmentStyle},
	}
}

// colorizeRows recolors the Worktree and Merge columns and the Branch
// column's mismatch suffix of a table's already rendered view and
// highlights the whole line of whichever row carries cursorSentinel
// (see app.go's doc on it), by delegating straight to loam.ColorizeRows.
func colorizeRows(view string, cols []table.Column, worktreeCol, mergeCol int) string {
	return loam.ColorizeRows(view, cols, []loam.WordColumn{
		{Index: mergeCol, Style: mergeStatusStyle},
		{Index: worktreeCol, Style: worktreeStatusStyle},
		{Index: colBranch, Segment: branchSegments},
	}, rowHighlightStyle)
}
