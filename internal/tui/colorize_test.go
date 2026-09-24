package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/luiul/dashkit/loam"
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

// newColorizeTable builds a table with the columns colorizeRows touches,
// at their real indexes (see the col* consts in worktrees.go), so the
// tests exercise the same wiring app.go passes through. Selected is
// overridden to empty: the row highlight is colorizeRows' post-render
// job, and the table's own default Selected style would trip
// loam.ColorizeRows' already-carries-ANSI skip guard on the cursor row.
func newColorizeTable(rows ...table.Row) table.Model {
	cols := []table.Column{
		{Title: "Repo", Width: 18},
		{Title: "Branch", Width: 30},
		{Title: "Created", Width: 8},
		{Title: "Worktree", Width: 8},
		{Title: "Merge", Width: 9},
	}
	tbl := table.New(table.WithColumns(cols), table.WithHeight(len(rows)+1))
	styles := table.DefaultStyles()
	styles.Selected = lipgloss.NewStyle()
	tbl.SetStyles(styles)
	tbl.SetRows(rows)
	return tbl
}

func TestBranchSegmentsSplitsTheMismatchLabelIntoFourSpans(t *testing.T) {
	withForcedColor(t)

	segments := branchSegments("feature/other @ review-jamie/")

	if len(segments) != 4 {
		t.Fatalf("got %d segments (%+v), want 4", len(segments), segments)
	}
	wantTexts := []string{"feature/other ", "@", " ", "review-jamie/"}
	var texts []string
	for _, s := range segments {
		texts = append(texts, s.Text)
	}
	for i, want := range wantTexts {
		if texts[i] != want {
			t.Fatalf("segment %d text = %q, want %q (all: %q)", i, texts[i], want, texts)
		}
	}
	// The segments must reassemble the word exactly, or
	// loam.RecolorSegments declines to style the cell at all.
	if got := strings.Join(texts, ""); got != "feature/other @ review-jamie/" {
		t.Fatalf("segments reassemble to %q, want the original label", got)
	}
	// coppice parity: the "@" dim, the directory segment magenta, the
	// branch name and the space between plain.
	if got, want := segments[1].Style.Render("@"), mismatchAtStyle.Render("@"); got != want {
		t.Fatalf("@ segment renders %q, want the dim style's %q", got, want)
	}
	if got, want := segments[3].Style.Render("review-jamie/"), mismatchSegmentStyle.Render("review-jamie/"); got != want {
		t.Fatalf("directory segment renders %q, want the magenta style's %q", got, want)
	}
}

func TestBranchSegmentsDeclinesAnythingButTheMismatchLabel(t *testing.T) {
	for _, word := range []string{
		"main",                         // plain branch name
		"feature/other",                // plain branch name with a slash
		"feature/other @ review-jamie", // no trailing slash: not the directory marker
		"feature/other @ /",            // empty segment
		"feature/other @ a/b/",         // an inner slash filepath.Base could never produce
	} {
		if got := branchSegments(word); got != nil {
			t.Fatalf("branchSegments(%q) = %+v, want nil (cell left unstyled)", word, got)
		}
	}
}

func TestColorizeRowsColorsTheMismatchSuffixButNotTheBranchName(t *testing.T) {
	withForcedColor(t)
	tbl := newColorizeTable(
		table.Row{"luiul/understory", "feature/other @ review-jamie/", "3d", "clean", "unmerged"},
		table.Row{"luiul/understory", "main", "12s", "dirty", "-"},
	)

	got := colorizeRows(tbl.View(), tbl.Columns(), colWorktree, colMerge)
	lines := strings.Split(got, "\n")

	if want := mismatchAtStyle.Render("@"); !strings.Contains(lines[1], want) {
		t.Fatalf("got mismatch row %q, want it to contain the dim @ %q", lines[1], want)
	}
	if want := mismatchSegmentStyle.Render("review-jamie/"); !strings.Contains(lines[1], want) {
		t.Fatalf("got mismatch row %q, want it to contain the magenta segment %q", lines[1], want)
	}
	// The branch name itself stays plain: it appears exactly as the table
	// drew it, pad space included, with no escape codes wrapped around it
	// (a styled render would break the contiguous " feature/other ").
	if !strings.Contains(lines[1], " feature/other ") {
		t.Fatalf("got mismatch row %q, want the branch name left as plain text", lines[1])
	}
	// A plain branch name gets no segments at all: " main " appears
	// exactly as the table drew it, with no escape codes wrapped around
	// it (the row's only styling is the Merge column's "-").
	if !strings.Contains(lines[2], " main ") {
		t.Fatalf("got plain-branch row %q, want the branch cell left as plain text", lines[2])
	}
}

func TestColorizeRowsHighlightsTheSelectedMismatchRowWithTheSuffixInside(t *testing.T) {
	withForcedColor(t)
	tbl := newColorizeTable(
		table.Row{"luiul/understory", "main", "3d", "clean", "-"},
		table.Row{"luiul/understory", "feature/other @ review-jamie/", cursorSentinel + "12s", "clean", "unmerged"},
	)

	got := colorizeRows(tbl.View(), tbl.Columns(), colWorktree, colMerge)
	lines := strings.Split(got, "\n")

	open, closeSeq := loam.StyleSequences(rowHighlightStyle)
	if open == "" {
		t.Fatal("StyleSequences returned no escape codes; withForcedColor isn't taking effect")
	}
	if strings.Contains(lines[1], open) {
		t.Fatalf("got the row highlight on the non-tagged row %q, want it left alone", lines[1])
	}
	if !strings.HasPrefix(lines[2], open) || !strings.HasSuffix(lines[2], closeSeq) {
		t.Fatalf("got tagged row %q, want it wrapped start-to-end in the highlight's open/close sequences", lines[2])
	}
	if strings.Contains(got, cursorSentinel) {
		t.Fatalf("got %q, want cursorSentinel stripped out of the final output entirely", got)
	}
	// The mismatch suffix's styles must survive inside the row highlight,
	// not get cut short by it (loam.HighlightRow reapplies its opener
	// after every inner reset).
	if want := mismatchSegmentStyle.Render("review-jamie/"); !strings.Contains(lines[2], want) {
		t.Fatalf("got tagged row %q, want the magenta segment %q to survive inside the highlight", lines[2], want)
	}
}

func TestColorizeRowsGreysOutTheParkedRowButKeepsTheParkedWordBright(t *testing.T) {
	withForcedColor(t)
	tbl := newColorizeTable(
		table.Row{"luiul/understory", "main", "3d", "clean", "-"},
		table.Row{"luiul/understory", "feature/done", parkedMarker + "2d", "parked", "merged"},
	)

	got := colorizeRows(tbl.View(), tbl.Columns(), colWorktree, colMerge)
	lines := strings.Split(got, "\n")

	dimOpen, _ := loam.StyleSequences(parkedRowStyle)
	if dimOpen == "" {
		t.Fatal("StyleSequences returned no escape codes; withForcedColor isn't taking effect")
	}
	if strings.Contains(lines[1], dimOpen) {
		t.Fatalf("got the grey-out on the non-parked row %q, want it left alone", lines[1])
	}
	if !strings.HasPrefix(lines[2], dimOpen) {
		t.Fatalf("got parked row %q, want it greyed out from the start of the line", lines[2])
	}
	// The parked word itself stays bright inside the grey (SGR 22 spliced
	// into its own opening sequence): the one signal a parked row exists
	// to show, the same exception coppice makes for its own parked label.
	parkedOpen, _ := loam.StyleSequences(worktreeStatusStyle("parked"))
	if want := parkedOpen + "\x1b[22m" + "parked"; !strings.Contains(lines[2], want) {
		t.Fatalf("got parked row %q, want the parked word re-brightened as %q", lines[2], want)
	}
	if strings.Contains(got, parkedMarker) {
		t.Fatalf("got %q, want parkedMarker stripped out of the final output entirely", got)
	}
}

func TestColorizeRowsGreysOutASelectedParkedRowWithoutBreakingTheHighlightBand(t *testing.T) {
	withForcedColor(t)
	tbl := newColorizeTable(
		table.Row{"luiul/understory", "main", "3d", "clean", "-"},
		table.Row{"luiul/understory", "feature/done", cursorSentinel + parkedMarker + "2d", "parked", "merged"},
	)

	got := colorizeRows(tbl.View(), tbl.Columns(), colWorktree, colMerge)
	lines := strings.Split(got, "\n")

	dimOpen, _ := loam.StyleSequences(parkedRowStyle)
	bandOpen, _ := loam.StyleSequences(rowHighlightStyle)
	parkedOpen, _ := loam.StyleSequences(worktreeStatusStyle("parked"))
	if dimOpen == "" || bandOpen == "" || parkedOpen == "" {
		t.Fatal("StyleSequences returned no escape codes; withForcedColor isn't taking effect")
	}
	// Greyed out AND carrying the selection band.
	if !strings.HasPrefix(lines[2], dimOpen) || !strings.Contains(lines[2], bandOpen) {
		t.Fatalf("got selected parked row %q, want it greyed out and highlighted", lines[2])
	}
	// The parked word is bright, and the band keeps flowing behind it
	// (the band's opener is the last styling still active when the word's
	// own opener starts, no reset in between) instead of gapping behind
	// the word.
	word := strings.Index(lines[2], parkedOpen)
	if word < 0 || !strings.HasPrefix(lines[2][word:], parkedOpen+"\x1b[22m") {
		t.Fatalf("got selected parked row %q, want the parked word bright inside the grey-out", lines[2])
	}
	before := lines[2][:word]
	if strings.LastIndex(before, bandOpen) < strings.LastIndex(before, "\x1b[0m") {
		t.Fatalf("got selected parked row %q, want the highlight band still open at the parked word", lines[2])
	}
}
