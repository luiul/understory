//go:build !darwin

package worktree

import "time"

// dirBirthTime always reports ok=false on non-darwin platforms: there's
// no portable stdlib way to read a directory's filesystem birth time
// (Linux's ext4/xfs need the statx syscall, which the plain syscall
// package doesn't expose), and understory itself is scoped to macOS (see
// README's Limitations) — see birthtime_darwin.go for the real
// implementation. Callers (applyCreatedTime) fall back to CommitTime
// instead.
func dirBirthTime(path string) (time.Time, bool) {
	return time.Time{}, false
}
