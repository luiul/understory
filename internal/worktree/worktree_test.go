package worktree

import (
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

	entries, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
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

	entries, err := parseListOutput(raw)
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
		entries, err := parseListOutput(raw)
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
	entries, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if len(entries) != 1 || entries[0].Dirty {
		t.Fatalf("got %+v, want Dirty=false", entries)
	}
}

func TestParseListOutputEmptyOrBlankIsNoEntriesNoError(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(""), []byte("   \n")} {
		entries, err := parseListOutput(raw)
		if err != nil {
			t.Fatalf("got err %v for %q", err, raw)
		}
		if entries != nil {
			t.Fatalf("got %+v, want nil entries for %q", entries, raw)
		}
	}
}

func TestParseListOutputInvalidJSONErrors(t *testing.T) {
	if _, err := parseListOutput([]byte("not json")); err == nil {
		t.Fatal("want an error for invalid JSON")
	}
}

func TestParseListOutputMultipleWorktrees(t *testing.T) {
	raw := []byte(`[
		{"branch": "main", "path": "/repo", "is_main": true, "commit": {"timestamp": 1}, "working_tree": {}, "repo": {"owner": "o", "name": "r"}},
		{"branch": "feature", "path": "/repo-feature", "is_main": false, "commit": {"timestamp": 2}, "working_tree": {"modified": true}, "repo": {"owner": "o", "name": "r"}}
	]`)
	entries, err := parseListOutput(raw)
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
	entries, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if len(entries) != 1 || !entries[0].Stale {
		t.Fatalf("got %+v, want Stale=true for a prunable worktree", entries)
	}
}

func TestParseListOutputNonPrunableWorktreeIsNotStale(t *testing.T) {
	raw := []byte(`[{"branch": "b", "path": "/p", "commit": {"timestamp": 0}, "working_tree": {}, "repo": {}, "worktree": {"state": "branch_worktree_mismatch"}}]`)
	entries, err := parseListOutput(raw)
	if err != nil {
		t.Fatalf("got err %v", err)
	}
	if len(entries) != 1 || entries[0].Stale {
		t.Fatalf("got %+v, want Stale=false for a non-prunable worktree state", entries)
	}
}

func TestMergeStatusMirrorsMainStateForAnOrdinaryWorktree(t *testing.T) {
	cases := []struct {
		mainState string
		want      string
	}{
		{"empty", MergeStatusMerged},
		{"integrated", MergeStatusMerged},
		{"ahead", MergeStatusUnmerged},
		{"diverged", MergeStatusUnknown},
		{"", MergeStatusUnknown},
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
