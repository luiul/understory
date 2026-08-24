// Coloring the Worktree and Merge columns cannot be done by putting
// ANSI-styled strings directly into table.Row values: bubbles/table v1's
// cell truncation (runewidth.Truncate) is not ANSI-aware, so escape codes
// get counted as extra visible width and sliced mid-sequence, corrupting
// the row (verified empirically against bubbles/table v1.0.0: a styled
// "unmerged" in a 9-wide column gets truncated with a dangling escape
// code). Post-processing the table's already-rendered plain-text view
// instead sidesteps that entirely: the widths/padding/truncation the
// table computes are always over plain text, and only the final display
// string gets colored.
//
// This is the same technique (and largely the same code) as canopy's own
// internal/tui/colorize.go, adapted from canopy's single State column to
// understory's two independently-colored columns (Worktree, Merge).
//
// The selected row's whole-line highlight (highlightRow, below) is
// layered on top of that same rendered-text pass rather than done via
// bubbles/table's own Styles.Selected: Selected wraps the *already
// row-joined* string in its own escape codes before colorizeRows ever
// sees it, which would trip colorizeRows' "skip a line that already
// carries its own color" guard and silently drop the Worktree/Merge
// coloring on exactly the one row most worth reading clearly. Instead
// Styles.Selected stays empty (see app.go) and colorizeRows recolors
// Worktree/Merge first, then wraps that same, now partially-colored line
// in the row highlight last — safe because highlightRow reapplies its
// own opening sequence after every reset it finds already inside the
// line (see its doc), rather than assuming the line was plain to begin
// with.

package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
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

// rowHighlightStyle marks the entire selected row, not just its leading
// cursorMarker cell: with rows grouped by repo (see sortWorktrees), most
// of a block's rows look alike (blank Repo cell, similar Branch/
// Worktree/Merge text), so a 1-wide marker glyph was easy to lose track
// of. Reverse video (rather than a fixed background color) swaps
// whatever foreground/background a terminal already has, so the
// highlight reads correctly across light and dark terminal themes
// without understory having to guess one.
var rowHighlightStyle = lipgloss.NewStyle().Reverse(true)

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

// colOffset is a column's start position and width within a rendered row
// line, accounting for bubbles/table's fixed 1-space padding on both sides
// of every cell (table.DefaultStyles()'s Cell/Header Padding(0, 1)).
type colOffset struct {
	start, width int
}

// columnOffsets computes each column's start/width within a rendered line,
// given cols in the same order the table was built with. Only correct for
// bubbles/table's default (no border) layout, which is all understory uses.
func columnOffsets(cols []table.Column) []colOffset {
	offsets := make([]colOffset, len(cols))
	pos := 1 // leading pad of the first cell
	for i, c := range cols {
		offsets[i] = colOffset{start: pos, width: c.Width}
		pos += c.Width + 2 // this cell's trailing pad + the next cell's leading pad
	}
	return offsets
}

// colorizeRows recolors the Worktree and Merge columns of a table's
// already rendered view, each cell picking its style from its own word
// (e.g. "dirty" vs "clean"), and highlights the whole line of whichever
// row carries the cursor marker (see isCursorRow). cols must be the
// exact columns the view was rendered with; cursorCol/worktreeCol/
// mergeCol are indexes into cols. Pass -1 for any column that isn't
// present (e.g. a table built without a cursor column) to skip it
// without affecting the others.
//
// The header line and any line that already contains an escape sequence
// coming in (from some outer style applied before colorizeRows ever ran)
// is left untouched entirely: recoloring a sub-span of a line that
// already carries its own color would inject a reset code that cuts the
// outer style short for the rest of that line. That guard does not apply
// between the three steps below for the *same* line, though: Merge and
// Worktree recoloring only insert bytes into their own disjoint spans,
// and the row highlight applied last is specifically built (see
// highlightRow) to survive wrapping a line that already contains their
// escape codes.
func colorizeRows(view string, cols []table.Column, cursorCol, worktreeCol, mergeCol int) string {
	offsets := columnOffsets(cols)
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if i == 0 || strings.Contains(line, "\x1b") {
			continue
		}
		// Rightmost column first: recoloring inserts bytes, which would
		// shift the start offset of any column to its right if done first.
		isCursor := cursorCol >= 0 && cursorCol < len(cols) && isCursorRow(line, offsets[cursorCol])
		if mergeCol >= 0 && mergeCol < len(cols) {
			line = recolorByWord(line, offsets[mergeCol], mergeStatusStyle)
		}
		if worktreeCol >= 0 && worktreeCol < len(cols) {
			line = recolorByWord(line, offsets[worktreeCol], worktreeStatusStyle)
		}
		if isCursor {
			line = highlightRow(line, rowHighlightStyle)
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// isCursorRow reports whether line's cell at the cursor column (off)
// holds the cursor marker rather than blank filler. Checked before
// recolorByWord ever touches worktreeCol/mergeCol: those only insert
// bytes into their own spans (to the right of the cursor column, see
// worktrees.go's colCursor/colWorktree/colMerge ordering), so the cursor
// column's own start offset is unaffected regardless of whether this
// check runs before or after them.
func isCursorRow(line string, off colOffset) bool {
	start := displayColumnToByteOffset(line, off.start)
	end := displayColumnToByteOffset(line, off.start+off.width)
	if start >= len(line) || end > len(line) || start > end {
		return false
	}
	return strings.TrimRight(line[start:end], " ") == cursorMarker
}

// highlightRow wraps the whole of line in style, e.g. rowHighlightStyle,
// even when line already contains other ANSI escape codes (the Worktree/
// Merge recoloring colorizeRows applies first). A naive open+line+close
// wrap would break the moment line contains its own reset code: every
// lipgloss render ends with a full SGR reset ("\x1b[0m", verified
// against lipgloss/termenv directly — true regardless of which
// attributes were opened), so an inner reset would end style's effect
// for the remainder of line, well before the intended closing tag.
// Reapplying style's own opening sequence immediately after every such
// inner reset keeps it in effect right up to the final, real close.
func highlightRow(line string, style lipgloss.Style) string {
	open, closeSeq := styleSequences(style)
	if open == "" {
		return line // no color support (e.g. NoColor profile): nothing to wrap
	}
	body := line
	if closeSeq != "" {
		body = strings.ReplaceAll(line, closeSeq, closeSeq+open)
	}
	return open + body + closeSeq
}

// styleSequences extracts style's opening and closing escape sequences by
// rendering it around a NUL byte and splitting on that byte, rather than
// assuming any particular SGR codes: NUL can't otherwise appear in a
// rendered table view, and this stays correct regardless of which
// attributes style combines or whether color is even enabled (in which
// case lipgloss renders content unchanged and both return values are
// "").
func styleSequences(style lipgloss.Style) (open, closeSeq string) {
	rendered := style.Render("\x00")
	idx := strings.IndexByte(rendered, 0)
	if idx < 0 {
		return "", ""
	}
	return rendered[:idx], rendered[idx+1:]
}

// recolorByWord wraps the display-column span of line at off in whichever
// style lookup(word) returns for that span's trimmed text, preserving
// line's total length. An empty word (blank filler row below the real
// data, or the placeholder row) is left alone.
//
// off's start/width are display columns, not byte offsets: naively
// slicing line as line[off.start:off.start+off.width] silently corrupts
// this the moment any earlier column contains a multi-byte rune whose
// byte length doesn't match its display width — the truncation ellipsis
// "…" bubbles/table's own runewidth.Truncate appends to an over-long Repo
// or Branch cell, or a genuinely unicode repo/branch name. displayColumnToByteOffset
// walks the line rune-by-rune to find the real byte offsets first.
func recolorByWord(line string, off colOffset, lookup func(string) lipgloss.Style) string {
	start := displayColumnToByteOffset(line, off.start)
	end := displayColumnToByteOffset(line, off.start+off.width)
	if start >= len(line) || end > len(line) || start > end {
		return line
	}
	slice := line[start:end]
	word := strings.TrimRight(slice, " ")
	if word == "" {
		return line
	}
	pad := strings.Repeat(" ", len(slice)-len(word))
	return line[:start] + lookup(word).Render(word) + pad + line[end:]
}

// displayColumnToByteOffset returns the byte index in line at which
// display column col begins. Returns len(line) once col reaches or
// passes the line's own display width.
func displayColumnToByteOffset(line string, col int) int {
	width := 0
	for i, r := range line {
		if width >= col {
			return i
		}
		width += runewidth.RuneWidth(r)
	}
	return len(line)
}
