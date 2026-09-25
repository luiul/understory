package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luiul/understory/internal/worktree"
)

// The probe-gated refresh path (issue #10): FocusMsg and probeTickMsg
// run the in-process membership probe, reconcile rows against it
// (skeletons in, vanished rows out), and poll only what changed.

// installProbeSeams swaps the probe/repo-list/registry seams for fakes
// so probeNow never touches the real disk or spawns git. registryModified
// reports !ok (registry never changes) unless a test overrides it.
func installProbeSeams(t *testing.T, repos []string, probed map[string][]worktree.ProbeEntry) {
	t.Helper()
	origK, origP, origM := knownRepoPaths, probeMembership, registryModified
	t.Cleanup(func() {
		knownRepoPaths, probeMembership, registryModified = origK, origP, origM
	})
	knownRepoPaths = func() []string { return repos }
	probeMembership = func([]string) map[string][]worktree.ProbeEntry { return probed }
	registryModified = func() (time.Time, bool) { return time.Time{}, false }
}

func TestFocusWithNewWorktreeInsertsSkeletonAndStartsTargetedPoll(t *testing.T) {
	// The flow the probe exists for: a worktree created in another
	// window shows up the moment understory's window is focused — a
	// skeleton row right away, a targeted poll (not a full fan-out)
	// filling in real data behind it.
	installProbeSeams(t, []string{"/repo/a"}, map[string][]worktree.ProbeEntry{
		"/repo/a": {
			{Path: "/w/old", Branch: "old", CreatedTime: time.Now()},
			{Path: "/w/new", Branch: "new-feature", CreatedTime: time.Now()},
		},
	})
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{repoEntry("/repo/a", "/w/old", "old")})
	m.pollInFlight = false

	updated, cmd := m.Update(tea.FocusMsg{})
	m = updated.(Model)

	if cmd == nil || !m.pollInFlight {
		t.Fatal("want the targeted poll started on focus")
	}
	wts := m.visibleWorktrees()
	if len(wts) != 2 {
		t.Fatalf("got %v, want the old row plus the skeleton", pathsOf(wts))
	}
	sk := wts[1] // zero CommitTime sorts the skeleton last in its repo group
	if !sk.Skeleton || sk.Path != "/w/new" || sk.Branch != "new-feature" || sk.RepoPath != "/repo/a" {
		t.Fatalf("got %+v, want the /w/new skeleton", sk)
	}
	if sk.Owner != "acme" || sk.Repo != "widgets" {
		t.Fatalf("got %s/%s, want the sibling rows' owner/repo label", sk.Owner, sk.Repo)
	}
}

func TestFocusWithoutMembershipChangeFallsBackToFullPoll(t *testing.T) {
	installProbeSeams(t, []string{"/repo/a"}, map[string][]worktree.ProbeEntry{
		"/repo/a": {{Path: "/w/a", Branch: "a", CreatedTime: time.Now()}},
	})
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{repoEntry("/repo/a", "/w/a", "a")})
	m.pollInFlight = false

	if _, cmd := m.Update(tea.FocusMsg{}); cmd == nil {
		t.Fatal("want the fallback full poll when nothing changed on disk")
	}
}

func TestFocusWithRemovedWorktreeDropsRowImmediately(t *testing.T) {
	installProbeSeams(t, []string{"/repo/a"}, map[string][]worktree.ProbeEntry{
		"/repo/a": {}, // a healthy repo with no linked worktrees left
	})
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{repoEntry("/repo/a", "/w/gone", "gone")})
	m.pollInFlight = false

	updated, cmd := m.Update(tea.FocusMsg{})
	m = updated.(Model)

	if got := m.visibleWorktrees(); len(got) != 0 {
		t.Fatalf("got %v, want the vanished row dropped", pathsOf(got))
	}
	if cmd == nil || !m.pollInFlight {
		t.Fatal("want a targeted poll confirming the removal")
	}
}

func TestProbeOmittedRepoKeepsItsRows(t *testing.T) {
	// The probe couldn't read the repo at all: "unknown", never "empty"
	// — its rows stay, the same keep-last-known rule a failed poll
	// follows (issue #7).
	installProbeSeams(t, []string{"/repo/a"}, map[string][]worktree.ProbeEntry{})
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{repoEntry("/repo/a", "/w/a", "a")})
	m.pollInFlight = false

	updated, _ := m.Update(tea.FocusMsg{})
	m = updated.(Model)
	if got := pathsOf(m.visibleWorktrees()); len(got) != 1 || got[0] != "/w/a" {
		t.Fatalf("got %v, want last-known rows kept", got)
	}
}

func TestProbeChangeDuringAPollQueuesTheTargetedPoll(t *testing.T) {
	installProbeSeams(t, []string{"/repo/a"}, map[string][]worktree.ProbeEntry{
		"/repo/a": {{Path: "/w/new", Branch: "new", CreatedTime: time.Now()}},
	})
	m := New(999, false) // Init's first poll counts as in flight (see New)
	updated, _ := m.Update(pollStartedMsg{results: nil})
	m = updated.(Model)

	// Focus mid-poll: the probe still reacts immediately, but the
	// targeted poll queues instead of piling a second fan-out on top.
	updated, cmd := m.Update(tea.FocusMsg{})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("want no second fan-out while a poll is in flight")
	}
	if wts := m.visibleWorktrees(); len(wts) != 1 || !wts[0].Skeleton {
		t.Fatal("want the skeleton row shown during the wait")
	}
	if len(m.pendingProbe) != 1 || m.pendingProbe[0] != "/repo/a" {
		t.Fatalf("got pendingProbe %v, want /repo/a queued", m.pendingProbe)
	}

	// When the in-flight poll finishes, the queued targeted poll
	// starts — and the skeleton survives the full poll's prune step
	// (its repo WAS polled).
	updated, _ = m.Update(repoResultMsg{result: okResult("/repo/a")})
	m = updated.(Model)
	updated, _ = m.Update(pollDoneMsg{})
	m = updated.(Model)
	if !m.pollInFlight {
		t.Fatal("want the queued targeted poll started when the full poll finished")
	}
	if len(m.pendingProbe) != 0 {
		t.Fatal("want the queue drained")
	}
	if wts := m.visibleWorktrees(); len(wts) != 1 || !wts[0].Skeleton {
		t.Fatal("want the skeleton to survive the full poll's finish")
	}
}

func TestLandedRepoResultIsReconciledAgainstLiveDisk(t *testing.T) {
	// wt's answer for /repo/a is seconds old by the time it lands; a
	// worktree created in between must not stay invisible for a whole
	// poll cycle (the skeleton covers it) nor skip its re-poll.
	installProbeSeams(t, nil, map[string][]worktree.ProbeEntry{
		"/repo/a": {
			{Path: "/w/a", Branch: "a", CreatedTime: time.Now()},
			{Path: "/w/new", Branch: "new", CreatedTime: time.Now()},
		},
	})
	m := New(999, false)
	updated, _ := m.Update(pollStartedMsg{results: nil})
	m = updated.(Model)
	updated, _ = m.Update(repoResultMsg{result: okResult("/repo/a", repoEntry("/repo/a", "/w/a", "a"))})
	m = updated.(Model)

	got := pathsOf(m.visibleWorktrees())
	if len(got) != 2 || got[1] != "/w/new" {
		t.Fatalf("got %v, want the missed worktree skeleton-inserted", got)
	}
	if len(m.pendingProbe) != 1 || m.pendingProbe[0] != "/repo/a" {
		t.Fatalf("got pendingProbe %v, want /repo/a queued for re-poll", m.pendingProbe)
	}
}

func TestLandedRepoResultDropsRowsVanishedSinceThePollRan(t *testing.T) {
	installProbeSeams(t, nil, map[string][]worktree.ProbeEntry{
		"/repo/a": {}, // the disk, NOW: nothing left
	})
	m := New(999, false)
	updated, _ := m.Update(pollStartedMsg{results: nil})
	m = updated.(Model)
	// wt's seconds-old answer still saw the worktree:
	updated, _ = m.Update(repoResultMsg{result: okResult("/repo/a", repoEntry("/repo/a", "/w/gone", "gone"))})
	m = updated.(Model)
	if got := m.visibleWorktrees(); len(got) != 0 {
		t.Fatalf("got %v, want the vanished row dropped by the probe", pathsOf(got))
	}
}

func TestTargetedPollDoesNotPruneUnpolledRepos(t *testing.T) {
	installProbeSeams(t, nil, map[string][]worktree.ProbeEntry{
		"/repo/a": {{Path: "/w/a", Branch: "a", CreatedTime: time.Now()}},
	})
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{
		repoEntry("/repo/a", "/w/a", "a"),
		repoEntry("/repo/b", "/w/b", "b"),
	})
	updated, _ := m.Update(pollStartedMsg{results: nil, targeted: true})
	m = updated.(Model)
	updated, _ = m.Update(repoResultMsg{result: okResult("/repo/a", repoEntry("/repo/a", "/w/a", "a"))})
	m = updated.(Model)
	updated, _ = m.Update(pollDoneMsg{})
	m = updated.(Model)

	got := pathsOf(m.visibleWorktrees())
	if len(got) != 2 {
		t.Fatalf("got %v, want /repo/b's rows untouched by /repo/a's targeted poll", got)
	}
}

func TestTargetedPollLeavesFullPollFailureBookkeepingAlone(t *testing.T) {
	m := New(999, false)
	m.pollFailures = 2 // the last full poll's count
	updated, _ := m.Update(pollStartedMsg{results: nil, targeted: true})
	m = updated.(Model)
	updated, _ = m.Update(repoResultMsg{result: worktree.RepoResult{RepoPath: "/repo/a", Err: errors.New("boom")}})
	m = updated.(Model)
	updated, _ = m.Update(pollDoneMsg{})
	m = updated.(Model)

	if m.pollFailures != 2 {
		t.Fatalf("got pollFailures %d, want the last full poll's 2 untouched by the targeted failure", m.pollFailures)
	}
}

func TestProbeTickProbesWithoutFullPolling(t *testing.T) {
	installProbeSeams(t, []string{"/repo/a"}, map[string][]worktree.ProbeEntry{
		"/repo/a": {{Path: "/w/a", Branch: "a", CreatedTime: time.Now()}},
	})
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{repoEntry("/repo/a", "/w/a", "a")})
	m.pollInFlight = false

	updated, cmd := m.Update(probeTickMsg{})
	m = updated.(Model)
	if m.pollInFlight {
		t.Fatal("want no poll when nothing changed on disk")
	}
	if cmd == nil {
		t.Fatal("want the probe tick re-armed")
	}
}

func TestRegistryChangeRequestsAFullPoll(t *testing.T) {
	installProbeSeams(t, []string{"/repo/a"}, map[string][]worktree.ProbeEntry{})
	mtime := time.Now()
	registryModified = func() (time.Time, bool) { return mtime, true }

	m := New(999, false)
	m.pollInFlight = false

	// The first probe only establishes the baseline: same mtime, no poll.
	updated, _ := m.Update(probeTickMsg{})
	m = updated.(Model)
	if m.pollInFlight {
		t.Fatal("want the baseline probe free of side effects")
	}

	// A changed registry mtime re-resolves the repo list and full-polls.
	mtime = mtime.Add(time.Second)
	updated, _ = m.Update(probeTickMsg{})
	m = updated.(Model)
	if !m.pollInFlight {
		t.Fatal("want a full poll when the registry file changes")
	}
}

func TestRQueuesOneFullPollBehindAnInFlightOne(t *testing.T) {
	m := New(999, false) // Init's first poll counts as in flight (see New)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("want no concurrent second fan-out")
	}
	if !m.pendingFull {
		t.Fatal("want the explicit r queued behind the in-flight poll")
	}

	updated, _ = m.Update(pollDoneMsg{})
	m = updated.(Model)
	if !m.pollInFlight {
		t.Fatal("want the queued full poll started when the in-flight one finished")
	}
	if m.pendingFull {
		t.Fatal("want the queue drained")
	}
}

func TestSkeletonRowRendersEllipsisStatusesAndItsOwnBucket(t *testing.T) {
	e := worktree.Entry{Owner: "acme", Repo: "widgets", Branch: "new", Path: "/w/new", CreatedTime: time.Now(), Skeleton: true}
	if got := worktreeStatusLabel(e); got != "…" {
		t.Fatalf("got Worktree %q, want …", got)
	}
	if got := mergeStatusLabel(e); got != "…" {
		t.Fatalf("got Merge %q, want …", got)
	}
	if line := worktreeSummaryLine([]worktree.Entry{e}); !strings.Contains(line, "1 probing") {
		t.Fatalf("got summary %q, want the probing bucket", line)
	}
}
