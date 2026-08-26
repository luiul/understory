// Worktree/Merge column coloring and the selected row's whole-line
// highlight are both handled by github.com/luiul/dashkit/loam, the rendering
// substrate this and canopy's own internal/tui/colorize.go share (see
// loam's package doc for why post-processing an already-rendered
// bubbles/table view, rather than styling table.Row values directly, is
// necessary at all). This file only holds what's specific to
// understory: which words map to which color, and the row highlight's
// own look.
package tui

import (
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/luiul/dashkit/loam"
)

var worktreeStatusStyles = map[string]lipgloss.Style{
	"dirty": lipgloss.NewStyle().Foreground(lipgloss.Color("11")),           // uncommitted changes: worth a look
	"stale": lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")), // directory's gone: a removal candidate
	"clean": lipgloss.NewStyle().Foreground(lipgloss.Color("240")),          // nothing to do here
}

var mergeStatusStyles = map[string]lipgloss.Style{
	"merged":   lipgloss.NewStyle().Foreground(lipgloss.Color("10")),  // safe to remove
	"unmerged": lipgloss.NewStyle().Foreground(lipgloss.Color("11")),  // still has commits main doesn't
	"unknown":  lipgloss.NewStyle().Foreground(lipgloss.Color("240")), // wt couldn't tell
	"-":        lipgloss.NewStyle().Foreground(lipgloss.Color("240")), // not applicable (main, or stale)
}

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
// column of its own).
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

// colorizeRows recolors the Worktree and Merge columns of a table's
// already rendered view and highlights the whole line of whichever row
// carries cursorSentinel (see app.go's doc on it), by delegating
// straight to loam.ColorizeRows.
func colorizeRows(view string, cols []table.Column, worktreeCol, mergeCol int) string {
	return loam.ColorizeRows(view, cols, []loam.WordColumn{
		{Index: mergeCol, Style: mergeStatusStyle},
		{Index: worktreeCol, Style: worktreeStatusStyle},
	}, rowHighlightStyle)
}
