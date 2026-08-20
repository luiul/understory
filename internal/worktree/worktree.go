// Package worktree is a thin subprocess wrapper around the `wt`
// (worktrunk) binary, the same pattern canopy's own internal/herdrclient
// uses for `herdr`.
//
// understory does not reimplement worktree discovery or status: `wt`
// already resolves branch/commit/dirty/ahead-behind state correctly
// (differently for a main worktree vs. its remote, and a branch worktree
// vs. its main branch), so this package only shells out to `wt list
// --format json` and parses its output. It never writes anything, not to
// `wt`'s own state, and not to the shared repo registry it reads from
// (see KnownRepoPaths).
//
// Every exported function here degrades gracefully to "no worktree rows"
// when `wt` isn't on PATH or a given repo isn't `wt`-managed: a missing
// dependency just means those rows don't show up, not a broken poll.
package worktree

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// defaultTimeout bounds one `wt list` call. `wt list` runs several git
// subprocesses per worktree it reports on, and a repo with many
// worktrees can legitimately take a few seconds.
const defaultTimeout = 10 * time.Second

// maxConcurrentListCalls caps how many `wt list` subprocesses ListAll runs
// at once, mirroring coppice's own ThreadPoolExecutor cap on the same
// operation: enough to overlap a registry's worth of repos without
// forking an unbounded number of subprocesses if that registry is large.
const maxConcurrentListCalls = 8

// registryPath is the shared cross-tool worktree registry: populated by
// the user's own `wt` `post-start` hook (see worktrunk's own config) and
// self-healed by coppice; understory only ever reads it.
func registryPath() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".cache", "wt", "known-repos"), true
}

func binaryPath() (string, bool) {
	path, err := exec.LookPath("wt")
	return path, err == nil
}

// Available reports whether the `wt` binary is on PATH.
func Available() bool {
	_, ok := binaryPath()
	return ok
}

// Entry is one worktree of one repo, as `wt list --format json` reports
// it.
type Entry struct {
	Owner      string
	Repo       string
	Branch     string
	Path       string
	IsMain     bool
	CommitSHA  string
	CommitMsg  string
	CommitTime time.Time
	// Dirty is true if the worktree has any staged, modified, untracked,
	// renamed, or deleted change relative to its own HEAD.
	Dirty bool
	// Symbols is wt's own compact status glyph string (dirty/ahead/behind,
	// e.g. "!^|"), reused as-is rather than re-deriving ahead/behind
	// semantics here: those differ for a main worktree (vs. its remote)
	// and a branch worktree (vs. its main branch), and wt already resolved
	// that correctly.
	Symbols string
}

// KnownRepoPaths returns every repo understory knows to look for
// worktrees in: every path listed in the shared `~/.cache/wt/known-repos`
// registry that still exists on disk, plus the repo containing the
// current working directory, if any and not already listed. That
// fallback matters because a repo understory itself is run from may
// never have gone through `wt` or `coppice`, and requiring registration
// first would leave the view empty by default for it.
func KnownRepoPaths() []string {
	seen := map[string]bool{}
	var repos []string

	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return // stale registry entry (repo moved/deleted); skip rather than let a later `wt list` fail on it
		}
		seen[path] = true
		repos = append(repos, path)
	}

	if path, ok := registryPath(); ok {
		if data, err := os.ReadFile(path); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				add(line)
			}
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		if root, ok := repoRoot(cwd); ok {
			add(root)
		}
	}

	return repos
}

// repoRoot resolves path to its repo's root via `git rev-parse
// --show-toplevel`, or ok=false if path isn't inside a git repo (or `git`
// isn't on PATH).
func repoRoot(path string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// rawEntry mirrors the fields ListWorktrees needs from `wt list
// --format json`'s per-worktree object; every other field `wt` reports
// (remote, main, worktree, statusline, ...) is intentionally left
// unparsed.
type rawEntry struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
	IsMain bool   `json:"is_main"`
	Commit struct {
		SHA       string `json:"sha"`
		ShortSHA  string `json:"short_sha"`
		Message   string `json:"message"`
		Timestamp int64  `json:"timestamp"`
	} `json:"commit"`
	WorkingTree struct {
		Staged    bool `json:"staged"`
		Modified  bool `json:"modified"`
		Untracked bool `json:"untracked"`
		Renamed   bool `json:"renamed"`
		Deleted   bool `json:"deleted"`
	} `json:"working_tree"`
	Repo struct {
		Owner string `json:"owner"`
		Name  string `json:"name"`
	} `json:"repo"`
	Symbols string `json:"symbols"`
}

// ListWorktrees returns every worktree of the repo rooted at repoPath, or
// an error if `wt` isn't installed, repoPath isn't `wt`-managed, or its
// output couldn't be parsed. Callers (ListAll) are expected to skip a repo
// that errors rather than fail the whole poll over it.
func ListWorktrees(repoPath string) ([]Entry, error) {
	bin, ok := binaryPath()
	if !ok {
		return nil, errNotInstalled
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	args := []string{"-C", repoPath, "--config-set", "list.json-schema=1", "list", "--format", "json"}
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return nil, err
	}
	return parseListOutput(out)
}

// parseListOutput is the pure parsing logic for `wt list --format json`'s
// stdout, split out from ListWorktrees so it's testable without a real
// `wt` binary.
func parseListOutput(out []byte) ([]Entry, error) {
	// `wt list`'s JSON can carry a stray ANSI escape byte in the
	// statusline field; strip it so json.Unmarshal never chokes on a raw
	// control character (statusline itself is never parsed below, but a
	// naive Unmarshal still trips over it if left in).
	clean := strings.ReplaceAll(string(out), "\x1b", "")
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return nil, nil
	}

	var raw []rawEntry
	if err := json.Unmarshal([]byte(clean), &raw); err != nil {
		return nil, err
	}

	entries := make([]Entry, len(raw))
	for i, r := range raw {
		wt := r.WorkingTree
		entries[i] = Entry{
			Owner:      r.Repo.Owner,
			Repo:       r.Repo.Name,
			Branch:     r.Branch,
			Path:       r.Path,
			IsMain:     r.IsMain,
			CommitSHA:  r.Commit.ShortSHA,
			CommitMsg:  r.Commit.Message,
			CommitTime: time.Unix(r.Commit.Timestamp, 0),
			Dirty:      wt.Staged || wt.Modified || wt.Untracked || wt.Renamed || wt.Deleted,
			Symbols:    r.Symbols,
		}
	}
	return entries, nil
}

// ListAll returns every worktree of every repo in repoPaths, run with
// bounded concurrency (see maxConcurrentListCalls). A repo whose
// ListWorktrees call errors (not `wt`-managed, deleted mid-poll, `wt`
// itself missing) is silently skipped rather than failing the whole call,
// the same "one bad source doesn't blank the view" degradation
// canopy's own internal/herdrclient follows.
func ListAll(repoPaths []string) []Entry {
	if !Available() || len(repoPaths) == 0 {
		return nil
	}

	unique := dedupe(repoPaths)
	results := make([][]Entry, len(unique))

	sem := make(chan struct{}, maxConcurrentListCalls)
	var wg sync.WaitGroup
	for i, repo := range unique {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, repo string) {
			defer wg.Done()
			defer func() { <-sem }()
			if entries, err := ListWorktrees(repo); err == nil {
				results[i] = entries
			}
		}(i, repo)
	}
	wg.Wait()

	var all []Entry
	for _, r := range results {
		all = append(all, r...)
	}
	return all
}

func dedupe(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

type notInstalledError struct{}

func (notInstalledError) Error() string { return "'wt' (worktrunk) is not installed" }

var errNotInstalled = notInstalledError{}
