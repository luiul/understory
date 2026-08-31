package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luiul/understory/internal/worktree"
)

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// stubRemove swaps the removeWorktree seam for a recording fake, same
// pattern as the openVSCode seam tests in app_test.go.
func stubRemove(t *testing.T) (got *[]worktree.Entry, opts *[]worktree.RemoveOptions) {
	t.Helper()
	orig := removeWorktree
	t.Cleanup(func() { removeWorktree = orig })
	got = &[]worktree.Entry{}
	opts = &[]worktree.RemoveOptions{}
	removeWorktree = func(e worktree.Entry, o worktree.RemoveOptions) error {
		*got = append(*got, e)
		*opts = append(*opts, o)
		return nil
	}
	return got, opts
}

func TestXOpensTheConfirmationPromptForTheSelectedRow(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})

	updated, cmd := m.Update(key("x"))
	m = updated.(Model)

	if m.confirm == nil {
		t.Fatal("want a pending confirmation after x")
	}
	if m.confirm.kind != confirmRemoveOne {
		t.Fatalf("got kind %v, want confirmRemoveOne", m.confirm.kind)
	}
	if len(m.confirm.entries) != 1 || m.confirm.entries[0].Path != "/w/a" {
		t.Fatalf("got targets %+v, want the selected row", m.confirm.entries)
	}
	if cmd == nil {
		t.Fatal("want an auto-cancel tick scheduled with the prompt")
	}
	if !strings.Contains(m.footerView(), "Remove worktree a at /w/a?") {
		t.Fatalf("footer = %q, want the removal prompt", m.footerView())
	}
}

func TestXOnThePlaceholderRowDoesNothing(t *testing.T) {
	m := New(999, false)
	updated, cmd := m.Update(key("x"))
	m = updated.(Model)
	if m.confirm != nil || cmd != nil {
		t.Fatal("want no prompt and no command when nothing is selected")
	}
}

func TestConfirmYesDispatchesRemovalAndClosesThePrompt(t *testing.T) {
	got, opts := stubRemove(t)

	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	updated, _ := m.Update(key("x"))
	m = updated.(Model)

	updated, cmd := m.Update(key("y"))
	m = updated.(Model)
	if m.confirm != nil {
		t.Fatal("want the prompt closed after y")
	}
	if cmd == nil {
		t.Fatal("want a removal command after y")
	}
	msg := cmd()
	res, ok := msg.(removeResultMsg)
	if !ok {
		t.Fatalf("got %T, want removeResultMsg", msg)
	}
	if len(res.results) != 1 || res.results[0].Err != nil {
		t.Fatalf("got %+v, want one successful result", res.results)
	}
	if len(*got) != 1 || (*got)[0].Path != "/w/a" {
		t.Fatalf("removed %+v, want the selected entry", *got)
	}
	if (*opts)[0].Force || (*opts)[0].ForceDelete {
		t.Fatalf("got opts %+v, want no force flags for a plain x removal", (*opts)[0])
	}
}

func TestConfirmEnterAlsoConfirms(t *testing.T) {
	got, _ := stubRemove(t)
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	updated, _ := m.Update(key("x"))
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.confirm != nil || cmd == nil {
		t.Fatal("want enter to answer the prompt like y")
	}
	cmd()
	if len(*got) != 1 {
		t.Fatalf("got %d removals, want 1", len(*got))
	}
}

func TestConfirmNoAndEscCancelWithoutDispatching(t *testing.T) {
	for _, k := range []tea.KeyMsg{key("n"), {Type: tea.KeyEsc}} {
		got, _ := stubRemove(t)
		m := New(999, false)
		m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
		updated, _ := m.Update(key("x"))
		m = updated.(Model)

		updated, cmd := m.Update(k)
		m = updated.(Model)
		if m.confirm != nil {
			t.Fatalf("want the prompt closed after %v", k)
		}
		if cmd != nil {
			t.Fatalf("want no command after %v", k)
		}
		if len(*got) != 0 {
			t.Fatalf("got %d removals after %v, want none", len(*got), k)
		}
	}
}

func TestConfirmationIsModalAndSwallowsOtherKeys(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", time.Minute), wtEntry("/w/b", "b", 2*time.Minute)})
	updated, _ := m.Update(key("x"))
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("want no command for a swallowed key")
	}
	if m.table.Cursor() != 0 {
		t.Fatal("want the cursor unmoved while the prompt is open")
	}
	if m.confirm == nil {
		t.Fatal("want the prompt still open")
	}

	// q is swallowed too: an in-flight prompt must be answered or
	// cancelled, not quit around.
	updated, _ = m.Update(key("q"))
	m = updated.(Model)
	if m.quitting {
		t.Fatal("want q swallowed by the modal, not quit")
	}
	if m.confirm == nil {
		t.Fatal("want the prompt still open after q")
	}
}

func TestConfirmationAutoCancelsAfterTheTimeout(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	updated, _ := m.Update(key("x"))
	m = updated.(Model)

	updated, cmd := m.Update(cancelConfirmMsg{token: m.confirmToken})
	m = updated.(Model)
	if m.confirm != nil {
		t.Fatal("want the prompt cancelled once its own token fires")
	}
	if m.notification == "" || m.notifyIsError {
		t.Fatalf("got notification %q (err=%v), want a subtle cancellation note", m.notification, m.notifyIsError)
	}
	if cmd == nil {
		t.Fatal("want the notification's clear scheduled")
	}
}

func TestStaleCancelConfirmTokenIsIgnored(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	updated, _ := m.Update(key("x"))
	m = updated.(Model)

	updated, _ = m.Update(cancelConfirmMsg{token: m.confirmToken - 1})
	m = updated.(Model)
	if m.confirm == nil {
		t.Fatal("want the prompt to survive a stale cancel token")
	}
}

func TestPollDroppingThePendingEntryCancelsThePrompt(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	updated, _ := m.Update(key("x"))
	m = updated.(Model)

	m.applyWorktrees(nil) // removed externally between polls
	if m.confirm != nil {
		t.Fatal("want the prompt cancelled once its target is gone")
	}
}

func TestPollShrinkingABatchKeepsOnlyTheRemainingEntries(t *testing.T) {
	stale1 := wtEntry("/w/gone1", "a", 0)
	stale1.Stale = true
	stale2 := wtEntry("/w/gone2", "b", time.Minute)
	stale2.Stale = true
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{stale1, stale2})
	updated, _ := m.Update(key("p"))
	m = updated.(Model)

	m.applyWorktrees([]worktree.Entry{stale2}) // stale1 pruned externally
	if m.confirm == nil {
		t.Fatal("want the prompt to survive with remaining targets")
	}
	if len(m.confirm.entries) != 1 || m.confirm.entries[0].Path != "/w/gone2" {
		t.Fatalf("got %+v, want only stale2 left", m.confirm.entries)
	}
	if !strings.Contains(m.confirmPrompt(), "Prune 1 stale") {
		t.Fatalf("prompt = %q, want the count updated to 1", m.confirmPrompt())
	}
}

func TestXOnTheMainWorktreeIsRefused(t *testing.T) {
	main := wtEntry("/w/main", "main", 0)
	main.IsMain = true
	m := New(999, true) // showMain, so the row exists at all
	m.applyWorktrees([]worktree.Entry{main})

	updated, cmd := m.Update(key("x"))
	m = updated.(Model)
	if m.confirm != nil {
		t.Fatal("want no prompt for the main worktree")
	}
	if !m.notifyIsError || !strings.Contains(m.notification, "main worktree") {
		t.Fatalf("got notification %q (err=%v), want the main-worktree refusal", m.notification, m.notifyIsError)
	}
	if cmd == nil {
		t.Fatal("want the notification's clear scheduled")
	}
}

func TestForceRemovePromptWarnsAndPassesForceFlags(t *testing.T) {
	got, opts := stubRemove(t)

	dirty := wtEntry("/w/a", "a", 0)
	dirty.Dirty = true
	dirty.MergeStatus = worktree.MergeStatusUnmerged
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{dirty})

	updated, _ := m.Update(key("X"))
	m = updated.(Model)
	prompt := m.confirmPrompt()
	for _, want := range []string{"Force remove", "discarded", "even if unmerged"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want it to mention %q", prompt, want)
		}
	}

	updated, cmd := m.Update(key("y"))
	m = updated.(Model)
	cmd()
	if len(*got) != 1 {
		t.Fatalf("got %d removals, want 1", len(*got))
	}
	if !(*opts)[0].Force || !(*opts)[0].ForceDelete {
		t.Fatalf("got opts %+v, want Force and ForceDelete", (*opts)[0])
	}
}

func TestStaleEntryPromptOnlyDropsTheRegistration(t *testing.T) {
	stale := wtEntry("/w/gone", "a", 0)
	stale.Stale = true
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{stale})

	updated, _ := m.Update(key("x"))
	m = updated.(Model)
	prompt := m.confirmPrompt()
	if !strings.Contains(prompt, "stale registration") || !strings.Contains(prompt, "already gone") {
		t.Fatalf("prompt = %q, want the stale-registration wording", prompt)
	}
}

func TestDirtyEntryPromptWarnsThatWtWillRefuse(t *testing.T) {
	dirty := wtEntry("/w/a", "a", 0)
	dirty.Dirty = true
	dirty.MergeStatus = worktree.MergeStatusUnmerged
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{dirty})

	updated, _ := m.Update(key("x"))
	m = updated.(Model)
	prompt := m.confirmPrompt()
	if !strings.Contains(prompt, "wt will refuse") || !strings.Contains(prompt, "X force removes") {
		t.Fatalf("prompt = %q, want the dirty warning pointing at X", prompt)
	}
	if !strings.Contains(prompt, "will be kept") {
		t.Fatalf("prompt = %q, want the unmerged branch marked as kept", prompt)
	}
}

func TestMergedEntryPromptSaysTheBranchIsDeletedToo(t *testing.T) {
	merged := wtEntry("/w/a", "a", 0)
	merged.MergeStatus = worktree.MergeStatusMerged
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{merged})

	updated, _ := m.Update(key("x"))
	m = updated.(Model)
	if prompt := m.confirmPrompt(); !strings.Contains(prompt, "merged and will be deleted too") {
		t.Fatalf("prompt = %q, want the branch-deletion warning", prompt)
	}
}

func TestPruneStaleTargetsEveryStaleEntry(t *testing.T) {
	got, _ := stubRemove(t)

	stale1 := wtEntry("/w/gone1", "a", 0)
	stale1.Stale = true
	clean := wtEntry("/w/c", "c", time.Minute)
	stale2 := wtEntry("/w/gone2", "b", 2*time.Minute)
	stale2.Stale = true
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{stale1, clean, stale2})

	updated, _ := m.Update(key("p"))
	m = updated.(Model)
	if m.confirm == nil || m.confirm.kind != confirmPruneStale {
		t.Fatalf("got %+v, want a pending confirmPruneStale", m.confirm)
	}
	if len(m.confirm.entries) != 2 {
		t.Fatalf("got %d targets, want the 2 stale entries", len(m.confirm.entries))
	}
	if !strings.Contains(m.confirmPrompt(), "Prune 2 stale") {
		t.Fatalf("prompt = %q", m.confirmPrompt())
	}

	updated, cmd := m.Update(key("y"))
	m = updated.(Model)
	cmd()
	if len(*got) != 2 {
		t.Fatalf("got %d removals, want 2", len(*got))
	}
	for _, e := range *got {
		if !e.Stale {
			t.Fatalf("removed %+v, want stale entries only", e)
		}
	}
}

func TestPruneStaleWithNoneStaleJustNotifies(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})

	updated, cmd := m.Update(key("p"))
	m = updated.(Model)
	if m.confirm != nil {
		t.Fatal("want no prompt when nothing is stale")
	}
	if !strings.Contains(m.notification, "no stale worktrees") {
		t.Fatalf("got notification %q", m.notification)
	}
	if cmd == nil {
		t.Fatal("want the notification's clear scheduled")
	}
}

func TestRemoveMergedTargetsOnlyTheSelectedReposMergedEntries(t *testing.T) {
	got, _ := stubRemove(t)

	merged := wtEntry("/w/a", "merged-br", 0)
	merged.MergeStatus = worktree.MergeStatusMerged
	unmerged := wtEntry("/w/b", "unmerged-br", time.Minute)
	unmerged.MergeStatus = worktree.MergeStatusUnmerged
	other := wtEntry("/w/c", "other", 2*time.Minute)
	other.Repo = "other-repo"
	other.MergeStatus = worktree.MergeStatusMerged
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{merged, unmerged, other})
	m.table.SetCursor(0) // the acme/widgets merged row (most recent)

	updated, _ := m.Update(key("M"))
	m = updated.(Model)
	if m.confirm == nil || m.confirm.kind != confirmRemoveMerged {
		t.Fatalf("got %+v, want a pending confirmRemoveMerged", m.confirm)
	}
	if len(m.confirm.entries) != 1 || m.confirm.entries[0].Branch != "merged-br" {
		t.Fatalf("got targets %+v, want only acme/widgets' merged entry", m.confirm.entries)
	}
	if !strings.Contains(m.confirmPrompt(), "acme/widgets") {
		t.Fatalf("prompt = %q, want the repo named", m.confirmPrompt())
	}

	updated, cmd := m.Update(key("y"))
	m = updated.(Model)
	cmd()
	if len(*got) != 1 || (*got)[0].Branch != "merged-br" {
		t.Fatalf("removed %+v, want only merged-br", *got)
	}
}

func TestRemoveMergedWithNoneMergedJustNotifies(t *testing.T) {
	unmerged := wtEntry("/w/a", "a", 0)
	unmerged.MergeStatus = worktree.MergeStatusUnmerged
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{unmerged})

	updated, _ := m.Update(key("M"))
	m = updated.(Model)
	if m.confirm != nil {
		t.Fatal("want no prompt when the selected repo has nothing merged")
	}
	if !strings.Contains(m.notification, "no merged worktrees") {
		t.Fatalf("got notification %q", m.notification)
	}
}

func TestRemoveResultSuccessNotifiesAndRepolls(t *testing.T) {
	m := New(999, false)
	updated, cmd := m.Update(removeResultMsg{results: []worktree.RemoveResult{
		{Entry: wtEntry("/w/a", "a", 0), Err: nil},
	}})
	m = updated.(Model)
	if m.notification != "removed a" || m.notifyIsError {
		t.Fatalf("got notification %q (err=%v)", m.notification, m.notifyIsError)
	}
	if cmd == nil {
		t.Fatal("want the notification clear and the repoll batched")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("got %T (%v), want a BatchMsg of clear-notification + poll", cmd(), cmd())
	}
}

func TestRemoveResultFailureSuggestsForceForADirtyRefusal(t *testing.T) {
	m := New(999, false)
	err := &worktree.RemoveError{Branch: "a", Output: "wt: worktree has uncommitted changes", Err: errors.New("exit status 1")}
	updated, cmd := m.Update(removeResultMsg{results: []worktree.RemoveResult{
		{Entry: wtEntry("/w/a", "a", 0), Err: err},
	}})
	m = updated.(Model)
	if !m.notifyIsError {
		t.Fatal("want an error notification")
	}
	if !strings.Contains(m.notification, "uncommitted changes") || !strings.Contains(m.notification, "X to force") {
		t.Fatalf("got %q, want wt's message plus the force hint", m.notification)
	}
	if cmd == nil {
		t.Fatal("want the notification's clear scheduled")
	}
}

func TestRemoveResultBatchPartialFailure(t *testing.T) {
	m := New(999, false)
	updated, _ := m.Update(removeResultMsg{results: []worktree.RemoveResult{
		{Entry: wtEntry("/w/a", "a", 0), Err: nil},
		{Entry: wtEntry("/w/b", "b", 0), Err: errors.New("boom")},
		{Entry: wtEntry("/w/c", "c", 0), Err: nil},
	}})
	m = updated.(Model)
	if !m.notifyIsError {
		t.Fatal("want an error notification for a partial failure")
	}
	if !strings.Contains(m.notification, "removed 2 of 3") || !strings.Contains(m.notification, "boom") {
		t.Fatalf("got %q", m.notification)
	}
}

func TestCopyPathToClipboard(t *testing.T) {
	orig := copyText
	t.Cleanup(func() { copyText = orig })
	var got string
	copyText = func(text string) error { got = text; return nil }

	m := New(999, false)
	m.home = "/w"
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})

	updated, cmd := m.Update(key("c"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("want a copy command for a valid selection")
	}
	msg := cmd()
	res, ok := msg.(copyResultMsg)
	if !ok {
		t.Fatalf("got %T, want copyResultMsg", msg)
	}
	if got != "/w/a" {
		t.Fatalf("copied %q, want the full, unshortened path", got)
	}

	updated, cmd = m.Update(res)
	m = updated.(Model)
	if m.notification != "copied ~/a" || m.notifyIsError {
		t.Fatalf("got notification %q (err=%v), want the shortened path", m.notification, m.notifyIsError)
	}
	if cmd == nil {
		t.Fatal("want the notification's clear scheduled")
	}
}

func TestCopyFailureNotifiesAsAnError(t *testing.T) {
	orig := copyText
	t.Cleanup(func() { copyText = orig })
	copyText = func(string) error { return errors.New("pbcopy: boom") }

	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	updated, cmd := m.Update(key("c"))
	m = updated.(Model)
	updated, _ = m.Update(cmd().(copyResultMsg))
	m = updated.(Model)
	if !m.notifyIsError || !strings.Contains(m.notification, "copy failed") {
		t.Fatalf("got notification %q (err=%v)", m.notification, m.notifyIsError)
	}
}

func TestToggleMainWorktreesAtRuntime(t *testing.T) {
	main := wtEntry("/w/main", "main", 0)
	main.IsMain = true
	feature := wtEntry("/w/f", "f", time.Minute)
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{main, feature})
	if len(m.displayedWorktrees()) != 1 {
		t.Fatal("want main hidden by default")
	}

	updated, cmd := m.Update(key("m"))
	m = updated.(Model)
	if !m.showMain {
		t.Fatal("want showMain flipped on")
	}
	if len(m.displayedWorktrees()) != 2 {
		t.Fatal("want main shown after m")
	}
	if cmd == nil {
		t.Fatal("want the notification's clear scheduled")
	}

	updated, _ = m.Update(key("m"))
	m = updated.(Model)
	if m.showMain {
		t.Fatal("want showMain flipped back off")
	}
	if len(m.displayedWorktrees()) != 1 {
		t.Fatal("want main hidden again after a second m")
	}
}

func TestHelpOverlayOpensRendersAndCloses(t *testing.T) {
	m := New(999, false)
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})

	updated, _ := m.Update(key("?"))
	m = updated.(Model)
	if !m.helpOpen {
		t.Fatal("want helpOpen after ?")
	}
	view := m.View()
	if !strings.Contains(view, "keybindings") || !strings.Contains(view, "force remove") {
		t.Fatalf("View() = %q, want the help overlay", view)
	}

	// q closes the overlay rather than quitting while it's open.
	updated, _ = m.Update(key("q"))
	m = updated.(Model)
	if m.helpOpen {
		t.Fatal("want q to close the overlay")
	}
	if m.quitting {
		t.Fatal("want q to close help, not quit, while help is open")
	}
	if strings.Contains(m.View(), "keybindings") {
		t.Fatal("want the table back once help closes")
	}
}

func TestMouseDragIsIgnoredWhileAPromptIsOpen(t *testing.T) {
	m := New(999, false)
	m.width, m.height = 150, 40
	m.resize()
	m.applyWorktrees([]worktree.Entry{wtEntry("/w/a", "a", 0)})
	updated, _ := m.Update(key("x"))
	m = updated.(Model)

	cols := m.table.Columns()
	_, originY := m.renderHeader()
	borderX := mergeBorderX(cols)
	updated, _ = m.Update(tea.MouseMsg{X: borderX, Y: originY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{X: borderX + 4, Y: originY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(Model)

	if got := m.table.Columns()[colMerge].Width; got != cols[colMerge].Width {
		t.Fatalf("Merge width = %d, want unchanged %d while the prompt is open", got, cols[colMerge].Width)
	}
}
