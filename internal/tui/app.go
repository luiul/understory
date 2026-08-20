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
// post-start hook and coppice already give a freshly created one.
package tui

import (
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luiul/understory/internal/vscode"
	"github.com/luiul/understory/internal/worktree"
)

// DefaultInterval is the poll interval used when none is given. `wt list`
// runs several git subprocesses per worktree it reports on, so this is
// deliberately slow: worktree state doesn't change anywhere near as often
// as, say, an agent's CPU usage.
const DefaultInterval = 15 * time.Second

const notifyDuration = 4 * time.Second

// cursorMarker is the plain-text glyph shown in the leftmost column of
// the currently selected row. It replaces bubbles/table's own Selected
// style (a whole-row background/foreground highlight), kept plain ASCII
// so buildWorktreeRows never has to reason about display width.
const cursorMarker = ">"

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
type pollResultMsg struct{ worktrees []worktree.Entry }
type openResultMsg struct{ result vscode.Result }
type clearNotifyMsg struct{ token int }

// Model is the bubbletea model backing the dashboard.
type Model struct {
	interval time.Duration
	home     string

	worktrees []worktree.Entry // every known worktree, raw (unsorted) from the last successful poll
	cursor    int              // remembers selection by-path across polls; table.Cursor() is the live ground truth while running

	table table.Model

	notification  string
	notifyIsError bool
	notifyToken   int

	width, height int
	quitting      bool
}

// New builds the dashboard model, polling at interval.
func New(interval time.Duration) Model {
	t := table.New(
		table.WithColumns(worktreeColumns(0)),
		table.WithFocused(true),
		table.WithHeight(15),
	)
	styles := table.DefaultStyles()
	styles.Selected = lipgloss.NewStyle()
	t.SetStyles(styles)

	return Model{
		interval: interval,
		home:     homeDir(),
		table:    t,
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
		return pollResultMsg{worktrees: worktree.ListAll(worktree.KnownRepoPaths())}
	}
}

func openCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return openResultMsg{result: vscode.Open(path)}
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
		m.table.SetWidth(msg.Width)
		m.table.SetHeight(clampInt(msg.Height-6, 3, 1000))
		m.resize()
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
	m.table.SetColumns(worktreeColumns(m.width))
	m.table.SetRows(buildWorktreeRows(m.displayedWorktrees(), cursor, m.home, time.Now()))
	m.table.SetCursor(cursor)
}

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	header := titleStyle.Render("understory") + subtleStyle.Render(" — worktrees on this machine")
	if summary := worktreeSummaryLine(m.worktrees); summary != "" {
		header += "\n" + summary
	}

	footer := subtleStyle.Render("↑/↓ move · enter open/focus · r refresh · q quit")
	if m.notification != "" {
		style := okStyle
		if m.notifyIsError {
			style = errorStyle
		}
		footer = style.Render(m.notification)
	}

	return header + "\n\n" + m.table.View() + "\n\n" + footer + "\n"
}

// Run starts the dashboard program and blocks until the user quits.
func Run(interval time.Duration) error {
	p := tea.NewProgram(New(interval), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
