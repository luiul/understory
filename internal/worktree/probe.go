// In-process worktree membership probe (issue #10): which linked
// worktrees does a repo have ON DISK right now, answered by reading the
// repo's .git/worktrees admin directory directly — no subprocess, so
// none of the ~0.25s-per-spawn tax that makes the full `wt list` poll
// slow to notice a freshly created worktree on this machine (issue #9).
//
// The probe answers MEMBERSHIP only. Everything else a row shows
// (dirty, merge status, commit info) still comes from the streamed
// `wt list` poll; the probe exists so the view can react to a worktree
// appearing or vanishing within milliseconds instead of waiting out a
// multi-second fan-out, and so a poll can be triggered for exactly the
// repos that changed (a targeted poll) rather than the whole registry.
package worktree

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProbeEntry is one linked worktree discovered in-process from a repo's
// .git/worktrees admin area. Path and Branch come from the admin dir's
// own gitdir/HEAD files, the same files `git worktree list` reads;
// CreatedTime is the admin dir's filesystem birth time, the same
// creation proxy Entry.CreatedTime documents (the admin dir and the
// worktree directory are created in the same moment by git worktree
// add).
type ProbeEntry struct {
	Path   string
	Branch string // "" for a detached HEAD: no branch label to render
	// CreatedTime is the admin dir's birth time when readable, else the
	// moment the probe ran: a zero value would render as a giant bogus
	// age in the Created column, and "first seen now" is the honest
	// fallback for a worktree the probe just noticed.
	CreatedTime time.Time
}

// Probe reads each repo's .git/worktrees admin directory and returns
// the linked worktrees found there, keyed by the repo path as given.
// It never spawns a subprocess, so it is cheap enough to run on every
// focus event and on a multi-second background tick.
//
// A repo that cannot be probed (no readable .git, not a repo at all) is
// OMITTED from the result: a missing key means "unknown", never
// "empty", so a transient read failure can't masquerade as "every
// worktree was removed" and take rows down with it — the same
// keep-last-known discipline RepoResult.Err follows for the poll itself
// (issue #7). A present key with an empty slice is authoritative: the
// repo currently has no linked worktrees. The main checkout is never
// reported: it has no admin dir under .git/worktrees by definition.
func Probe(repoPaths []string) map[string][]ProbeEntry {
	out := map[string][]ProbeEntry{}
	for _, repo := range repoPaths {
		entries, ok := probeRepo(repo)
		if !ok {
			continue
		}
		out[repo] = entries
	}
	return out
}

// RegistryMtime returns the shared registry file's modification time,
// the cheap half of "did the registered repo set change?" between full
// repo-list resolutions: KnownRepoPaths itself costs a git subprocess
// for its cwd fallback (0.25s of spawn tax here), so the probe tick
// compares mtimes and only re-resolves the list on an actual change.
func RegistryMtime() (time.Time, bool) {
	path, ok := registryPath()
	if !ok {
		return time.Time{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// probeRepo is Probe's per-repo half: the repo's linked worktrees, or
// ok=false when the repo's admin area can't be read at all (see Probe's
// omitted-key contract). A repo with no .git/worktrees directory yet is
// a healthy repo with zero linked worktrees, not a read failure.
func probeRepo(repoPath string) ([]ProbeEntry, bool) {
	common, ok := gitCommonDir(repoPath)
	if !ok {
		return nil, false
	}
	admin := filepath.Join(common, "worktrees")
	dirs, err := os.ReadDir(admin)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []ProbeEntry{}, true
		}
		return nil, false
	}
	out := make([]ProbeEntry, 0, len(dirs))
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		entry, ok := probeAdminDir(filepath.Join(admin, d.Name()))
		if !ok {
			continue // mid-creation or unreadable; the next probe catches it
		}
		out = append(out, entry)
	}
	return out, true
}

// gitCommonDir resolves a repo checkout's git common directory
// in-process: .git as a directory IS it; .git as a file (the checkout
// is itself a linked worktree) names that worktree's admin dir, whose
// commondir file in turn names the common dir shared with the main
// checkout.
func gitCommonDir(repoPath string) (string, bool) {
	dotgit := filepath.Join(repoPath, ".git")
	info, err := os.Stat(dotgit)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return dotgit, true
	}
	data, err := os.ReadFile(dotgit)
	if err != nil {
		return "", false
	}
	admin := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir:"))
	if !filepath.IsAbs(admin) {
		admin = filepath.Join(repoPath, admin)
	}
	commonData, err := os.ReadFile(filepath.Join(admin, "commondir"))
	if err != nil {
		return "", false
	}
	common := strings.TrimSpace(string(commonData))
	if !filepath.IsAbs(common) {
		common = filepath.Join(admin, common)
	}
	return common, true
}

// probeAdminDir reads one .git/worktrees/<name> admin dir: gitdir names
// the worktree's own .git file (its parent directory is the worktree
// path), HEAD names the checked-out branch. git writes the admin dir's
// entries over the course of a worktree add (the dir itself appears
// first), so a missing or unreadable file means "creation still in
// flight" and the entry is skipped — the next probe, seconds later at
// most, reads it complete.
func probeAdminDir(dir string) (ProbeEntry, bool) {
	gd, err := os.ReadFile(filepath.Join(dir, "gitdir"))
	if err != nil {
		return ProbeEntry{}, false
	}
	wtPath := filepath.Clean(strings.TrimSuffix(strings.TrimSpace(string(gd)), "/.git"))
	head, err := os.ReadFile(filepath.Join(dir, "HEAD"))
	if err != nil {
		return ProbeEntry{}, false
	}
	entry := ProbeEntry{Path: wtPath}
	if h := strings.TrimSpace(string(head)); strings.HasPrefix(h, "ref: refs/heads/") {
		entry.Branch = strings.TrimPrefix(h, "ref: refs/heads/") // a plain SHA (detached HEAD) leaves Branch ""
	}
	// The worktree directory's own birth time is the creation proxy
	// Entry.CreatedTime documents (same source applyCreatedTime uses);
	// the admin dir's is its equal-aged stand-in, and "first seen now"
	// covers filesystems without a birth-time syscall (see
	// birthtime_other.go).
	if created, ok := dirBirthTime(wtPath); ok {
		entry.CreatedTime = created
	} else if created, ok := dirBirthTime(dir); ok {
		entry.CreatedTime = created
	} else {
		entry.CreatedTime = time.Now()
	}
	return entry, true
}
