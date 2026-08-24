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

	got := colorizeRows(tbl.View(), tbl.Columns(), -1, 0, 1)

	wantWorktree := worktreeStatusStyle("dirty").Render("dirty")
	wantMerge := mergeStatusStyle("unmerged").Render("unmerged")
	if !strings.Contains(got, wantWorktree) {
		t.Fatalf("got %q, want it to contain the styled word %q", got, wantWorktree)
	}
	if !strings.Contains(got, wantMerge) {
		t.Fatalf("got %q, want it to contain the styled word %q", got, wantMerge)
	}
}

func TestColorizeRowsAccentsTheCursorMarkerOnTheSelectedRow(t *testing.T) {
	withForcedColor(t)
	cols := []table.Column{
		{Title: "", Width: 1}, // cursor marker
		{Title: "Worktree", Width: 8},
		{Title: "Merge", Width: 9},
	}
	tbl := table.New(table.WithColumns(cols), table.WithHeight(3))
	tbl.SetRows([]table.Row{{"", "clean", "-"}, {cursorMarker, "dirty", "unmerged"}})
	tbl.SetCursor(0) // table's own cursor is unrelated to the marker cell values we set directly

	got := colorizeRows(tbl.View(), tbl.Columns(), 0, 1, 2)

	want := cursorMarkerStyle.Render(cursorMarker)
	if !strings.Contains(got, want) {
		t.Fatalf("got %q, want it to contain the accented marker %q", got, want)
	}
}

func TestColorizeRowsLeavesTheCursorColumnAloneWhenPassedMinusOne(t *testing.T) {
	// -1 means "no cursor column in this table" (see e.g. the tests above
	// that only pass Worktree/Merge columns); colorizeRows must not panic
	// or otherwise misbehave when told to skip it.
	withForcedColor(t)
	cols := []table.Column{
		{Title: "", Width: 1},
		{Title: "Worktree", Width: 8},
		{Title: "Merge", Width: 9},
	}
	tbl := table.New(table.WithColumns(cols), table.WithHeight(3))
	tbl.SetRows([]table.Row{{cursorMarker, "dirty", "unmerged"}})

	got := colorizeRows(tbl.View(), tbl.Columns(), -1, 1, 2)

	unwanted := cursorMarkerStyle.Render(cursorMarker)
	if strings.Contains(got, unwanted) {
		t.Fatalf("got %q, want the cursor column left unaccented when cursorCol is -1", got)
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

	got := colorizeRows(view, cols, -1, 0, 1)

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

	got := colorizeRows(rendered, tbl.Columns(), -1, 0, 1)
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

	got := colorizeRows(tbl.View(), tbl.Columns(), -1, 1, 2)

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
