// Package worktree is a thin subprocess wrapper around the `wt`
// (worktrunk) binary.
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
	// CreatedTime is when this worktree's own directory was created on
	// disk (its filesystem birth time, see dirBirthTime), used as a proxy
	// for "when this worktree/branch was created": neither `wt` nor git
	// itself record a branch's own creation time, but the worktree
	// directory `wt add`/`git worktree add` created for it is stamped
	// with exactly that moment. Falls back to CommitTime (see
	// applyCreatedTime) when birth time can't be read: a Stale entry's
	// directory is already gone, and non-macOS filesystems have no
	// straightforward stdlib way to expose birth time at all (see
	// birthtime_other.go).
	CreatedTime time.Time
	// Dirty is true if the worktree has any staged, modified, untracked,
	// renamed, or deleted change relative to its own HEAD.
	Dirty bool
	// Stale is true when `wt` reports this worktree's state as "prunable":
	// its administrative git metadata still exists, but the working
	// directory itself is gone (deleted by hand, moved, an OS temp dir
	// reaped, ...). Nothing else about it (Dirty, MergeStatus, Path) is
	// meaningful once that's true; it's purely a removal candidate.
	Stale bool
	// MergeStatus is this branch's relationship to the repo's main branch,
	// a triage answer to "what does this worktree need?" derived from
	// `wt`'s own main_state (which `wt` resolves via git, simulating the
	// merge with `git merge-tree` for the conflict case). `wt` documents
	// nine main_state values; they collapse into four display states by
	// action class: MergeStatusMerged for "empty", "integrated",
	// "same_commit", and "behind" (nothing to integrate, main already
	// contains everything, so the worktree is safe to remove),
	// MergeStatusUnmerged for "ahead" and "diverged" (has commits main
	// doesn't, but merges cleanly), MergeStatusConflict for
	// "would_conflict" (has commits main doesn't, and merging would
	// conflict: the one state that changes the action class, and time
	// sensitive, since a conflicting branch gets worse the longer main
	// moves), and MergeStatusUnknown for "orphan", an absent main_state,
	// or anything unrecognized `wt` might report in the future (wt
	// genuinely can't relate the branch to main). "" (not applicable)
	// for the main worktree itself (nothing to merge it into) or a Stale
	// one (its relationship to main is moot once the worktree directory
	// is already gone).
	MergeStatus string
	// Symbols is wt's own compact status glyph string (dirty/ahead/behind,
	// e.g. "!^|"), reused as-is rather than re-deriving ahead/behind
	// semantics here: those differ for a main worktree (vs. its remote)
	// and a branch worktree (vs. its main branch), and wt already resolved
	// that correctly.
	Symbols string
}

// MergeStatus values for Entry.MergeStatus. The zero value ("") means
// "not applicable" (see Entry.MergeStatus's doc).
const (
	MergeStatusMerged   = "merged"
	MergeStatusUnmerged = "unmerged"
	MergeStatusConflict = "conflict"
	MergeStatusUnknown  = "unknown"
)

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
	// Worktree.State surfaces `wt`'s own "prunable" designation (see
	// Entry.Stale); every other worktree state it might report (e.g.
	// "branch_worktree_mismatch", seen on a detached/mismatched checkout)
	// is intentionally left unparsed, understory has no use for it yet.
	Worktree struct {
		State string `json:"state"`
	} `json:"worktree"`
	// MainState is `wt`'s own assessment of this branch's relationship to
	// the repo's main branch ("empty"/"integrated"/"ahead"/...), reused to
	// derive Entry.MergeStatus rather than re-deriving that relationship
	// from raw commit/ref data ourselves: main vs. remote-tracking
	// semantics differ subtly enough (see worktree.go's package doc) that
	// `wt` having already resolved it correctly is worth deferring to.
	MainState string `json:"main_state"`
	Symbols   string `json:"symbols"`
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
	entries, err := parseListOutput(out)
	if err != nil {
		return nil, err
	}
	applyRepoFallback(entries, repoPath)
	applyCreatedTime(entries)
	return entries, nil
}

// applyCreatedTime fills in CreatedTime (mutating entries in place),
// preferring each entry's own worktree directory's filesystem birth
// time (dirBirthTime) as the best available proxy for "when this
// worktree/branch was created" (see Entry.CreatedTime's doc for why:
// neither `wt` nor git record that directly). Falls back to CommitTime
// when birth time can't be read: a Stale entry's directory is already
// gone by definition, and dirBirthTime itself always reports ok=false on
// platforms without a birth-time syscall (see birthtime_other.go).
func applyCreatedTime(entries []Entry) {
	for i := range entries {
		if bt, ok := dirBirthTime(entries[i].Path); ok {
			entries[i].CreatedTime = bt
		} else {
			entries[i].CreatedTime = entries[i].CommitTime
		}
	}
}

// applyRepoFallback fills in Repo (mutating entries in place) for any
// entry `wt` couldn't attribute to an owner/name, using the basename of
// repoPath (the repo root ListWorktrees queried) instead.
//
// `wt list --format json` only includes a `repo` object when the repo
// has a configured remote it can derive an owner/name from (see its
// `repo_url` field); a local-only repo (no remote, e.g. a scratch clone
// under a tmp dir) omits that key entirely, leaving Entry.Owner and
// Entry.Repo both "". Every such repo then rendered as the same blank
// label, so distinct remoteless repos were visually indistinguishable
// (and effectively ungrouped) in understory's Repo column. Falling back
// to the repo root's directory name at least gives each one a distinct,
// stable label, consistent across every worktree of that same repo.
func applyRepoFallback(entries []Entry, repoPath string) {
	fallback := filepath.Base(filepath.Clean(repoPath))
	for i := range entries {
		if entries[i].Owner == "" && entries[i].Repo == "" {
			entries[i].Repo = fallback
		}
	}
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
		stale := r.Worktree.State == "prunable"
		entries[i] = Entry{
			Owner:       r.Repo.Owner,
			Repo:        r.Repo.Name,
			Branch:      r.Branch,
			Path:        r.Path,
			IsMain:      r.IsMain,
			CommitSHA:   r.Commit.ShortSHA,
			CommitMsg:   r.Commit.Message,
			CommitTime:  time.Unix(r.Commit.Timestamp, 0),
			Dirty:       wt.Staged || wt.Modified || wt.Untracked || wt.Renamed || wt.Deleted,
			Stale:       stale,
			MergeStatus: mergeStatus(r.IsMain, stale, r.MainState),
			Symbols:     r.Symbols,
		}
	}
	return entries, nil
}

// mergeStatus derives Entry.MergeStatus from `wt`'s own reporting: see
// Entry.MergeStatus's doc for what each value means and when it applies.
// The mapping stays centralized in this one function so that wt's JSON
// schema 2 (which moves this vocabulary to display.state) and any future
// main_state values each touch exactly one place; unrecognized values
// degrade to MergeStatusUnknown rather than overstating certainty.
func mergeStatus(isMain, stale bool, mainState string) string {
	if isMain || stale {
		return ""
	}
	switch mainState {
	case "empty", "integrated", "same_commit", "behind":
		return MergeStatusMerged
	case "ahead", "diverged":
		return MergeStatusUnmerged
	case "would_conflict":
		return MergeStatusConflict
	default: // "orphan", absent, or a future value wt adds later
		return MergeStatusUnknown
	}
}

// ListAll returns every worktree of every repo in repoPaths, run with
// bounded concurrency (see maxConcurrentListCalls). A repo whose
// ListWorktrees call errors (not `wt`-managed, deleted mid-poll, `wt`
// itself missing) is silently skipped rather than failing the whole call,
// a graceful-degradation pattern: one bad source doesn't blank the view.
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
