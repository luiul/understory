package tui

// The poll cache: understory's stale-while-revalidate startup (issue
// #8). The first poll after launch takes seconds to tens of seconds on
// a real registry (every `wt list` is a subprocess fan-out), which used
// to leave the table on "loading worktrees…" for the whole wait. With
// the last completed poll's merged view on disk, a warm start renders
// THAT immediately — marked "refreshing…" in the summary line until the
// first poll lands — and the streaming poll then reconciles repo by
// repo. A worktree removed since the last run lingers as a stale row
// for one poll at most, and removal prompts always revalidate against
// live polls, so a wrong cache never does harm; the first completed
// poll rewrites the file.
//
// Best-effort in both directions: an unreadable, corrupt, or
// wrong-version cache is just a cold start, never an error, and a
// failed save degrades the NEXT launch to a cold start, nothing about
// this one.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/luiul/understory/internal/worktree"
)

// cacheVersion is the on-disk schema version, bumped on any shape
// change: a mismatched file is ignored (a cold start) rather than
// parsed against the wrong layout.
const cacheVersion = 1

// pollCachePath is the package-level seam onto the cache file's
// location, swapped out in tests (which must never touch the real
// ~/.cache); the same pattern as the openVSCode seam.
var pollCachePath = defaultPollCachePath

// defaultPollCachePath is ~/.cache/understory/poll-cache.json: next to
// the shared wt registry's ~/.cache/wt/known-repos, deliberately NOT
// os.UserCacheDir (~/Library/Caches on macOS) — the convention here is
// the wt ecosystem's own.
func defaultPollCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "understory", "poll-cache.json")
}

// pollCache is one saved merged poll view: the entries exactly as the
// last completed poll left them (per-repo merged, last-known rows for
// failed repos included), plus the window-state maps, so a warm start
// renders the VS Code column's last-known states too instead of
// flashing "?" on every row for a poll.
type pollCache struct {
	Version      int                    `json:"version"`
	SavedAt      time.Time              `json:"savedAt"`
	Entries      []worktree.Entry       `json:"entries"`
	VSCode       map[string]vscodeState `json:"vscode"`
	VSCodeStrict map[string]bool        `json:"vscodeStrict"`
}

// loadPollCache reads the saved poll view, or returns nil on ANY
// problem (no path, missing file, unreadable, corrupt JSON, schema
// version mismatch): every failure mode is just a cold start.
func loadPollCache() *pollCache {
	if pollCachePath() == "" {
		return nil
	}
	data, err := os.ReadFile(pollCachePath())
	if err != nil {
		return nil
	}
	var c pollCache
	if err := json.Unmarshal(data, &c); err != nil || c.Version != cacheVersion {
		return nil
	}
	return &c
}

// savePollCache persists the current merged view. Atomic via
// write-temp-then-rename, so a kill mid-write never leaves a truncated
// cache behind (loadPollCache would survive one anyway, but the rename
// is cheaper than the exception path). Errors are swallowed on purpose:
// see the file doc.
func savePollCache(entries []worktree.Entry, vscode map[string]vscodeState, strict map[string]bool) {
	path := pollCachePath()
	if path == "" {
		return
	}
	data, err := json.Marshal(pollCache{
		Version:      cacheVersion,
		SavedAt:      time.Now(),
		Entries:      entries,
		VSCode:       vscode,
		VSCodeStrict: strict,
	})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
	}
}
