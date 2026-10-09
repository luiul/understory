package tui

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/luiul/dashkit/loam"
	"github.com/luiul/dashkit/trellis"
	"github.com/luiul/understory/internal/worktree"
)

func columnWidths(cols []table.Column) []int {
	widths := make([]int, len(cols))
	for i, col := range cols {
		widths[i] = col.Width
	}
	return widths
}

func tableWidth(cols []table.Column) int {
	width := loam.CellPadding * len(cols)
	for _, col := range cols {
		width += col.Width
	}
	return width
}

func layoutFixture() worktree.Entry {
	w := wtEntry("/home/alex/worktrees/"+strings.Repeat("p", 30)+"/widgets", "issue/ISA-18408_dedupe-satellite-replay-echoes", 0)
	w.Owner, w.Repo = "hellofresh", "tardis-community"
	w.MergeStatus = "unmerged"
	return w
}

func TestWorktreeColumnsExactAutomaticWidths(t *testing.T) {
	policies := worktreeColumnPolicies([]worktree.Entry{layoutFixture()}, "/home/alex", time.Now(), nil)
	cases := []struct {
		width int
		want  []int
	}{
		{100, []int{18, 26, 8, 9, 9, 8, 8}},
		{110, []int{21, 33, 8, 9, 9, 8, 8}},
		{120, []int{24, 40, 8, 9, 9, 8, 8}},
		{140, []int{27, 46, 8, 9, 9, 8, 19}},
		{180, []int{29, 50, 8, 9, 9, 8, 53}},
		{240, []int{41, 74, 8, 9, 9, 8, 77}},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.width), func(t *testing.T) {
			cols, fits := worktreeColumns(tc.width, policies, trellis.Preferences{})
			if !fits || !reflect.DeepEqual(columnWidths(cols), tc.want) {
				t.Fatalf("widths = %v, fits = %v, want %v, true", columnWidths(cols), fits, tc.want)
			}
			if got := tableWidth(cols); got != tc.width {
				t.Fatalf("padded width = %d, want %d", got, tc.width)
			}
			for i, col := range cols {
				if col.Width < policies[i].HardMinimum {
					t.Fatalf("%s width = %d, below hard floor %d", col.Title, col.Width, policies[i].HardMinimum)
				}
			}
		})
	}
}

func TestAutomaticSurplusUsesOneTwoTwoWeightsAndCompactDefaults(t *testing.T) {
	policies := worktreeColumnPolicies(nil, "", time.Now(), nil)
	base, fits := worktreeColumns(104, policies, trellis.Preferences{})
	if !fits || !reflect.DeepEqual(columnWidths(base), []int{16, 20, 8, 9, 9, 8, 20}) {
		t.Fatalf("readable defaults = %v, fits %v", columnWidths(base), fits)
	}
	wide, fits := worktreeColumns(240, policies, trellis.Preferences{})
	if !fits || !reflect.DeepEqual(columnWidths(wide), []int{43, 75, 8, 9, 9, 8, 74}) {
		t.Fatalf("weighted surplus = %v, fits %v", columnWidths(wide), fits)
	}
	for _, col := range base {
		if col.Width < trellis.ContentWidth(col.Title)+1 {
			t.Fatalf("readable header %q touches its border", col.Title)
		}
	}
	if policies[colRepo].Weight != 1 || policies[colBranch].Weight != 2 || policies[colPath].Weight != 2 {
		t.Fatal("stretch weights must be 1:2:2")
	}
	for _, col := range []int{colRepo, colBranch} {
		if policies[col].ShrinkPriority >= policies[colPath].ShrinkPriority {
			t.Fatal("Repo and Branch must have higher content priority than Path")
		}
	}
}

func TestWorktreeColumnsHardFloorBoundaries(t *testing.T) {
	policies := worktreeColumnPolicies(nil, "", time.Now(), nil)
	for _, tc := range []struct {
		width int
		fits  bool
		want  []int
	}{
		{61, false, []int{8, 8, 6, 6, 8, 4, 8}},
		{62, true, []int{8, 8, 6, 6, 8, 4, 8}},
		{63, true, []int{8, 8, 7, 6, 8, 4, 8}},
	} {
		t.Run(strconv.Itoa(tc.width), func(t *testing.T) {
			cols, fits := worktreeColumns(tc.width, policies, trellis.Preferences{})
			if fits != tc.fits || !reflect.DeepEqual(columnWidths(cols), tc.want) {
				t.Fatalf("widths = %v, fits = %v, want %v, %v", columnWidths(cols), fits, tc.want, tc.fits)
			}
			if got := tableWidth(cols); got != max(tc.width, 62) {
				t.Fatalf("padded width = %d, want %d", got, max(tc.width, 62))
			}
		})
	}
	if got := columnMinWidths(); !reflect.DeepEqual(got, []int{8, 8, 6, 6, 8, 4, 8}) {
		t.Fatalf("drag floors = %v", got)
	}
}

func TestWorktreePoliciesMeasurePlainUnicodeLabelsAndShortenedPaths(t *testing.T) {
	w := wtEntry("/home/alex/worktrees/界界/"+strings.Repeat("e\u0301", 12), strings.Repeat("界", 10), 0)
	w.Owner, w.Repo = strings.Repeat("界", 8), strings.Repeat("e\u0301", 7)
	w.Mismatch = true
	entries := []worktree.Entry{w, w}
	rows := buildWorktreeRows(entries, 1, "/home/alex", 0, time.Now(), nil, "", "")
	if rows[1][colRepo] != "" {
		t.Fatal("fixture must include a blank repeated group label")
	}
	policies := worktreeColumnPolicies(entries, "/home/alex", time.Now(), nil)
	for _, tc := range []struct {
		col   int
		label string
		want  int
	}{
		{colRepo, repoLabel(w), 24},
		{colBranch, branchLabel(w), 28},
		{colPath, shortenHome(w.Path, "/home/alex"), 29},
	} {
		if got := runewidth.StringWidth(tc.label); got != tc.want {
			t.Fatalf("fixture %q width = %d, want %d", tc.label, got, tc.want)
		}
		if got := policies[tc.col].Preferred; got != tc.want {
			t.Fatalf("column %d preferred = %d, want %d for %q", tc.col, got, tc.want, tc.label)
		}
	}
	cols, fits := worktreeColumns(240, policies, trellis.Preferences{})
	if !fits {
		t.Fatal("wide Unicode layout must fit")
	}
	for _, col := range []int{colRepo, colBranch, colPath} {
		if cols[col].Width < policies[col].Preferred {
			t.Fatalf("column %d width %d truncates funded content %d", col, cols[col].Width, policies[col].Preferred)
		}
	}
	w.Owner = "\x1b[31m" + w.Owner + "\x1b[0m" + loam.Sentinel
	w.Branch = "\x1b[32m" + w.Branch + "\x1b[0m" + loam.Sentinel
	decorated := worktreeColumnPolicies([]worktree.Entry{w}, "/home/alex", time.Now(), nil)
	if decorated[colRepo].Preferred != 24 || decorated[colBranch].Preferred != 28 {
		t.Fatalf("styling or cursor marker changed content targets: %v", decorated)
	}
}

func TestSizingUsesVisibleRowsBeforeFilterAndMeasuresGroupedLabels(t *testing.T) {
	useTempCache(t)
	m := New(999, false)
	m.width, m.height, m.home = 240, 40, "/home/alex"
	short := wtEntry("/w/short", "short", 0)
	long := layoutFixture()
	repeated := long
	repeated.Path = "/w/repeated"
	hidden := wtEntry("/w/main", strings.Repeat("hidden", 60), 0)
	hidden.IsMain = true
	m.applyWorktrees([]worktree.Entry{short, long, repeated, hidden})
	want := worktreeColumnPolicies(m.visibleWorktrees(), m.home, time.Now(), m.vscode)
	if want[colBranch].Preferred != len(long.Branch) {
		t.Fatal("hidden main content must not affect automatic sizing")
	}
	before := columnWidths(m.table.Columns())
	for _, query := range []string{"short", "nothing-matches", ""} {
		previous := m.selectedPath()
		m.filterQuery = query
		m.redisplay(previous)
		if !reflect.DeepEqual(columnWidths(m.table.Columns()), before) {
			t.Fatalf("filter %q moved geometry", query)
		}
	}
	m.showMain = true
	m.redisplay(m.selectedPath())
	if m.table.Columns()[colBranch].Width <= before[colBranch] {
		t.Fatal("show-main must add its branch to automatic content targets")
	}
}

func TestWorktreePoliciesFitRealCompactContentWithoutPlaceholderOrTags(t *testing.T) {
	now := time.Now()
	w := wtEntry("/w/a", "a", time.Hour)
	w.CreatedTime = now.Add(-23*time.Hour - 59*time.Minute)
	w.ParkedAt = now
	w.MergeStatus = "needs-review"
	policies := worktreeColumnPolicies([]worktree.Entry{w}, "", now, map[string]vscodeState{w.Path: vscodeOpen})
	if got := policies[colCreated].Preferred; got != 8 {
		t.Fatalf("Created preferred = %d, want readable default 8", got)
	}
	if got := policies[colWorktree].HardMinimum; got != len("parked") {
		t.Fatalf("Worktree hard floor = %d, want 6 for parked", got)
	}
	if got := policies[colMerge].Preferred; got != len("needs-review") {
		t.Fatalf("Merge preferred = %d, want full current content", got)
	}
	cols, fits := worktreeColumns(180, policies, trellis.Preferences{})
	if !fits || cols[colMerge].Width != len("needs-review") {
		t.Fatalf("compact Merge content not funded: %v, fits %v", columnWidths(cols), fits)
	}
	for _, col := range []int{colCreated, colWorktree, colVSCode} {
		if cols[col].Width != policies[col].Minimum {
			t.Fatalf("compact column %d stretched to %d", col, cols[col].Width)
		}
	}
	m := New(999, false)
	m.width = 180
	m.worktrees = nil
	m.resize()
	before := columnWidths(m.table.Columns())
	m.pollsLanded, m.pollFailures = 1, 20
	m.filterQuery = strings.Repeat("long-no-match-query", 20)
	m.resize()
	if got := columnWidths(m.table.Columns()); !reflect.DeepEqual(got, before) {
		t.Fatalf("placeholder changed widths: got %v, want %v", got, before)
	}
}

func sendMouse(m Model, x, y int, action tea.MouseAction, button tea.MouseButton) Model {
	updated, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: action, Button: button})
	return updated.(Model)
}

func sendWindowSize(m Model, width, height int) Model {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func dragColumn(m Model, col, delta int) Model {
	_, y := m.renderHeader()
	off := loam.ColumnOffsets(m.table.Columns())[col]
	x := off.Start + off.Width
	m = sendMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft)
	return sendMouse(m, x+delta, y, tea.MouseActionMotion, tea.MouseButtonLeft)
}

func TestPollRefitsAutomaticLayoutAndTruncatesManualLayout(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(strconv.FormatBool(manual), func(t *testing.T) {
			useTempCache(t)
			m := New(999, false)
			m.width, m.height = 240, 40
			m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
			if manual {
				m = dragColumn(m, colRepo, 4)
				m = sendMouse(m, 0, 0, tea.MouseActionRelease, tea.MouseButtonLeft)
			}
			before := columnWidths(m.table.Columns())
			long := wtEntry("/w/a", strings.Repeat("branch", 12), 0)
			long.Owner, long.Repo = "hellofresh", strings.Repeat("r", 40)
			updated, _ := m.Update(pollResultMsg{results: []worktree.RepoResult{{Entries: []worktree.Entry{long}}}})
			m = updated.(Model)
			if m.preferences.Manual() != manual {
				t.Fatalf("manual preference = %v, want %v", m.preferences.Manual(), manual)
			}
			if manual {
				if got := columnWidths(m.table.Columns()); !reflect.DeepEqual(got, before) {
					t.Fatalf("manual poll moved columns: got %v, want %v", got, before)
				}
				if m.table.Columns()[colBranch].Width >= trellis.ContentWidth(branchLabel(long)) {
					t.Fatal("fixture must force manual truncation of the new branch")
				}
				if !strings.Contains(ansi.Strip(m.table.View()), "…") {
					t.Fatal("manual layout must ellipsize longer content")
				}
			} else {
				for _, col := range []int{colRepo, colBranch} {
					if got := m.table.Columns()[col].Width; got <= before[col] {
						t.Fatalf("automatic column %d did not refit: %d <= %d", col, got, before[col])
					}
				}
			}
			if got := tableWidth(m.table.Columns()); got != 240 {
				t.Fatalf("poll table width = %d, want 240", got)
			}
		})
	}
}

func TestManualLayoutWideNarrowWideHasNoDrift(t *testing.T) {
	m := New(999, false)
	m.width, m.height = 240, 40
	m.applyWorktrees([]worktree.Entry{layoutFixture()})
	m = dragColumn(m, colRepo, 7)
	m = sendMouse(m, 0, 0, tea.MouseActionRelease, tea.MouseButtonLeft)
	wide := columnWidths(m.table.Columns())
	for cycle := 0; cycle < 4; cycle++ {
		for _, width := range []int{180, 140, 120, 110, 100, 63, 62, 61, 240} {
			m = sendWindowSize(m, width, 40)
			if !m.preferences.Manual() {
				t.Fatal("resize lost manual preferences")
			}
			if got := tableWidth(m.table.Columns()); got != max(width, 62) {
				t.Fatalf("cycle %d, viewport %d: padded width = %d", cycle, width, got)
			}
			for i, col := range m.table.Columns() {
				if col.Width < columnMinWidths()[i] {
					t.Fatalf("viewport %d: column %d below hard floor", width, i)
				}
			}
		}
		if got := columnWidths(m.table.Columns()); !reflect.DeepEqual(got, wide) {
			t.Fatalf("cycle %d drifted: got %v, want %v", cycle, got, wide)
		}
	}
}

func TestActiveDragFreezesPollGeometryAndRefitsOnceAtGestureEnd(t *testing.T) {
	for _, changed := range []bool{false, true} {
		for _, end := range []tea.MouseAction{tea.MouseActionRelease, tea.MouseActionMotion} {
			t.Run(strconv.FormatBool(changed)+"/"+strconv.Itoa(int(end)), func(t *testing.T) {
				m := New(999, false)
				m.width, m.height = 240, 40
				m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
				delta := 0
				if changed {
					delta = 4
				}
				m = dragColumn(m, colRepo, delta)
				frozen := columnWidths(m.table.Columns())
				long := wtEntry("/w/a", strings.Repeat("branch", 12), 0)
				long.MergeStatus = "needs-review"
				m.applyWorktrees([]worktree.Entry{long})
				if !m.resizer.Dragging() || !reflect.DeepEqual(columnWidths(m.table.Columns()), frozen) {
					t.Fatalf("poll moved live drag geometry: got %v, want %v", columnWidths(m.table.Columns()), frozen)
				}
				if got := m.table.Rows()[0][colBranch]; got != long.Branch {
					t.Fatalf("poll did not update rows during drag: %q", got)
				}
				m = sendMouse(m, 0, 0, end, tea.MouseButtonNone)
				if m.resizer.Dragging() {
					t.Fatal("gesture end left drag active")
				}
				if m.preferences.Manual() != changed {
					t.Fatal("only changed motion may select a manual layout")
				}
				if m.table.Columns()[colMerge].Width != len(long.MergeStatus) {
					t.Fatal("gesture end did not fund new compact content")
				}
				if !changed && m.table.Columns()[colBranch].Width <= frozen[colBranch] {
					t.Fatal("automatic gesture end did not fund new branch content")
				}
				after := columnWidths(m.table.Columns())
				m = sendMouse(m, 0, 0, end, tea.MouseButtonNone)
				if !reflect.DeepEqual(columnWidths(m.table.Columns()), after) {
					t.Fatal("repeated gesture end moved geometry")
				}
			})
		}
	}
}

func TestWindowSizeOnlyCancelsDragForAChangedWidth(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(strconv.FormatBool(manual), func(t *testing.T) {
			m := New(999, false)
			m.width, m.height = 180, 40
			m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
			delta := 0
			if manual {
				delta = 5
			}
			m = dragColumn(m, colRepo, delta)
			before := columnWidths(m.table.Columns())
			rows := append([]table.Row(nil), m.table.Rows()...)
			for _, height := range []int{40, 55} {
				m = sendWindowSize(m, 180, height)
				if !m.resizer.Dragging() || !reflect.DeepEqual(columnWidths(m.table.Columns()), before) {
					t.Fatal("same-width message changed columns or canceled the gesture")
				}
				if !reflect.DeepEqual(m.table.Rows(), rows) || m.table.Height() != height-7 {
					t.Fatal("same-width message must update height only")
				}
			}
			m = sendWindowSize(m, 120, 55)
			if m.resizer.Dragging() || m.resizer.DragColumn() != -1 {
				t.Fatal("changed width must cancel before projecting geometry")
			}
			if m.preferences.Manual() != manual {
				t.Fatal("changed width altered saved preferences")
			}
			projected := columnWidths(m.table.Columns())
			m = sendMouse(m, 90, 3, tea.MouseActionMotion, tea.MouseButtonLeft)
			if !reflect.DeepEqual(columnWidths(m.table.Columns()), projected) {
				t.Fatal("stale motion after width change resumed the canceled gesture")
			}
		})
	}
}

func TestTooNarrowNoticeMatchesHeaderOriginAndRecovers(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	for _, width := range []int{61, 62, 63, 180, 61} {
		m = sendWindowSize(m, width, 40)
		header, y := m.renderHeader()
		if got, want := strings.Contains(ansi.Strip(header), "terminal too narrow"), width < 62; got != want {
			t.Fatalf("viewport %d: warning = %v, want %v", width, got, want)
		}
		if y != len(strings.Split(header, "\n"))+1 {
			t.Fatalf("viewport %d: wrong header origin %d", width, y)
		}
		line := strings.Split(m.View(), "\n")[y]
		if !strings.Contains(line, "Repo") || !strings.Contains(line, "Path") {
			t.Fatalf("viewport %d: origin points to %q, not table header", width, line)
		}
		wantHeight := 33
		if width < 62 {
			wantHeight--
		}
		if m.table.Height() != wantHeight {
			t.Fatalf("viewport %d: height = %d, want %d", width, m.table.Height(), wantHeight)
		}
	}
}

func TestNoOpGestureKeepsAutomaticLayout(t *testing.T) {
	m := New(999, false)
	m = sendWindowSize(m, 62, 40)
	_, y := m.renderHeader()
	off := loam.ColumnOffsets(m.table.Columns())[colRepo]
	x := off.Start + off.Width
	m = sendMouse(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = sendMouse(m, x, y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = sendMouse(m, x+20, y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = sendMouse(m, x+20, y, tea.MouseActionRelease, tea.MouseButtonLeft)
	if m.preferences.Manual() {
		t.Fatal("zero-delta or fully clamped motion captured preferences")
	}
}

func TestModalOpeningSettlesExistingDrag(t *testing.T) {
	for _, key := range []string{"?", "x", "p", "P", "M"} {
		t.Run(key, func(t *testing.T) {
			m := New(999, false)
			m.width, m.height = 180, 40
			w := wtEntry("/w/a", "a", time.Hour)
			w.Dirty = true
			w.Stale = key == "P"
			w.MergeStatus = "merged"
			m.applyWorktrees([]worktree.Entry{w})
			m = dragColumn(m, colRepo, 0)
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			m = updated.(Model)
			if !m.helpOpen && !m.confirm.Active() {
				t.Fatal("fixture did not open modal")
			}
			if m.resizer.Dragging() {
				t.Fatal("modal left gesture active")
			}
			m = sendMouse(m, 0, 0, tea.MouseActionRelease, tea.MouseButtonLeft)
			updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = updated.(Model)
			before := m.table.Columns()[colBranch].Width
			w.Branch = strings.Repeat("branch", 20)
			m.applyWorktrees([]worktree.Entry{w})
			if m.table.Columns()[colBranch].Width <= before {
				t.Fatal("modal left automatic sizing frozen")
			}
		})
	}
}

func TestParkedWordFitsAtTheWorktreeHardFloor(t *testing.T) {
	w := wtEntry("/w/a", "a", time.Hour)
	w.ParkedAt = time.Now()
	m := New(999, false)
	m.worktrees = []worktree.Entry{w}
	m = sendWindowSize(m, 62, 40)
	if got := m.table.Columns()[colWorktree].Width; got != 6 {
		t.Fatalf("Worktree width = %d, want parked floor 6", got)
	}
	if !strings.Contains(ansi.Strip(m.table.View()), "parked") {
		t.Fatalf("parked truncated at hard floor: %q", m.table.View())
	}
}
