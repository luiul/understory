// Package tui is understory's interactive dashboard: every worktree of
// every repo it knows about (see internal/worktree), most recently
// committed first, with open-or-focus-a-VS-Code-window on Enter.
//
// This is deliberately a single view with no notion of "live" (an agent
// currently working inside a given worktree): that would require the
// same process-discovery machinery canopy already owns for its own
// Agents view, and duplicating it here would recouple two tools that are
// otherwise fully independent. Enter always opens or focuses a VS Code
// window on the selected worktree's path, the same behavior `wt`'s own
// post-start hook and coppice already give a freshly created one — via
// github.com/luiul/dashkit/mycelium's shared open-or-focus logic, since
// canopy needs the exact same switch-or-create behavior for its own
// agent rows.
package tui

import (
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luiul/dashkit/loam"
	"github.com/luiul/dashkit/mycelium"
	"github.com/luiul/dashkit/trellis"
	"github.com/luiul/understory/internal/worktree"
)

// DefaultInterval is the poll interval used when none is given. `wt list`
// runs several git subprocesses per worktree it reports on, so this is
// deliberately slow: worktree state doesn't change anywhere near as often
// as, say, an agent's CPU usage.
const DefaultInterval = 15 * time.Second

const notifyDuration = 4 * time.Second

// cursorSentinel tags whichever row is currently selected; see
// loam.Sentinel's doc for the mechanism and why it replaced a visible
// leading marker column/glyph. Aliased locally so the rest of this
// package (buildWorktreeRows in worktrees.go, this file's own tests)
// doesn't need to import loam just to reference the same constant
// colorizeRows (colorize.go) checks for.
const cursorSentinel = loam.Sentinel

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	subtleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errorStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
)

// homeDir is the current user's home directory, used to shorten the Path
// column to "~". "" (meaning: don't shorten) if it can't be determined.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// shortenHome replaces a leading home-directory prefix with "~", the same
// shorthand every shell prompt uses, so the Path column has more room
// left over for the part that actually varies row to row.
func shortenHome(path, home string) string {
	if home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}

type tickMsg struct{}
type pollResultMsg struct {
	worktrees []worktree.Entry
}
type openResultMsg struct{ result mycelium.Result }
type clearNotifyMsg struct{ token int }

// Model is the bubbletea model backing the dashboard.
type Model struct {
	interval time.Duration
	home     string
	// showMain, when false (the default), drops each repo's main worktree
	// (Entry.IsMain) from displayedWorktrees; see that method's doc for why.
	showMain bool

	worktrees []worktree.Entry // every known worktree, raw (unsorted) from the last successful poll
	cursor    int              // remembers selection by-path across polls; table.Cursor() is the live ground truth while running

	table table.Model

	// resizer tracks an in-progress mouse column-border drag (see
	// github.com/luiul/dashkit/trellis); colOverrides remembers the
	// resulting width of every column a drag has actually touched (a drag
	// always moves two adjacent columns at once; see trellis.Model.
	// Handle's own doc), by column index (see the Column indexes in
	// worktrees.go). worktreeColumns applies whichever of these are still
	// wider than that column's own natural floor (see its own doc — Repo/
	// Branch's floor moves with the data, so a stale override narrower
	// than a freshly polled, longer name is dropped rather than
	// truncating it) every time columns are rebuilt, so a fresh poll
	// doesn't silently discard an earlier resize. Cleared whenever a
	// WindowSizeMsg arrives (see Update): a genuinely new terminal width
	// invalidates the old distribution of space entirely, so resize
	// starts fresh rather than fighting stale overrides sized for a
	// different width. Path never carries an override at all — same as
	// canopy's own Location, it always absorbs whatever's left over after
	// every other column's own effective width is accounted for.
	colOverrides map[int]int
	resizer      trellis.Model

	notification  string
	notifyIsError bool
	notifyToken   int

	width, height int
	quitting      bool
}

// New builds the dashboard model, polling at interval. showMain controls
// whether each repo's main worktree (Entry.IsMain) is included in the
// view; see displayedWorktrees' doc.
func New(interval time.Duration, showMain bool) Model {
	t := table.New(
		table.WithColumns(worktreeColumns(0, nil, nil)),
		table.WithFocused(true),
		table.WithHeight(15),
	)
	styles := table.DefaultStyles()
	// Selected stays empty rather than bubbles/table's own default
	// highlight: the row highlight understory does show is applied by
	// colorizeRows (colorize.go) as a post-render pass instead, precisely
	// so it can layer on top of the Worktree/Merge status coloring rather
	// than replacing it; see that file's package doc for why.
	styles.Selected = lipgloss.NewStyle()
	t.SetStyles(styles)

	return Model{
		interval: interval,
		home:     homeDir(),
		showMain: showMain,
		table:    t,
		resizer:  trellis.New(),
	}
}

// Init kicks off the first poll and the recurring timer.
func (m Model) Init() tea.Cmd {
	return tea.Batch(pollCmd(), tickCmd(m.interval))
}

func tickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg { return tickMsg{} })
}

func pollCmd() tea.Cmd {
	return func() tea.Msg {
		entries := worktree.ListAll(worktree.KnownRepoPaths())
		return pollResultMsg{worktrees: entries}
	}
}

func openCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return openResultMsg{result: mycelium.OpenVSCode(path)}
	}
}

func clearNotifyCmd(token int) tea.Cmd {
	return tea.Tick(notifyDuration, func(time.Time) tea.Msg { return clearNotifyMsg{token: token} })
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// A new terminal width invalidates whatever distribution of space a
		// prior drag settled on — resize is about to recompute every column
		// from scratch against the new width, so any stale override is
		// dropped first rather than fighting that recompute.
		m.colOverrides = nil
		m.table.SetWidth(msg.Width)
		m.table.SetHeight(clampInt(msg.Height-6, 3, 1000))
		m.resize()
		return m, nil

	case tea.MouseMsg:
		_, originY := m.renderHeader()
		cols := m.table.Columns()
		widths, changed := m.resizer.Handle(msg, cols, columnMinWidths(m.displayedWorktrees()), 0, originY)
		if changed {
			if m.colOverrides == nil {
				m.colOverrides = map[int]int{}
			}
			// A drag always moves the dragged column and its right-hand
			// neighbor together (see trellis.Model.Handle's own doc), so
			// both of their new widths need remembering, not only the one
			// DragColumn() points at.
			for i, w := range widths {
				m.colOverrides[i] = w
			}
			m.table.SetColumns(trellis.Apply(cols, widths))
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			return m, pollCmd()
		case "enter":
			return m, m.enterCmd()
		default:
			var cmd tea.Cmd
			m.table, cmd = m.table.Update(msg)
			m.refreshCursorMarker()
			return m, cmd
		}

	case tickMsg:
		return m, tea.Batch(pollCmd(), tickCmd(m.interval))

	case pollResultMsg:
		m.applyWorktrees(msg.worktrees)
		return m, nil

	case openResultMsg:
		m.notification = msg.result.Message
		m.notifyIsError = !msg.result.OK
		m.notifyToken++
		return m, clearNotifyCmd(m.notifyToken)

	case clearNotifyMsg:
		if msg.token == m.notifyToken {
			m.notification = ""
		}
		return m, nil
	}
	return m, nil
}

// enterCmd opens or focuses a VS Code window on the selected row's path.
func (m Model) enterCmd() tea.Cmd {
	w, ok := m.selectedWorktree()
	if !ok {
		return nil
	}
	return openCmd(w.Path)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampCursor clamps idx into [0, n-1], or 0 if n is 0 (no rows to select
// at all, e.g. only the placeholder row is showing).
func clampCursor(idx, n int) int {
	if n == 0 {
		return 0
	}
	return clampInt(idx, 0, n-1)
}

// refreshCursorMarker copies table.Model's own cursor (after arrow keys,
// page up/down, etc. moved it) back into m.cursor and rebuilds the
// cursor-marker column so the arrow follows immediately rather than
// waiting for the next poll.
func (m *Model) refreshCursorMarker() {
	m.cursor = clampCursor(m.table.Cursor(), len(m.displayedWorktrees()))
	m.table.SetRows(buildWorktreeRows(m.displayedWorktrees(), m.cursor, m.home, time.Now()))
}

// resize rebuilds columns (Path's width depends on m.width) and rows for
// the new terminal width, preserving whatever the live cursor currently
// is.
func (m *Model) resize() {
	cursor := clampCursor(m.table.Cursor(), len(m.displayedWorktrees()))
	m.cursor = cursor
	// Clear rows before changing columns: bubbles/table re-renders
	// immediately on both SetColumns and SetRows against whatever's
	// currently set, so swapping to a column count the old rows don't
	// match panics if the two are ever briefly out of sync mid-update.
	m.table.SetRows(nil)
	m.table.SetColumns(worktreeColumns(m.width, m.displayedWorktrees(), m.colOverrides))
	m.table.SetRows(buildWorktreeRows(m.displayedWorktrees(), cursor, m.home, time.Now()))
	m.table.SetCursor(cursor)
}

// renderHeader builds the header block (title, plus an optional summary
// line) and reports how many terminal rows precede the table's own
// header row: the header block's own line count, plus the blank
// separator line View always inserts before the table. View and the
// tea.MouseMsg case in Update both need exactly this — View to render
// the text, mouse handling to know whether a click landed on the
// table's own header row (see trellis.Model.Handle's doc) — so both call
// this one helper rather than keeping two copies of the same line-
// counting logic in sync by hand; the same split canopy's own
// internal/tui/app.go already uses.
func (m Model) renderHeader() (text string, tableOriginY int) {
	text = titleStyle.Render("understory") + subtleStyle.Render(" — worktrees on this machine")
	lines := 1
	if summary := worktreeSummaryLine(m.displayedWorktrees()); summary != "" {
		text += "\n" + summary
		lines++
	}
	return text, lines + 1 // +1 for the blank separator line View puts before the table
}

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	header, _ := m.renderHeader()

	footer := subtleStyle.Render("↑/↓ move · enter open/focus · drag column border to resize · r refresh · q quit")
	if m.notification != "" {
		style := okStyle
		if m.notifyIsError {
			style = errorStyle
		}
		footer = style.Render(m.notification)
	}

	tableView := colorizeRows(m.table.View(), m.table.Columns(), colWorktree, colMerge)
	// Marks each column border on the header row with a visible divider
	// (see loam.DrawHeaderBorders' own doc) — otherwise the only cue for
	// where a mouse drag needs to land is bubbles/table's own blank
	// 2-space inter-cell gap, which doesn't look any different from the
	// padding inside a cell.
	tableView = loam.DrawHeaderBorders(tableView, m.table.Columns(), subtleStyle)
	return header + "\n\n" + tableView + "\n\n" + footer + "\n"
}

// Run starts the dashboard program and blocks until the user quits.
// showMain controls whether each repo's main worktree is shown; see New.
func Run(interval time.Duration, showMain bool) error {
	p := tea.NewProgram(New(interval, showMain), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
