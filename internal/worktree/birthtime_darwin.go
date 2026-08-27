//go:build darwin

package worktree

import (
	"os"
	"syscall"
	"time"
)

// dirBirthTime returns path's filesystem birth time (creation time), or
// ok=false if path can't be stat'd (e.g. a Stale worktree whose
// directory is already gone). macOS's filesystems (APFS, HFS+) both
// track birth time natively and expose it via syscall.Stat_t's
// Birthtimespec field, which is why this is darwin-only rather than a
// portable implementation: understory itself is scoped to "same
// machine, same user" (see README's Limitations), and that machine is
// macOS. See birthtime_other.go for the fallback on any other GOOS.
func dirBirthTime(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(stat.Birthtimespec.Sec, stat.Birthtimespec.Nsec), true
}
