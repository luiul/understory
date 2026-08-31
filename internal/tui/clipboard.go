// The copy-path action (y, vim's yank): pipes the selected worktree's
// full path to pbcopy. macOS-only, same as the rest of understory's
// desktop integrations (mycelium's AppleScript window detection,
// dirBirthTime's birth-time syscall).
package tui

import (
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// copyText is a package-level seam onto pbcopy, swapped out in tests;
// same pattern as app.go's openVSCode seam.
var copyText = func(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

type copyResultMsg struct {
	path string
	err  error
}

// copyCmd copies path (the full, unshortened path: the clipboard is for
// pasting into terminals and other tools, where "~" wouldn't expand
// everywhere) and reports the outcome for the status line.
func copyCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return copyResultMsg{path: path, err: copyText(path)}
	}
}
