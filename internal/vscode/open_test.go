package vscode

import (
	"errors"
	"testing"
)

// fakeDeps returns deps with every field faked to safe no-op defaults
// (no window already open, code CLI present, every command "succeeds"),
// so each test only needs to override the one or two fields it cares
// about instead of restating the whole struct.
func fakeDeps() deps {
	return deps{
		lookPathCode: func() (string, bool) { return "/usr/local/bin/code", true },
		runCommand:   func(args []string) (bool, string) { return true, "" },
		windowTitles: func() ([]string, error) { return nil, nil },
		raiseWindow:  func(title string) (bool, error) { return false, nil },
	}
}

func TestOpenForcesANewWindowWhenNoneIsAlreadyOpen(t *testing.T) {
	// The case a worktree row that's never been opened before always
	// hits: windowTitles finds nothing, so Open must force a genuinely
	// new window (-n) rather than handing --reuse-window to the CLI and
	// letting it fall back to hijacking some unrelated window.
	d := fakeDeps()
	var gotArgs []string
	d.runCommand = func(args []string) (bool, string) { gotArgs = args; return true, "" }

	result := open(d, "/Users/x/worktrees/feature")

	if !result.OK {
		t.Fatalf("want ok, got %+v", result)
	}
	want := []string{"/usr/local/bin/code", "-n", "/Users/x/worktrees/feature"}
	if len(gotArgs) != len(want) {
		t.Fatalf("got %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("got %v, want %v", gotArgs, want)
		}
	}
}

func TestOpenRaisesTheExistingWindowInsteadOfShellingOutToCode(t *testing.T) {
	// The switch-to-already-open half: when a window already has this
	// path's folder open, Open should raise it directly and never touch
	// the `code` CLI at all (that's the whole point of checking first).
	d := fakeDeps()
	d.windowTitles = func() ([]string, error) { return []string{"feature — main"}, nil }
	raisedTitle := ""
	d.raiseWindow = func(title string) (bool, error) { raisedTitle = title; return true, nil }
	codeCalled := false
	d.runCommand = func(args []string) (bool, string) { codeCalled = true; return true, "" }

	result := open(d, "/Users/x/worktrees/feature")

	if !result.OK {
		t.Fatalf("want ok, got %+v", result)
	}
	if raisedTitle != "feature — main" {
		t.Fatalf("got raised title %q, want %q", raisedTitle, "feature — main")
	}
	if codeCalled {
		t.Fatalf("want the code CLI never invoked once an existing window was raised")
	}
}

func TestOpenFallsThroughToANewWindowWhenTheMatchedWindowIsGone(t *testing.T) {
	// windowTitles can be stale: the matched window may have closed
	// between that check and the raise attempt. raiseWindow reporting
	// "not found" (false, nil) should fall through to opening fresh,
	// not report failure.
	d := fakeDeps()
	d.windowTitles = func() ([]string, error) { return []string{"feature — main"}, nil }
	d.raiseWindow = func(title string) (bool, error) { return false, nil }
	var gotArgs []string
	d.runCommand = func(args []string) (bool, string) { gotArgs = args; return true, "" }

	result := open(d, "/Users/x/worktrees/feature")

	if !result.OK {
		t.Fatalf("want ok, got %+v", result)
	}
	found := false
	for _, a := range gotArgs {
		if a == "-n" {
			found = true
		}
	}
	if !found {
		t.Fatalf("got args %v, want -n (forced new window)", gotArgs)
	}
}

func TestOpenFallsBackToReuseWindowWhenTheAlreadyOpenCheckItselfErrors(t *testing.T) {
	// windowTitles erroring (e.g. the Automation permission for
	// scripting VS Code hasn't been granted yet) means Open genuinely
	// doesn't know whether a window is already open. Falling back to
	// --reuse-window here, rather than unconditionally forcing -n, keeps
	// same-row repeated presses from stacking up duplicate windows for
	// anyone who hasn't granted that permission.
	d := fakeDeps()
	d.windowTitles = func() ([]string, error) { return nil, errors.New("not authorized") }
	var gotArgs []string
	d.runCommand = func(args []string) (bool, string) { gotArgs = args; return true, "" }

	open(d, "/x")

	found := false
	for _, a := range gotArgs {
		if a == "-n" {
			t.Fatalf("got args %v, want no -n when the already-open check errored", gotArgs)
		}
		if a == "--reuse-window" {
			found = true
		}
	}
	if !found {
		t.Fatalf("got args %v, want --reuse-window", gotArgs)
	}
}

func TestOpenFallsBackToOpenWhenCodeCLIMissing(t *testing.T) {
	d := fakeDeps()
	d.lookPathCode = func() (string, bool) { return "", false }
	var gotArgs []string
	d.runCommand = func(args []string) (bool, string) { gotArgs = args; return true, "" }

	result := open(d, "/Users/x/worktrees/feature")

	if !result.OK {
		t.Fatalf("want ok, got %+v", result)
	}
	want := []string{"open", "-a", "Visual Studio Code", "/Users/x/worktrees/feature"}
	if len(gotArgs) != len(want) {
		t.Fatalf("got %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("got %v, want %v", gotArgs, want)
		}
	}
}

func TestOpenWithoutAPathFailsClearly(t *testing.T) {
	result := open(fakeDeps(), "")
	if result.OK {
		t.Fatalf("want not ok, got %+v", result)
	}
	if !contains(result.Message, "path") {
		t.Fatalf("got message %q, want it to mention path", result.Message)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
