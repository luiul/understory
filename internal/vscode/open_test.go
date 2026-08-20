package vscode

import "testing"

func TestOpenUsesTheCodeCLIWhenAvailable(t *testing.T) {
	var gotArgs []string
	d := defaultDeps()
	d.lookPathCode = func() (string, bool) { return "/usr/local/bin/code", true }
	d.runCommand = func(args []string) (bool, string) { gotArgs = args; return true, "" }

	result := open(d, "/Users/x/worktrees/feature")

	if !result.OK {
		t.Fatalf("want ok, got %+v", result)
	}
	want := []string{"/usr/local/bin/code", "--reuse-window", "/Users/x/worktrees/feature"}
	if len(gotArgs) != len(want) {
		t.Fatalf("got %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("got %v, want %v", gotArgs, want)
		}
	}
}

func TestOpenUsesReuseWindowNotNewWindow(t *testing.T) {
	// Enter gets pressed repeatedly on the same row; -n/--new-window would
	// stack up a duplicate window on every press instead of reusing (or
	// opening once and thereafter focusing) the same one.
	d := defaultDeps()
	d.lookPathCode = func() (string, bool) { return "code", true }
	var gotArgs []string
	d.runCommand = func(args []string) (bool, string) { gotArgs = args; return true, "" }

	open(d, "/x")

	for _, a := range gotArgs {
		if a == "-n" || a == "--new-window" {
			t.Fatalf("got args %v, want no -n/--new-window flag", gotArgs)
		}
	}
	found := false
	for _, a := range gotArgs {
		if a == "--reuse-window" {
			found = true
		}
	}
	if !found {
		t.Fatalf("got args %v, want --reuse-window", gotArgs)
	}
}

func TestOpenFallsBackToOpenWhenCodeCLIMissing(t *testing.T) {
	d := defaultDeps()
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
	result := open(defaultDeps(), "")
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
