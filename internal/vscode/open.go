// Package vscode opens, or focuses if a window is already open on a given
// path, a VS Code window there. Extracted from canopy's internal/jump
// (the same helper canopy's own Agents view uses for a VS Code integrated
// terminal), since understory needs the exact same open-or-focus behavior
// for a worktree row with no agent connection of its own to jump to:
// Enter on a worktree always ends up here.
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
	}
}

// Open opens, or focuses if a window is already open on path, a VS Code
// window there, using the real OS. Uses `--reuse-window`, not
// `-n`/`--new-window`: this is called on every Enter press on the same
// row, and `-n` would stack up a duplicate window each time instead of
// reusing the one already there.
func Open(path string) Result {
	return open(defaultDeps(), path)
}

func open(d deps, path string) Result {
	if path == "" {
		return Result{false, "No known path to open."}
	}

	if codeBin, ok := d.lookPathCode(); ok {
		if exitOK, _ := d.runCommand([]string{codeBin, "--reuse-window", path}); exitOK {
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
