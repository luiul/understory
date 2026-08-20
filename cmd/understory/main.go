// Command understory is an interactive dashboard for every git worktree
// of every repo it knows about (see internal/worktree): most recently
// committed first, with Enter opening or focusing a VS Code window on
// whichever one is selected.
//
// understory reads the same shared `~/.cache/wt/known-repos` registry
// `wt` (worktrunk) and coppice already populate, read only: it never
// writes to it, and never creates, switches, or removes a worktree
// itself. For that, see https://worktrunk.dev and
// https://github.com/luiul/coppice.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/luiul/understory/internal/tui"
)

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=1.2.3"
var version = "0.1.0-dev"

const helpText = `understory: interactive dashboard for every git worktree of every repo
wt/coppice know about, most recently committed first.

Arrow keys to move, Enter to open or focus a VS Code window on the
selected worktree, q to quit, r to refresh.

Usage:
  understory [flags]

Flags:
  --interval <seconds>   Poll interval in seconds (default 15).
  --no-color             Disable color output (also respects NO_COLOR).
  --version              Show the version and exit.
  -h, --help             Show this help and exit.
`

func main() {
	fs := flag.NewFlagSet("understory", flag.ExitOnError)
	interval := fs.Float64("interval", tui.DefaultInterval.Seconds(), "Poll interval in seconds.")
	noColor := fs.Bool("no-color", false, "Disable color output (also respects NO_COLOR).")
	showVersion := fs.Bool("version", false, "Show the version and exit.")
	fs.Usage = func() { fmt.Fprint(os.Stderr, helpText) }

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if *showVersion {
		fmt.Printf("understory %s\n", version)
		return
	}

	if *interval <= 0 {
		fmt.Fprintln(os.Stderr, "understory: --interval must be positive")
		os.Exit(2)
	}

	if *noColor || os.Getenv("NO_COLOR") != "" {
		lipgloss.SetColorProfile(termenv.Ascii)
	}

	err := tui.Run(time.Duration(*interval * float64(time.Second)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "understory:", err)
		os.Exit(1)
	}
}
