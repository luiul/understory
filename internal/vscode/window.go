// windowTitles and raiseWindow give Open a way to check whether a VS Code
// window is already open on a given path *before* invoking the `code`
// CLI at all: `code --reuse-window <path>` only reuses an existing window
// when one already has that exact folder open; whenever none does (a
// worktree row that's never been opened before, or one whose window has
// since been closed), it silently falls back to hijacking whichever
// window was last active instead of opening a fresh one — confirmed both
// empirically and in upstream reports (microsoft/vscode#121926,
// #216602, #215749). Detecting "already open" ourselves, via each
// window's title, and only ever calling `code` at all for the "not
// already open" half, avoids that fallback entirely: the CLI never gets
// a chance to guess wrong about which window to reuse.

package vscode

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const windowScriptTimeout = 6 * time.Second

// windowTitles lists every currently open VS Code window's title, via
// System Events. Returns an empty slice, no error, if VS Code isn't
// running at all: that's the ordinary "nothing to switch to" case, not a
// failure. A non-nil error means the AppleScript itself failed (e.g. the
// Automation permission for scripting VS Code hasn't been granted yet),
// which Open treats as "couldn't tell, fall back to --reuse-window"
// rather than as "definitely nothing open".
func windowTitles() ([]string, error) {
	out, err := runOsascript(`
if application "Visual Studio Code" is running then
	tell application "System Events"
		tell process "Code"
			get name of every window
		end tell
	end tell
else
	return ""
end if
`)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	// osascript joins an AppleScript list with ", " when coerced to text.
	titles := strings.Split(out, ", ")
	for i := range titles {
		titles[i] = strings.TrimSpace(titles[i])
	}
	return titles, nil
}

// raiseWindow brings the VS Code window with this exact title to the
// front, activating the app itself first (a window can't be raised above
// other apps' windows until its own app is). Returns false, no error, if
// no window with that title exists anymore (e.g. it was closed between
// windowTitles finding it and this call) — Open treats that the same as
// never having found it.
func raiseWindow(title string) (bool, error) {
	script := `
tell application "Visual Studio Code" to activate
tell application "System Events"
	tell process "Code"
		set matches to (every window whose name is "` + escapeForAppleScript(title) + `")
		if (count of matches) is 0 then
			return "false"
		end if
		perform action "AXRaise" of (item 1 of matches)
		return "true"
	end tell
end tell
`
	out, err := runOsascript(script)
	if err != nil {
		return false, err
	}
	return out == "true", nil
}

func runOsascript(script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), windowScriptTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return "", errors.New("VS Code didn't respond to AppleScript in time (Automation permission prompt not yet answered?)")
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func escapeForAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// matchWindowTitle finds the first title that's already showing path,
// going by this ecosystem's `window.title` convention (folder basename
// first, then a " " + separator + " ", e.g. "understory — main", or the
// plain basename on its own with nothing open in it yet). A title
// matches when it equals the basename exactly, or starts with the
// basename followed by a space: that's a real word boundary (so
// "understory-lab — main" does NOT match a search for "understory",
// since the character right after the shared prefix is "-", not a
// space), tolerant of whatever separator glyph sits between folder name
// and branch (em dash, plain hyphen, ...) without hardcoding one. Weak
// key, same class of limitation as canopy's own cwd-based Ghostty match:
// two different paths that happen to share a leaf folder name are
// indistinguishable by title alone.
// OpenPaths reports, for each of paths, whether a VS Code window is
// currently open on it (see matchWindowTitle's doc for the matching
// rule). Fetches the full window title list once via windowTitles
// regardless of how many paths are checked, the same call Open's own
// already-open check uses. Returns a nil map and the underlying error if
// the title check itself failed (e.g. the Automation permission for
// scripting VS Code hasn't been granted yet); callers should treat that
// as "unknown" rather than as "nothing is open" (see windowTitles' own
// doc on that distinction).
func OpenPaths(paths []string) (map[string]bool, error) {
	titles, err := windowTitles()
	if err != nil {
		return nil, err
	}
	return openPaths(titles, paths), nil
}

// openPaths is OpenPaths' pure matching logic, split out so it's testable
// against a fake title list without shelling out to osascript.
func openPaths(titles, paths []string) map[string]bool {
	open := make(map[string]bool, len(paths))
	for _, p := range paths {
		_, ok := matchWindowTitle(titles, p)
		open[p] = ok
	}
	return open
}

func matchWindowTitle(titles []string, path string) (string, bool) {
	base := filepath.Base(path)
	if base == "" {
		return "", false
	}
	for _, title := range titles {
		if title == base {
			return title, true
		}
		if strings.HasPrefix(title, base+" ") {
			return title, true
		}
	}
	return "", false
}
