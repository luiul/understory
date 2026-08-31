// Package tui is understory's interactive dashboard: every worktree of
// every repo it knows about (see internal/worktree), most recently
// committed first, with open-or-focus-a-VS-Code-window on Enter and
// confirmed removal (x/X/P/M, see actions.go) delegating the actual
// work to `wt remove`/git, the same commands coppice's own `remove`
// wraps.
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

	"github.com/luiul/dashkit/confirm"
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
	// promptStyle renders a pending confirmation's prompt: yellow for the
	// ordinary kinds, while errorStyle's louder red marks confirmForceOne
	// (the one that discards uncommitted work and deletes unmerged
	// branches). See footerView.
	promptStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
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
	// resulting width of the two columns each drag has actually moved (a
	// drag always moves two adjacent columns at once; see trellis.Model.
	// Handle's own doc), by column index (see the Column indexes in
	// worktrees.go). Only the dragged pair is ever recorded: recording
	// every column's current width would pin columns the user never
	// touched, freezing Repo/Branch's grow-to-fit sizing the moment any
	// single drag happens. worktreeColumns applies these absolutely (see
	// its own doc — a drag is a deliberate pin, in both directions) every
	// time columns are rebuilt, so a fresh poll doesn't silently discard
	// an earlier resize. Cleared whenever a WindowSizeMsg arrives (see
	// Update): a genuinely new terminal width invalidates the old
	// distribution of space entirely, so resize starts fresh rather than
	// fighting stale overrides sized for a different width. Path never
	// carries an override that worktreeColumns applies — same as canopy's
	// own Location, it always absorbs whatever's left over after every
	// other column's own effective width is accounted for.
	colOverrides map[int]int
	resizer      trellis.Model

	notification  string
	notifyIsError bool
	notifyToken   int

	// confirm is the pending confirmation modal's state machine (see
	// github.com/luiul/dashkit/confirm): the armed prompt's payload (a
	// confirmState, nil when closed) plus the auto-cancel token
	// discipline. helpOpen swaps the table for the keybinding overlay
	// (?).
	confirm  confirm.State[confirmState]
	helpOpen bool

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

// openVSCode is a package-level seam onto mycelium.OpenVSCode, swapped
// out in tests so app_test.go can verify the selected row's path and
// branch are threaded through without shelling out to osascript or the
// real `code` CLI; mycelium's own test suite covers the window-detection
// logic itself in depth.
var openVSCode = mycelium.OpenVSCode

func openCmd(path, branch string) tea.Cmd {
	return func() tea.Msg {
		return openResultMsg{result: openVSCode(path, branch)}
	}
}

func clearNotifyCmd(token int) tea.Cmd {
	return tea.Tick(notifyDuration, func(time.Time) tea.Msg { return clearNotifyMsg{token: token} })
}

// notify sets the status-line notification and returns the command that
// clears it after notifyDuration, invalidated if a newer notification
// replaces it first (the token pattern, see clearNotifyMsg).
func (m *Model) notify(text string, isErr bool) tea.Cmd {
	m.notification = text
	m.notifyIsError = isErr
	m.notifyToken++
	return clearNotifyCmd(m.notifyToken)
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
		// No column drags while a modal is up: the confirmation prompt
		// owns the footer and the help overlay replaces the table, so a
		// drag's target row isn't even on screen.
		if m.helpOpen || m.confirm.Active() {
			return m, nil
		}
		_, originY := m.renderHeader()
		cols := m.table.Columns()
		widths, changed := m.resizer.Handle(msg, cols, columnMinWidths(), 0, originY)
		if changed {
			if m.colOverrides == nil {
				m.colOverrides = map[int]int{}
			}
			// A drag always moves the dragged column and its right-hand
			// neighbor together (see trellis.Model.Handle's own doc), so
			// both of their new widths need remembering — and no others:
			// recording every column's width would pin columns this drag
			// never touched (see colOverrides' own doc).
			dragged := m.resizer.DragColumn()
			m.colOverrides[dragged] = widths[dragged]
			m.colOverrides[dragged+1] = widths[dragged+1]
			m.table.SetColumns(trellis.Apply(cols, widths))
		}
		return m, nil

	case tea.KeyMsg:
		// The help overlay carries no actions of its own: any key closes
		// it (ctrl+c excepted, which always quits).
		if m.helpOpen {
			if msg.String() == "ctrl+c" {
				m.quitting = true
				return m, tea.Quit
			}
			m.helpOpen = false
			return m, nil
		}
		// The confirmation prompt is modal; the answer discipline
		// itself (y confirms, n/esc/enter cancel, everything else
		// swallowed, ctrl+c quits) lives in dashkit's confirm package,
		// shared with canopy so the two can't drift apart.
		if m.confirm.Active() {
			switch confirm.Classify(msg) {
			case confirm.Confirm:
				c := m.confirm.Payload
				m.confirm.Resolve()
				return m, confirmedCmd(c)
			case confirm.Cancel:
				m.confirm.Resolve()
				return m, nil
			case confirm.Quit:
				m.quitting = true
				return m, tea.Quit
			default:
				return m, nil
			}
		}
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			return m, pollCmd()
		case "enter":
			return m, m.enterCmd()
		case "x":
			cmd := m.startConfirm(confirmRemoveOne)
			return m, cmd
		case "X":
			cmd := m.startConfirm(confirmForceOne)
			return m, cmd
		case "P":
			cmd := m.startConfirm(confirmPruneStale)
			return m, cmd
		case "M":
			cmd := m.startConfirm(confirmRemoveMerged)
			return m, cmd
		case "y":
			// Copy is y, vim's yank: c collided with canopy's dismiss
			// binding, and one key meaning two different things across
			// the two dashboards is the worst kind of inconsistency.
			if w, ok := m.selectedWorktree(); ok {
				return m, copyCmd(w.Path)
			}
			return m, nil
		case "m":
			previousPath := m.selectedPath()
			m.showMain = !m.showMain
			m.redisplay(previousPath)
			if m.showMain {
				return m, m.notify("showing main worktrees", false)
			}
			return m, m.notify("hiding main worktrees", false)
		case "?":
			m.helpOpen = true
			return m, nil
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
		return m, m.notify(msg.result.Message, !msg.result.OK)

	case removeResultMsg:
		text, isErr := removeSummary(msg.results)
		cmd := m.notify(text, isErr)
		// Refresh right away if anything actually got removed, so the row
		// disappears now instead of on the next tick (up to interval
		// away).
		for _, r := range msg.results {
			if r.Err == nil {
				return m, tea.Batch(cmd, pollCmd())
			}
		}
		return m, cmd

	case copyResultMsg:
		if msg.err != nil {
			return m, m.notify("copy failed: "+firstLine(msg.err.Error()), true)
		}
		return m, m.notify("copied "+shortenHome(msg.path, m.home), false)

	case confirm.Msg:
		if m.confirm.Tick(msg) {
			return m, m.notify(confirm.TimeoutText(), false)
		}
		return m, nil

	case clearNotifyMsg:
		if msg.token == m.notifyToken {
			m.notification = ""
		}
		return m, nil
	}
	return m, nil
}

// enterCmd opens or focuses a VS Code window on the selected row's path,
// passing the row's branch along with it: mycelium matches windows on
// rootName+branch together (every worktree of a repo shares the repo's
// leaf folder name, so the basename alone can't tell two of them apart)
// and can find a window open on a subpackage inside the worktree by the
// branch in its title even when no file is focused there — see
// mycelium.OpenVSCode's own doc.
func (m Model) enterCmd() tea.Cmd {
	w, ok := m.selectedWorktree()
	if !ok {
		return nil
	}
	return openCmd(w.Path, w.Branch)
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

// helpBindings is the ? overlay's content: every keybinding, including
// the inherited bubbles/table navigation set the one-line footer has no
// room for. The navigation entries, the mouse row, the close hint, and
// the title are deliberately identical to canopy's own overlay, and the
// rendering itself is loam.HelpView in both (the two dashboards share
// one set of conventions); only the action rows in the middle differ,
// being domain-specific.
var helpBindings = []loam.HelpBinding{
	{Key: "↑/↓, k/j", Desc: "move the selection"},
	{Key: "pgup/pgdn, b/f", Desc: "page up/down"},
	{Key: "u/d", Desc: "half page up/down"},
	{Key: "g/G, home/end", Desc: "jump to the top/bottom"},
	{Key: "enter", Desc: "open or focus a VS Code window on the worktree"},
	{Key: "x", Desc: "remove the selected worktree (asks first; a merged branch is deleted, others kept)"},
	{Key: "X", Desc: "force remove: discard uncommitted changes, delete the branch even if unmerged"},
	{Key: "P", Desc: "prune every stale worktree registration (asks first)"},
	{Key: "M", Desc: "remove every merged worktree of the selected repo (asks first)"},
	{Key: "y", Desc: "copy the worktree path to the clipboard"},
	{Key: "m", Desc: "show or hide each repo's main worktree"},
	{Key: "r", Desc: "refresh now"},
	{Key: "mouse", Desc: "drag a column border on the header row to resize the two columns it joins"},
	{Key: "?", Desc: "this help"},
	{Key: "q, ctrl+c", Desc: "quit"},
}

// helpView renders the ? overlay that replaces the table while
// helpOpen, delegating the actual layout to loam.HelpView (shared with
// canopy).
func (m Model) helpView() string {
	return loam.HelpView("keybindings", helpBindings, titleStyle, subtleStyle)
}

// footerView renders the bottom line: the confirmation prompt while one
// is pending (it's modal and swallows all other keys, so it replaces
// everything else), else the latest notification, else the default
// keybinding hints, kept to the essentials now that ? opens the full
// list.
func (m Model) footerView() string {
	if m.confirm.Active() {
		style := promptStyle
		if m.confirm.Payload.kind == confirmForceOne {
			style = errorStyle
		}
		return style.Render(m.confirmPrompt())
	}
	if m.notification != "" {
		style := okStyle
		if m.notifyIsError {
			style = errorStyle
		}
		return style.Render(m.notification)
	}
	if m.helpOpen {
		return subtleStyle.Render("press any key to close")
	}
	return subtleStyle.Render("↑/↓ move · enter open/focus · x remove · ? help · q quit")
}

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	header, _ := m.renderHeader()

	body := m.helpView()
	if !m.helpOpen {
		tableView := colorizeRows(m.table.View(), m.table.Columns(), colWorktree, colMerge)
		// Marks each column border on the header row with a visible divider
		// (see loam.DrawHeaderBorders' own doc) — otherwise the only cue for
		// where a mouse drag needs to land is bubbles/table's own blank
		// 2-space inter-cell gap, which doesn't look any different from the
		// padding inside a cell.
		body = loam.DrawHeaderBorders(tableView, m.table.Columns(), subtleStyle)
	}
	return header + "\n\n" + body + "\n\n" + m.footerView() + "\n"
}

// Run starts the dashboard program and blocks until the user quits.
// showMain controls whether each repo's main worktree is shown; see New.
func Run(interval time.Duration, showMain bool) error {
	p := tea.NewProgram(New(interval, showMain), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
