package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luiul/understory/internal/worktree"
)

// The streaming poll path (issue #8): pollStartedMsg → repoResultMsg × N
// → pollDoneMsg, with the window snapshot arriving on its own
// snapshotMsg at any point in the sequence. The batch path
// (pollResultMsg) has its own coverage in worktrees_test.go; these
// tests drive the streamed messages through Update itself.

// streamPoll plays a full streamed poll into m: the started message,
// one repo result per given RepoResult, and the done message,
// returning the final model and the done message's command.
func streamPoll(t *testing.T, m Model, results ...worktree.RepoResult) (Model, tea.Cmd) {
	t.Helper()
	updated, _ := m.Update(pollStartedMsg{results: nil})
	mm := updated.(Model)
	for _, r := range results {
		updated, _ = mm.Update(repoResultMsg{result: r})
		mm = updated.(Model)
	}
	updated, doneCmd := mm.Update(pollDoneMsg{})
	return updated.(Model), doneCmd
}

func TestStreamedReposRenderProgressively(t *testing.T) {
	m := New(999, false)
	a := repoEntry("/repo/a", "/w/a", "a")

	// Mid-poll, after only repo A has reported, its rows must already be
	// visible — the old barrier path showed nothing until every repo
	// (and the slowest) had finished.
	updated, _ := m.Update(pollStartedMsg{results: nil})
	mm := updated.(Model)
	updated, _ = mm.Update(repoResultMsg{result: okResult("/repo/a", a)})
	mm = updated.(Model)

	if got := pathsOf(mm.displayedWorktrees()); len(got) != 1 || got[0] != "/w/a" {
		t.Fatalf("got %v, want /w/a rendered mid-poll", got)
	}
	if !mm.pollInFlight {
		t.Fatal("want the poll still marked in flight mid-stream")
	}
}

func TestStreamedFailureKeepsLastKnownRows(t *testing.T) {
	m := New(999, false)
	a := repoEntry("/repo/a", "/w/a", "a")
	m.applyWorktrees([]worktree.Entry{a})

	mm, _ := streamPoll(t, m, worktree.RepoResult{RepoPath: "/repo/a", Err: errors.New("timed out")})

	if got := pathsOf(mm.displayedWorktrees()); len(got) != 1 || got[0] != "/w/a" {
		t.Fatalf("got %v, want /w/a kept on a failed streamed poll", got)
	}
	if mm.pollFailures != 1 {
		t.Fatalf("got pollFailures %d, want 1", mm.pollFailures)
	}
}

func TestPollDonePrunesReposThatDroppedOutOfTheRegistry(t *testing.T) {
	m := New(999, false)
	a, b := repoEntry("/repo/a", "/w/a", "a"), repoEntry("/repo/b", "/w/b", "b")
	m.applyWorktrees([]worktree.Entry{a, b})

	// Only /repo/b reports this cycle: /repo/a vanished from
	// KnownRepoPaths between polls, and its rows were only authoritative
	// while registered.
	mm, _ := streamPoll(t, m, okResult("/repo/b", b))

	if got := pathsOf(mm.displayedWorktrees()); len(got) != 1 || got[0] != "/w/b" {
		t.Fatalf("got %v, want only /w/b after /repo/a went unpolled", got)
	}
}

func TestPollDoneClearsTheInFlightGuardAndCountsThePoll(t *testing.T) {
	m := New(999, false) // New sets pollInFlight for Init's first poll
	mm, _ := streamPoll(t, m, okResult("/repo/a"))

	if mm.pollInFlight {
		t.Fatal("want the in-flight guard cleared at poll done")
	}
	if mm.poll != nil {
		t.Fatal("want the live-poll bookkeeping released at poll done")
	}
	if mm.pollsLanded != 1 {
		t.Fatalf("got pollsLanded %d, want 1", mm.pollsLanded)
	}
}

func TestPollDoneSavesTheCache(t *testing.T) {
	useTempCache(t)
	m := New(999, false)
	a := repoEntry("/repo/a", "/w/a", "a")

	streamPoll(t, m, okResult("/repo/a", a))

	c := loadPollCache()
	if c == nil {
		t.Fatal("want a cache file after the first completed poll")
	}
	if len(c.Entries) != 1 || c.Entries[0].Path != "/w/a" {
		t.Fatalf("got cached entries %+v, want /w/a", c.Entries)
	}
}

func TestPollDoneNotifiesOnlyOnTheZeroToNFailureTransition(t *testing.T) {
	m := New(999, false)
	fail := worktree.RepoResult{RepoPath: "/repo/a", Err: errors.New("timed out")}

	mm, cmd := streamPoll(t, m, fail)
	if cmd == nil || !mm.notifyIsError || !strings.Contains(mm.notification, "1 repo timed out or errored") {
		t.Fatalf("got notification %q (cmd %v), want the 0→N failure notice", mm.notification, cmd)
	}

	mm, cmd = streamPoll(t, mm, fail)
	if cmd != nil {
		t.Fatalf("got a second notification, want none while failures persist (token %d)", mm.notifyToken)
	}

	mm, _ = streamPoll(t, mm, okResult("/repo/a"))
	_, cmd = streamPoll(t, mm, fail)
	if cmd == nil {
		t.Fatal("want a fresh notification after failures recovered to 0 and rose again")
	}
}

func TestSnapshotArrivingFirstAppliesToReposAsTheyLand(t *testing.T) {
	m := New(999, false)
	a := repoEntry("/repo/a", "/w/a", "a")
	snap := fakeVSCodeSnapshot{open: map[string]bool{"/w/a": true}}

	// The snapshotMsg beats even pollStartedMsg (the two are batched
	// concurrently): it must be kept, not dropped.
	updated, _ := m.Update(snapshotMsg{snap: snap})
	mm := updated.(Model)
	updated, _ = mm.Update(pollStartedMsg{results: nil})
	mm = updated.(Model)
	updated, _ = mm.Update(repoResultMsg{result: okResult("/repo/a", a)})
	mm = updated.(Model)

	if mm.vscode["/w/a"] != vscodeOpen {
		t.Fatalf("got vscode %v, want open for /w/a", mm.vscode["/w/a"])
	}
	if !mm.vscodeStrict["/w/a"] {
		t.Fatalf("got strict %v, want true for /w/a", mm.vscodeStrict["/w/a"])
	}
}

func TestSnapshotArrivingLateCatchesUpReposThatLandedFirst(t *testing.T) {
	m := New(999, false)
	a := repoEntry("/repo/a", "/w/a", "a")
	snap := fakeVSCodeSnapshot{open: map[string]bool{"/w/a": true}}

	updated, _ := m.Update(pollStartedMsg{results: nil})
	mm := updated.(Model)
	updated, _ = mm.Update(repoResultMsg{result: okResult("/repo/a", a)})
	mm = updated.(Model)
	if mm.vscode["/w/a"] != vscodeUnknown {
		t.Fatalf("got %v before the snapshot, want unknown", mm.vscode["/w/a"])
	}

	updated, _ = mm.Update(snapshotMsg{snap: snap})
	mm = updated.(Model)
	if mm.vscode["/w/a"] != vscodeOpen {
		t.Fatalf("got %v after the late snapshot, want open", mm.vscode["/w/a"])
	}
}

func TestLateSnapshotLeavesFailedReposLastKnownStatesAlone(t *testing.T) {
	m := New(999, false)
	a := repoEntry("/repo/a", "/w/a", "a")
	m.applyWorktrees([]worktree.Entry{a})
	m.vscode = map[string]vscodeState{"/w/a": vscodeOpen}
	snap := fakeVSCodeSnapshot{open: map[string]bool{}} // would say closed if applied

	updated, _ := m.Update(pollStartedMsg{results: nil})
	mm := updated.(Model)
	updated, _ = mm.Update(repoResultMsg{result: worktree.RepoResult{RepoPath: "/repo/a", Err: errors.New("timed out")}})
	mm = updated.(Model)
	updated, _ = mm.Update(snapshotMsg{snap: snap})
	mm = updated.(Model)

	if mm.vscode["/w/a"] != vscodeOpen {
		t.Fatalf("got %v, want the failed repo's last-known open state kept", mm.vscode["/w/a"])
	}
}

func TestASnapshotArrivingWithNoPollInFlightIsDiscarded(t *testing.T) {
	m := New(999, false)
	m.pollInFlight = false // idle between polls

	updated, _ := m.Update(snapshotMsg{snap: fakeVSCodeSnapshot{}})
	mm := updated.(Model)

	if mm.poll != nil {
		t.Fatal("want no live-poll bookkeeping created for a discarded snapshot")
	}
}
