// Package vscode opens, or focuses if a window is already open on a given
// path, a VS Code window there. Extracted from canopy's internal/jump
// (the same helper canopy's own Agents view uses for a VS Code integrated
// terminal), since understory needs the exact same open-or-focus behavior
// for a worktree row with no agent connection of its own to jump to:
// Enter on a worktree always ends up here.
//
// Unlike canopy's jump package, Open doesn't just hand `--reuse-window`
// to the `code` CLI and trust it to find the right window: see window.go
// for why that alone isn't enough to actually get switch-or-create
// behavior, and what checks the already-open case itself instead.
package vscode

import (
	"os/exec"
	"strings"
)

// Result reports whether opening/focusing succeeded and a human-readable
// message about it.
type Result struct {
	OK      bool
	Message string
}

// deps groups every external side effect Open makes, so tests can swap
// each one out without touching the real OS.
type deps struct {
	lookPathCode func() (string, bool)
	runCommand   func(args []string) (exitOK bool, stderr string)
	// windowTitles and raiseWindow implement the already-open check (see
	// window.go); split out so tests can fake "a window is/isn't already
	// open on this path" without shelling out to osascript.
	windowTitles func() ([]string, error)
	raiseWindow  func(title string) (bool, error)
}

func defaultDeps() deps {
	return deps{
		lookPathCode: func() (string, bool) {
			p, err := exec.LookPath("code")
			return p, err == nil
		},
		runCommand: func(args []string) (bool, string) {
			cmd := exec.Command(args[0], args[1:]...)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			err := cmd.Run()
			return err == nil, strings.TrimSpace(stderr.String())
		},
		windowTitles: windowTitles,
		raiseWindow:  raiseWindow,
	}
}

// Open opens, or focuses if a window is already open on path, a VS Code
// window there, using the real OS. Checks for an already-open window
// itself (see window.go) rather than leaning on `code --reuse-window`
// alone: that flag only reuses the right window when one already has
// path open, and silently hijacks whichever window was last active
// otherwise — exactly the case a worktree row that's never been opened
// before hits every time. Explicitly forces a new window (`-n`) instead
// once we know none is already open, which is safe against Enter being
// pressed repeatedly on the same row precisely because the already-open
// check above will find that new window on every subsequent press.
func Open(path string) Result {
	return open(defaultDeps(), path)
}

func open(d deps, path string) Result {
	if path == "" {
		return Result{false, "No known path to open."}
	}

	alreadyOpen, titlesErr := d.windowTitles()
	if titlesErr == nil {
		if title, ok := matchWindowTitle(alreadyOpen, path); ok {
			if raised, raiseErr := d.raiseWindow(title); raiseErr == nil && raised {
				return Result{true, "Focused VS Code window for " + path + "."}
			}
			// Window vanished between the check and the raise (closed in
			// the meantime) or raising it failed outright: fall through to
			// opening fresh, same as if it had never matched at all.
		}
	}

	if codeBin, ok := d.lookPathCode(); ok {
		// titlesErr != nil means the already-open check itself couldn't run
		// (VS Code scripting not permitted yet, most likely): fall back to
		// the CLI's own best-effort reuse instead of risking a duplicate
		// window on every press, same as before this file existed.
		flag := "-n"
		if titlesErr != nil {
			flag = "--reuse-window"
		}
		if exitOK, _ := d.runCommand([]string{codeBin, flag, path}); exitOK {
			if flag == "-n" {
				return Result{true, "Opened a new VS Code window for " + path + "."}
			}
			return Result{true, "Focused VS Code window for " + path + "."}
		}
	}

	// Fall back to just raising the app if the `code` shell command isn't
	// installed; this can't target the right *window*, only the app.
	exitOK, stderr := d.runCommand([]string{"open", "-a", "Visual Studio Code", path})
	if exitOK {
		return Result{true, "Opened " + path + " in VS Code (install the 'code' CLI for exact-window focus)."}
	}
	if stderr == "" {
		stderr = "Couldn't open VS Code."
	}
	return Result{false, stderr}
}
