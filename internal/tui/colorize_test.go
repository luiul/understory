package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// withForcedColor forces lipgloss to emit real ANSI (tests otherwise run
// with stdout not a tty, which lipgloss auto-detects and downgrades to no
// color), restoring the original profile afterward so this doesn't leak
// into other tests.
func withForcedColor(t *testing.T) {
	t.Helper()
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })
}

func TestColumnOffsetsAccountForOneSpacePaddingOnBothSidesOfEachCell(t *testing.T) {
	cols := []table.Column{{Title: "A", Width: 3}, {Title: "B", Width: 5}}

	offsets := columnOffsets(cols)

	if offsets[0] != (colOffset{start: 1, width: 3}) {
		t.Fatalf("got %+v, want start=1 width=3", offsets[0])
	}
	// 1 (leading pad) + 3 (A) + 2 (A's trailing pad + B's leading pad) = 6
	if offsets[1] != (colOffset{start: 6, width: 5}) {
		t.Fatalf("got %+v, want start=6 width=5", offsets[1])
	}
}

func TestColorizeRowsAppliesTheWorktreeAndMergeStylesToAnUnselectedRow(t *testing.T) {
	withForcedColor(t)
	cols := []table.Column{
		{Title: "Worktree", Width: 8},
		{Title: "Merge", Width: 9},
	}
	tbl := table.New(table.WithColumns(cols), table.WithHeight(3))
	tbl.SetRows([]table.Row{{"clean", "-"}, {"dirty", "unmerged"}})
	tbl.SetCursor(0) // row 0 selected; row 1 (the one we check) is not

	got := colorizeRows(tbl.View(), tbl.Columns(), 0, 1)

	wantWorktree := worktreeStatusStyle("dirty").Render("dirty")
	wantMerge := mergeStatusStyle("unmerged").Render("unmerged")
	if !strings.Contains(got, wantWorktree) {
		t.Fatalf("got %q, want it to contain the styled word %q", got, wantWorktree)
	}
	if !strings.Contains(got, wantMerge) {
		t.Fatalf("got %q, want it to contain the styled word %q", got, wantMerge)
	}
}

func TestColorizeRowsHighlightsTheWholeCursorRowButNotOthers(t *testing.T) {
	withForcedColor(t)
	cols := []table.Column{
		{Title: "Worktree", Width: 8},
		{Title: "Merge", Width: 9},
	}
	tbl := table.New(table.WithColumns(cols), table.WithHeight(3))
	// Row 1 gets the sentinel prepended to its Merge cell to mark it as the
	// selected row (simulating what buildWorktreeRows would do to colUpdated).
	tbl.SetRows([]table.Row{{"clean", "-"}, {"dirty", cursorSentinel + "unmerged"}})

	got := colorizeRows(tbl.View(), tbl.Columns(), 0, 1)
	lines := strings.Split(got, "\n")

	open, closeSeq := styleSequences(rowHighlightStyle)
	if open == "" {
		t.Fatal("styleSequences returned no escape codes; withForcedColor isn't taking effect")
	}
	if strings.Contains(lines[1], open) {
		t.Fatalf("got the row highlight on the non-cursor row %q, want it left alone", lines[1])
	}
	if !strings.HasPrefix(lines[2], open) {
		t.Fatalf("got cursor row %q, want it to start with the row highlight's opening sequence %q", lines[2], open)
	}
	if !strings.HasSuffix(lines[2], closeSeq) {
		t.Fatalf("got cursor row %q, want it to end with the row highlight's closing sequence %q", lines[2], closeSeq)
	}
	// The Worktree/Merge status words must still be individually colored
	// *inside* the row highlight, not just left plain because the row as a
	// whole is already ANSI-wrapped.
	wantWorktree := worktreeStatusStyle("dirty").Render("dirty")
	if !strings.Contains(lines[2], wantWorktree) {
		t.Fatalf("got cursor row %q, want it to still contain the styled word %q", lines[2], wantWorktree)
	}
	// The sentinel must be stripped from the output before returning, so it
	// never leaks into a terminal or gets copy-pasted.
	if strings.Contains(got, cursorSentinel) {
		t.Fatalf("got output still containing cursorSentinel %q, want it stripped out", got)
	}
}

func TestHighlightRowReappliesItsOpeningSequenceAfterAnInnerReset(t *testing.T) {
	withForcedColor(t)
	// Simulates what colorizeRows hands highlightRow: a line that already
	// contains its own inner-colored (and therefore inner-reset) span.
	inner := worktreeStatusStyle("dirty").Render("dirty")
	line := "before " + inner + " after"

	got := highlightRow(line, rowHighlightStyle)

	open, closeSeq := styleSequences(rowHighlightStyle)
	want := open + "before " + inner + open + " after" + closeSeq
	if got != want {
		t.Fatalf("got %q, want %q (outer style reapplied right after the inner reset)", got, want)
	}
}

func TestHighlightRowIsANoOpWithoutColorSupport(t *testing.T) {
	// No withForcedColor: lipgloss should downgrade to NoColor here, so
	// styleSequences has nothing to wrap with and highlightRow must return
	// line unchanged rather than, say, panic on an empty ReplaceAll old
	// value.
	if got, want := highlightRow("plain line", rowHighlightStyle), "plain line"; got != want {
		t.Fatalf("got %q, want %q unchanged", got, want)
	}
}

func TestStyleSequencesSplitsOpenAndCloseAroundTheRenderedContent(t *testing.T) {
	withForcedColor(t)
	open, closeSeq := styleSequences(rowHighlightStyle)
	if open == "" || closeSeq == "" {
		t.Fatalf("got open=%q close=%q, want both non-empty with color forced on", open, closeSeq)
	}
	if got, want := rowHighlightStyle.Render("x"), open+"x"+closeSeq; got != want {
		t.Fatalf("got %q, want open+content+close to reconstruct the style's own render %q", got, want)
	}
}

func TestColorizeRowsSkipsALineThatAlreadyCarriesItsOwnAnsi(t *testing.T) {
	withForcedColor(t)
	// colorizeRows must leave a line that already contains an escape
	// sequence untouched rather than recolor a sub-span of it (which would
	// inject a reset code that cuts the outer style short for the rest of
	// that line). Simulate that by handing it a pre-styled line directly,
	// since understory's own Selected style is overridden to empty and so
	// never produces one in practice.
	cols := []table.Column{
		{Title: "Worktree", Width: 8},
		{Title: "Merge", Width: 9},
	}
	preStyled := lipgloss.NewStyle().Reverse(true).Render("dirty    unmerged ")
	view := "Worktree Merge\n" + preStyled

	got := colorizeRows(view, cols, 0, 1)

	if got != view {
		t.Fatalf("got a modified pre-styled line:\n%q\nwant it unchanged from:\n%q", got, view)
	}
}

func TestColorizeRowsLeavesTheHeaderLineUntouched(t *testing.T) {
	withForcedColor(t)
	cols := []table.Column{
		{Title: "Worktree", Width: 8},
		{Title: "Merge", Width: 9},
	}
	tbl := table.New(table.WithColumns(cols), table.WithHeight(3))
	tbl.SetRows([]table.Row{{"dirty", "unmerged"}})

	rendered := tbl.View()
	wantHeaderLine := strings.Split(rendered, "\n")[0] // bold by default, even before colorizeRows

	got := colorizeRows(rendered, tbl.Columns(), 0, 1)
	gotHeaderLine := strings.Split(got, "\n")[0]

	if gotHeaderLine != wantHeaderLine {
		t.Fatalf("got header line %q, want it byte-identical to the table's own %q", gotHeaderLine, wantHeaderLine)
	}
}

func TestColorizeRowsStillColorsAfterAMultiByteRuneInAnEarlierColumn(t *testing.T) {
	// Regression test: bubbles/table truncates an over-long cell with a
	// "…" ellipsis (3 UTF-8 bytes, 1 display column). A naive byte-offset
	// slice for a later column silently misaligns on any row where an
	// earlier column (Repo/Branch here) got truncated that way — or just
	// contains a non-ASCII name — landing on the wrong bytes, extracting
	// garbage instead of a known status word, and so (via the style
	// lookups' silent fallback) rendering with no color at all rather than
	// erroring. Verified empirically against real `wt` output where a
	// truncated Branch cell made every row after it in that render lose
	// its Worktree/Merge coloring.
	withForcedColor(t)
	cols := []table.Column{
		{Title: "Repo", Width: 6},
		{Title: "Worktree", Width: 8},
		{Title: "Merge", Width: 9},
	}
	tbl := table.New(table.WithColumns(cols), table.WithHeight(3))
	// runewidth.Truncate("a-long-name", 6, "…") -> "a-lon…": 6 display
	// columns, but 7 bytes (the ellipsis is 3 bytes for 1 column).
	// Row 0 stays selected (and so already ANSI-wrapped, which
	// colorizeRows correctly skips); row 1 is the one under test.
	tbl.SetRows([]table.Row{
		{"other", "clean", "-"},
		{"a-lon…", "dirty", "unmerged"},
	})
	tbl.SetCursor(0)

	got := colorizeRows(tbl.View(), tbl.Columns(), 1, 2)

	wantWorktree := worktreeStatusStyle("dirty").Render("dirty")
	wantMerge := mergeStatusStyle("unmerged").Render("unmerged")
	if !strings.Contains(got, wantWorktree) {
		t.Fatalf("got %q, want it to still contain the styled word %q despite the earlier multi-byte rune", got, wantWorktree)
	}
	if !strings.Contains(got, wantMerge) {
		t.Fatalf("got %q, want it to still contain the styled word %q despite the earlier multi-byte rune", got, wantMerge)
	}
}

func TestDisplayColumnToByteOffsetAccountsForMultiByteRunes(t *testing.T) {
	line := "a-lon… dirty"
	// "a-lon…" is 6 display columns (5 ASCII + 1 for the ellipsis) but 8
	// bytes (5 + 3-byte ellipsis); column 7 (the space) should map to byte
	// 8, not byte 7.
	if got, want := displayColumnToByteOffset(line, 7), 9; got != want {
		t.Fatalf("got byte offset %d, want %d", got, want)
	}
}

func TestWorktreeStatusStyleFallsBackToPlainForAnUnknownWord(t *testing.T) {
	if got, want := worktreeStatusStyle("bogus").Render("bogus"), lipgloss.NewStyle().Render("bogus"); got != want {
		t.Fatalf("got %q, want the zero style's rendering %q for an unrecognized word", got, want)
	}
}

func TestMergeStatusStyleFallsBackToPlainForAnUnknownWord(t *testing.T) {
	if got, want := mergeStatusStyle("bogus").Render("bogus"), lipgloss.NewStyle().Render("bogus"); got != want {
		t.Fatalf("got %q, want the zero style's rendering %q for an unrecognized word", got, want)
	}
}
