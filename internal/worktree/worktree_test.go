package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestParseListOutputMapsFieldsAndComputesDirty(t *testing.T) {
	raw := []byte(`[
		{
			"branch": "add-sku-category",
			"path": "/Users/x/worktrees/hellofresh/add-sku-category/tardis-community",
			"is_main": false,
			"commit": {"sha": "14ae2d8", "short_sha": "14ae2d8", "message": "union topics", "timestamp": 1787217791},
			"working_tree": {"staged": false, "modified": true, "untracked": false, "renamed": false, "deleted": false},
			"repo": {"owner": "hellofresh", "name": "tardis-community"},
			"symbols": "^⚑"
		}
	]`)

	entries, skipped, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if skipped != 0 {
		t.Fatalf("got %d skipped entries, want 0 for a clean parse", skipped)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	got := entries[0]
	want := Entry{
		Owner:       "hellofresh",
		Repo:        "tardis-community",
		Branch:      "add-sku-category",
		Path:        "/Users/x/worktrees/hellofresh/add-sku-category/tardis-community",
		IsMain:      false,
		CommitSHA:   "14ae2d8",
		CommitMsg:   "union topics",
		CommitTime:  time.Unix(1787217791, 0),
		Dirty:       true,
		MergeStatus: MergeStatusUnknown, // no "main_state" in this fixture: falls back to unknown
		Symbols:     "^⚑",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseListOutputStripsStrayANSIEscapeBytes(t *testing.T) {
	// A real `wt list` payload can carry a raw ESC byte (0x1b) inside an
	// otherwise-unparsed field; a naive json.Unmarshal chokes on that
	// control character even though it never appears inside a JSON string
	// delimiter here, so parseListOutput strips it before decoding.
	raw := []byte("[{\"branch\": \"main\", \"path\": \"/repo\", \"is_main\": true, \"commit\": {\"timestamp\": 0}, \"working_tree\": {}, \"repo\": {}, \"symbols\": \"\x1b\"}]")

	entries, _, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if len(entries) != 1 || entries[0].Branch != "main" {
		t.Fatalf("got %+v, want one entry for branch main", entries)
	}
}

func TestParseListOutputTreatsEveryWorkingTreeFlagAsDirty(t *testing.T) {
	cases := []string{
		`{"staged": true}`,
		`{"modified": true}`,
		`{"untracked": true}`,
		`{"renamed": true}`,
		`{"deleted": true}`,
	}
	for _, wt := range cases {
		raw := []byte(`[{"branch": "b", "path": "/p", "commit": {"timestamp": 0}, "working_tree": ` + wt + `, "repo": {}}]`)
		entries, _, err := parseListOutput(raw)
		if err != nil {
			t.Fatalf("got err %v for %s", err, wt)
		}
		if len(entries) != 1 || !entries[0].Dirty {
			t.Fatalf("got %+v for %s, want Dirty=true", entries, wt)
		}
	}
}

func TestParseListOutputCleanWorktreeIsNotDirty(t *testing.T) {
	raw := []byte(`[{"branch": "main", "path": "/p", "commit": {"timestamp": 0}, "working_tree": {}, "repo": {}}]`)
	entries, _, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if len(entries) != 1 || entries[0].Dirty {
		t.Fatalf("got %+v, want Dirty=false", entries)
	}
}

func TestParseListOutputEmptyOrBlankIsNoEntriesNoError(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(""), []byte("   \n")} {
		entries, _, err := parseListOutput(raw)
		if err != nil {
			t.Fatalf("got err %v for %q", err, raw)
		}
		if entries != nil {
			t.Fatalf("got %+v, want nil entries for %q", entries, raw)
		}
	}
}

func TestParseListOutputInvalidJSONErrors(t *testing.T) {
	if _, _, err := parseListOutput([]byte("not json")); err == nil {
		t.Fatal("want an error for invalid JSON")
	}
}

func TestParseListOutputSkipsAMalformedEntryInsteadOfFailingTheRepo(t *testing.T) {
	// Decoding is per entry: one malformed object (here: branch is a
	// number) skips just that entry, it doesn't fail the whole repo's
	// output and blank every sibling row for a poll.
	raw := []byte(`[
		{"branch": "main", "path": "/p", "commit": {"timestamp": 0}, "working_tree": {}, "repo": {}},
		{"branch": 42, "path": 7}
	]`)
	entries, skipped, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v, want per-entry tolerance", err)
	}
	if skipped != 1 || len(entries) != 1 || entries[0].Branch != "main" {
		t.Fatalf("got %d entries (skipped %d): %+v, want the one valid entry plus one skip", len(entries), skipped, entries)
	}
}

func TestParseListOutputMultipleWorktrees(t *testing.T) {
	raw := []byte(`[
		{"branch": "main", "path": "/repo", "is_main": true, "commit": {"timestamp": 1}, "working_tree": {}, "repo": {"owner": "o", "name": "r"}},
		{"branch": "feature", "path": "/repo-feature", "is_main": false, "commit": {"timestamp": 2}, "working_tree": {"modified": true}, "repo": {"owner": "o", "name": "r"}}
	]`)
	entries, _, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].IsMain != true || entries[1].IsMain != false {
		t.Fatalf("got %+v, want first entry main and second not", entries)
	}
}

func TestParseListOutputMarksAPrunableWorktreeStale(t *testing.T) {
	raw := []byte(`[{"branch": "gone", "path": "/gone", "commit": {"timestamp": 0}, "working_tree": {}, "repo": {}, "worktree": {"state": "prunable"}}]`)
	entries, _, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if len(entries) != 1 || !entries[0].Stale {
		t.Fatalf("got %+v, want Stale=true for a prunable worktree", entries)
	}
}

func TestParseListOutputMarksABranchWorktreeMismatch(t *testing.T) {
	raw := []byte(`[{"branch": "feature/other", "path": "/w/review-jamie/repo", "commit": {"timestamp": 0}, "working_tree": {}, "repo": {}, "worktree": {"state": "branch_worktree_mismatch"}}]`)
	entries, _, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if len(entries) != 1 || !entries[0].Mismatch {
		t.Fatalf("got %+v, want Mismatch=true for a branch_worktree_mismatch worktree", entries)
	}
	if entries[0].Stale {
		t.Fatalf("got %+v, want Stale=false: a mismatch is not a removal candidate", entries)
	}
}

func TestParseListOutputRightfulPathIsNotAMismatch(t *testing.T) {
	raw := []byte(`[{"branch": "b", "path": "/p", "commit": {"timestamp": 0}, "working_tree": {}, "repo": {}, "worktree": {"state": "active"}}]`)
	entries, _, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if len(entries) != 1 || entries[0].Mismatch {
		t.Fatalf("got %+v, want Mismatch=false for an ordinary worktree", entries)
	}
}

func TestParseListOutputNonPrunableWorktreeIsNotStale(t *testing.T) {
	raw := []byte(`[{"branch": "b", "path": "/p", "commit": {"timestamp": 0}, "working_tree": {}, "repo": {}, "worktree": {"state": "branch_worktree_mismatch"}}]`)
	entries, _, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if len(entries) != 1 || entries[0].Stale {
		t.Fatalf("got %+v, want Stale=false for a non-prunable worktree state", entries)
	}
}

func TestMergeStatusMirrorsMainStateForAnOrdinaryWorktree(t *testing.T) {
	// All nine main_state values wt documents, plus absent and
	// unrecognized ones, mapped by action class (see Entry.MergeStatus's
	// doc): nothing-to-integrate states are merged, cleanly-mergeable
	// states are unmerged, only would_conflict is a conflict, and what wt
	// genuinely can't relate to main is unknown.
	cases := []struct {
		mainState string
		want      string
	}{
		{"empty", MergeStatusMerged},
		{"integrated", MergeStatusMerged},
		{"same_commit", MergeStatusMerged},
		{"behind", MergeStatusMerged},
		{"ahead", MergeStatusUnmerged},
		{"diverged", MergeStatusUnmerged},
		{"would_conflict", MergeStatusConflict},
		{"orphan", MergeStatusUnknown},
		{"", MergeStatusUnknown},
		{"some_future_state", MergeStatusUnknown},
	}
	for _, c := range cases {
		if got := mergeStatus(false, false, c.mainState); got != c.want {
			t.Errorf("mergeStatus(false, false, %q) = %q, want %q", c.mainState, got, c.want)
		}
	}
}

func TestMergeStatusIsNotApplicableForMainOrStale(t *testing.T) {
	if got := mergeStatus(true, false, "ahead"); got != "" {
		t.Errorf("mergeStatus(isMain=true) = %q, want \"\" (not applicable to the main worktree)", got)
	}
	if got := mergeStatus(false, true, "ahead"); got != "" {
		t.Errorf("mergeStatus(stale=true) = %q, want \"\" (not applicable once the worktree is gone)", got)
	}
}

func TestApplyRepoFallbackFillsRepoOnlyWhenOwnerAndRepoAreBothBlank(t *testing.T) {
	entries := []Entry{
		{Branch: "main"},                       // no repo info at all: wants the fallback
		{Owner: "acme", Branch: "feature"},     // has an owner already: leave alone
		{Repo: "widgets", Branch: "feature-2"}, // has a repo name already: leave alone
	}
	applyRepoFallback(entries, "/private/tmp/wt-clone")

	if got, want := entries[0].Repo, "wt-clone"; got != want {
		t.Fatalf("got Repo %q, want %q", got, want)
	}
	if entries[1].Repo != "" || entries[1].Owner != "acme" {
		t.Fatalf("got %+v, want owner left untouched and Repo still blank", entries[1])
	}
	if entries[2].Repo != "widgets" {
		t.Fatalf("got %+v, want Repo left untouched", entries[2])
	}
}

func TestApplyRepoFallbackUsesRepoPathBasenameEvenWithTrailingSlash(t *testing.T) {
	entries := []Entry{{Branch: "main"}}
	applyRepoFallback(entries, "/private/var/folders/x/T/tmp.92EItLcBae/repo/")

	if got, want := entries[0].Repo, "repo"; got != want {
		t.Fatalf("got Repo %q, want %q", got, want)
	}
}

func TestApplyCreatedTimeUsesTheWorktreeDirectorysBirthTime(t *testing.T) {
	dir := t.TempDir() // freshly created, so its birth time is "now"
	entries := []Entry{{Path: dir, CommitTime: time.Now().Add(-30 * 24 * time.Hour)}}

	applyCreatedTime(entries)

	// The directory's birth time should be roughly now, not the 30-day-old
	// CommitTime it'd fall back to if dirBirthTime somehow failed.
	if age := time.Since(entries[0].CreatedTime); age < 0 || age > time.Minute {
		t.Fatalf("got CreatedTime %v ago, want it close to just-now (the temp dir's real birth time)", age)
	}
}

func TestApplyCreatedTimeFallsBackToCommitTimeWhenTheDirectoryIsGone(t *testing.T) {
	// A Stale entry's directory no longer exists (see Entry.Stale's doc),
	// so dirBirthTime can't stat it and applyCreatedTime must fall back to
	// CommitTime rather than leaving CreatedTime zero.
	commitTime := time.Unix(1700000000, 0)
	entries := []Entry{{Path: "/no/such/path/understory-test", CommitTime: commitTime}}

	applyCreatedTime(entries)

	if entries[0].CreatedTime != commitTime {
		t.Fatalf("got CreatedTime %v, want the CommitTime fallback %v", entries[0].CreatedTime, commitTime)
	}
}

func TestDedupeDropsRepeatsPreservingFirstOccurrenceOrder(t *testing.T) {
	got := dedupe([]string{"/a", "/b", "/a", "/c", "/b"})
	want := []string{"/a", "/b", "/c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestListAllReturnsNilWhenWtIsNotOnPath(t *testing.T) {
	// This test only asserts the graceful-degradation path that doesn't
	// need a real `wt` binary: an empty repo list short-circuits before
	// ever checking Available(), returning an empty set rather than an error.
	if got := ListAll(nil); got != nil {
		t.Fatalf("got %+v, want nil for no repo paths", got)
	}
}

// writeFakeWt puts a fake `wt` binary first on PATH (a temp-dir shim):
// it answers instantly with one main-worktree JSON entry for most
// repos, but stalls for any repo whose path contains "slow" — the
// issue #7 shape, where concurrent polls pushed real repos past the
// timeout. The stall is `exec sleep`, so the timeout's SIGKILL hits the
// sleep itself with no orphaned grandchild holding the stdout pipe open.
func writeFakeWt(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	shim := `#!/bin/sh
repo=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-C" ]; then repo="$2"; shift 2; continue; fi
  shift
done
case "$repo" in
  *slow*) exec sleep 10 ;;
  *) printf '[{"branch":"main","path":"%s","is_main":true,"commit":{"timestamp":0},"working_tree":{},"repo":{"owner":"acme","name":"widgets"}}]' "$repo" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "wt"), []byte(shim), 0o755); err != nil {
		t.Fatalf("write the fake wt: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestListAllReportsAHealthyRepoAlongsideATimedOutOne(t *testing.T) {
	writeFakeWt(t)
	// The timeout needs real margin on both sides: even the instant shim
	// pays ~0.7s of process-spawn overhead on this machine class, and the
	// slow repo must be killed well before its own sleep ends.
	old := listTimeout
	listTimeout = 2 * time.Second
	t.Cleanup(func() { listTimeout = old })

	start := time.Now()
	results := ListAll([]string{"/repo/slow", "/repo/fast"})
	elapsed := time.Since(start)

	if len(results) != 2 {
		t.Fatalf("got %d results, want one per repo", len(results))
	}
	slow, fast := results[0], results[1]
	if fast.Err != nil || len(fast.Entries) != 1 || fast.Entries[0].RepoPath != "/repo/fast" {
		t.Fatalf("fast repo: got %+v, want its one entry and no error", fast)
	}
	if slow.Err == nil || len(slow.Entries) != 0 {
		t.Fatalf("slow repo: got %+v, want its timeout error and no entries", slow)
	}
	if elapsed >= 8*time.Second {
		t.Fatalf("ListAll took %s, want the slow repo killed at the 2s timeout, not waited out (the shim sleeps 10s)", elapsed)
	}
}

func TestRemoveArgsBuildsThePlainRemovalInvocation(t *testing.T) {
	e := Entry{RepoPath: "/repo", Branch: "feat-x", Path: "/w/feat-x"}
	got := removeArgs(e, RemoveOptions{})
	want := []string{"-C", "/repo", "remove", "feat-x", "-y"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRemoveArgsAppendsTheForceFlags(t *testing.T) {
	e := Entry{RepoPath: "/repo", Branch: "feat-x", Path: "/w/feat-x"}
	got := removeArgs(e, RemoveOptions{Force: true, ForceDelete: true})
	want := []string{"-C", "/repo", "remove", "feat-x", "-y", "-f", "-D"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPruneStaleArgsDropsOneRegistrationByPath(t *testing.T) {
	// Stale entries go through git, not wt (wt refuses a worktree whose
	// directory is already gone), and by path, not branch (a detached
	// stale entry may have no branch name at all).
	e := Entry{RepoPath: "/repo", Path: "/w/gone"}
	got := pruneStaleArgs(e)
	want := []string{"-C", "/repo", "worktree", "remove", "--force", "/w/gone"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRemoveErrorLeadsWithTheCommandOutput(t *testing.T) {
	err := &RemoveError{Branch: "feat-x", Output: "wt: worktree has uncommitted changes", Err: errors.New("exit status 1")}
	if got := err.Error(); got != "wt: worktree has uncommitted changes" {
		t.Fatalf("got %q, want the command output, not the exit status", got)
	}
	empty := &RemoveError{Branch: "feat-x", Err: errors.New("exit status 1")}
	if got := empty.Error(); got != "exit status 1" {
		t.Fatalf("got %q, want the wrapped error when there's no output", got)
	}
}

// --- parked marks -------------------------------------------------------
//
// The parked mark's storage is git config in a real repo (coppice's own
// tests cover its format in depth); these exercise the read fold
// (parkedMarks/applyParked), the write side (Park/Unpark), and the
// read-time follow-up rule (Entry.Parked).

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("commit", "--allow-empty", "-q", "-m", "init")
	return dir
}

func TestEntryParkedRequiresAMark(t *testing.T) {
	if (Entry{CommitTime: time.Now()}).Parked() {
		t.Fatal("an entry with no mark is not parked")
	}
}

func TestEntryParkedWithNoHeadCommitStaysParked(t *testing.T) {
	// CommitTime zero (wt genuinely couldn't report one) can't disprove
	// the mark, same as coppice's `bool(head_ts) and head_ts > parked_ts`.
	e := Entry{ParkedAt: time.Unix(1_700_000_000, 0)}
	if !e.Parked() {
		t.Fatal("a mark with no head time to compare against stays parked")
	}
}

func TestEntryParkedUntilTheHeadMovesPastTheMark(t *testing.T) {
	mark := time.Unix(1_700_000_000, 0)
	parked := Entry{ParkedAt: mark, CommitTime: mark.Add(-time.Hour)}
	if !parked.Parked() {
		t.Fatal("a head older than the mark is parked")
	}
	followUp := Entry{ParkedAt: mark, CommitTime: mark.Add(time.Hour)}
	if followUp.Parked() {
		t.Fatal("a head newer than the mark is follow-up: active again")
	}
}

func TestParkedMarksReadsEveryMarkAndSkipsJunk(t *testing.T) {
	dir := initRepo(t)
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("config", "branch.feat-a.parked-at", "1700000000")
	run("config", "branch.feat-b.parked-at", "not-a-number") // unparsable: skipped, not fatal
	run("config", "branch.feat-c.remote", "origin")          // unrelated branch config: ignored

	marks := parkedMarks(dir)
	if len(marks) != 1 || !marks["feat-a"].Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("got %v, want just feat-a's mark", marks)
	}
}

func TestParkedMarksIsEmptyWithoutARepo(t *testing.T) {
	if got := parkedMarks(t.TempDir()); len(got) != 0 {
		t.Fatalf("got %v, want no marks for a non-repo", got)
	}
}

func TestApplyParkedFoldsMarksIntoMatchingBranches(t *testing.T) {
	dir := initRepo(t)
	if err := Park(Entry{RepoPath: dir, Branch: "feat-a"}); err != nil {
		t.Fatalf("Park: %v", err)
	}
	entries := []Entry{{Branch: "feat-a"}, {Branch: "feat-b"}}

	applyParked(entries, dir)

	if entries[0].ParkedAt.IsZero() {
		t.Fatal("want feat-a's mark folded in")
	}
	if !entries[1].ParkedAt.IsZero() {
		t.Fatal("feat-b carries no mark")
	}
}

func TestParkAndUnparkRoundTrip(t *testing.T) {
	dir := initRepo(t)
	e := Entry{RepoPath: dir, Branch: "feat-a"}

	before := time.Now().Add(-time.Second)
	if err := Park(e); err != nil {
		t.Fatalf("Park: %v", err)
	}
	marks := parkedMarks(dir)
	if len(marks) != 1 || marks["feat-a"].Before(before) || marks["feat-a"].After(time.Now().Add(time.Second)) {
		t.Fatalf("got %v, want feat-a parked at ~now", marks)
	}

	if err := Unpark(e); err != nil {
		t.Fatalf("Unpark: %v", err)
	}
	if got := parkedMarks(dir); len(got) != 0 {
		t.Fatalf("got %v, want the mark gone", got)
	}

	// A missing key is not an error: unparking something never parked is
	// a no-op (git exits 5 for it).
	if err := Unpark(e); err != nil {
		t.Fatalf("Unpark without a mark: %v", err)
	}
}
