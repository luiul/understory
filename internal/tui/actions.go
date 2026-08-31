// Keybinding-driven actions on worktrees: the confirmation modal behind
// x/X/P/M (its state machine — answers, auto-cancel timeout, poll
// revalidation — is dashkit's confirm package, shared with canopy), the
// async removal command they dispatch, and the result summarization that
// turns per-entry outcomes into a status-line notification. app.go owns
// the Model and the Update switch; this file owns everything those keys
// set in motion.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luiul/dashkit/confirm"
	"github.com/luiul/understory/internal/worktree"
)

// confirmKind identifies which action a pending confirmation runs on a
// "yes". Both the prompt text and the dispatched command switch on it.
type confirmKind int

const (
	// confirmRemoveOne removes the selected worktree (x): plain
	// `wt remove`, which refuses a dirty worktree and deletes the branch
	// only if merged.
	confirmRemoveOne confirmKind = iota
	// confirmForceOne force-removes the selected worktree (X):
	// `wt remove -f -D`, discarding uncommitted changes and deleting the
	// branch even if unmerged. The "kill" counterpart to x's polite
	// remove.
	confirmForceOne
	// confirmPruneStale drops every displayed stale worktree
	// registration (P): their directories are already gone, so this only
	// prunes git metadata.
	confirmPruneStale
	// confirmRemoveMerged removes every displayed merged worktree of the
	// selected row's repo (M), deleting their (merged) branches too.
	confirmRemoveMerged
)

// confirmState is a pending confirmation's payload (see
// confirm.State). entries holds the targets: exactly one for the
// single-worktree kinds, the whole batch for
// confirmPruneStale/confirmRemoveMerged. Kept as plain data (not a
// closure) so tests can inspect it and polls can revalidate it against
// fresh worktree lists (see revalidateConfirm).
type confirmState struct {
	kind    confirmKind
	entries []worktree.Entry
	repo    string // repo label, used by confirmRemoveMerged's prompt only
}

// removeWorktree is a package-level seam onto worktree.Remove, swapped
// out in tests so app/actions tests can verify dispatch without shelling
// out to `wt`/`git`; same pattern as app.go's openVSCode seam.
var removeWorktree = worktree.Remove

type removeResultMsg struct{ results []worktree.RemoveResult }

// removeCmd removes entries sequentially (each removal runs the repo's
// pre-remove hooks, so a batch fired concurrently would fork a storm of
// hook subprocesses) and reports every outcome in one message.
func removeCmd(entries []worktree.Entry, opts worktree.RemoveOptions) tea.Cmd {
	return func() tea.Msg {
		results := make([]worktree.RemoveResult, len(entries))
		for i, e := range entries {
			results[i] = worktree.RemoveResult{Entry: e, Err: removeWorktree(e, opts)}
		}
		return removeResultMsg{results: results}
	}
}

// startConfirm opens the confirmation modal for kind, computing its
// target set from the current view, and returns the auto-cancel tick.
// When there's nothing to confirm (no selection, a guarded entry, an
// empty batch) no modal opens; the cases where the user needs telling
// why set a notification instead and return its clear command.
func (m *Model) startConfirm(kind confirmKind) tea.Cmd {
	switch kind {
	case confirmRemoveOne, confirmForceOne:
		w, ok := m.selectedWorktree()
		if !ok {
			return nil
		}
		if w.IsMain {
			return m.notify("can't remove a repo's main worktree", true)
		}
		return m.confirm.Arm(confirmState{kind: kind, entries: []worktree.Entry{w}})
	case confirmPruneStale:
		var stale []worktree.Entry
		for _, w := range m.displayedWorktrees() {
			// IsMain excluded: pruning the main checkout's registration
			// from a dashboard is too surprising, even if its directory
			// is gone; that's for the user to do in the repo itself.
			if w.Stale && !w.IsMain {
				stale = append(stale, w)
			}
		}
		if len(stale) == 0 {
			return m.notify("no stale worktrees to prune", false)
		}
		return m.confirm.Arm(confirmState{kind: kind, entries: stale})
	case confirmRemoveMerged:
		w, ok := m.selectedWorktree()
		if !ok {
			return nil
		}
		label := repoLabel(w)
		var merged []worktree.Entry
		for _, e := range m.displayedWorktrees() {
			if repoLabel(e) == label && e.MergeStatus == worktree.MergeStatusMerged && !e.IsMain && !e.Stale {
				merged = append(merged, e)
			}
		}
		if len(merged) == 0 {
			return m.notify("no merged worktrees for "+label, false)
		}
		return m.confirm.Arm(confirmState{kind: kind, entries: merged, repo: label})
	}
	return nil
}

// confirmedCmd builds the command a "yes" answers dispatch.
func confirmedCmd(c *confirmState) tea.Cmd {
	opts := worktree.RemoveOptions{}
	if c.kind == confirmForceOne {
		opts = worktree.RemoveOptions{Force: true, ForceDelete: true}
	}
	return removeCmd(c.entries, opts)
}

// revalidateConfirm keeps a pending confirmation honest across polls:
// rows keep repolling while the prompt is open, so each target a fresh
// poll no longer reports (removed externally) drops out of the batch
// (confirm.Refresh), and a prompt left with nothing to act on cancels
// itself.
func (m *Model) revalidateConfirm(fresh []worktree.Entry) {
	if !m.confirm.Active() {
		return
	}
	alive := make(map[string]bool, len(fresh))
	for _, e := range fresh {
		alive[e.Path] = true
	}
	remaining := confirm.Refresh(m.confirm.Payload.entries, func(e worktree.Entry) (worktree.Entry, bool) {
		return e, alive[e.Path]
	})
	if len(remaining) == 0 {
		m.confirm.Resolve()
		return
	}
	m.confirm.Payload.entries = remaining
}

// confirmPrompt renders the pending confirmation's prompt text. It
// states the consequences using the same signals the row already
// displays (Dirty/Stale/MergeStatus), so the user confirms with full
// knowledge of what removal will do to the branch.
func (m Model) confirmPrompt() string {
	c := m.confirm.Payload
	switch c.kind {
	case confirmRemoveOne, confirmForceOne:
		return m.singleRemovePrompt(c.entries[0], c.kind == confirmForceOne)
	case confirmPruneStale:
		return fmt.Sprintf("Prune %d stale worktree %s? Their directories are already gone; this only drops the git registrations. [y/N]",
			len(c.entries), plural(len(c.entries), "registration", "registrations"))
	case confirmRemoveMerged:
		return fmt.Sprintf("Remove %d merged %s of %s? Their branches will be deleted too. [y/N]",
			len(c.entries), plural(len(c.entries), "worktree", "worktrees"), c.repo)
	}
	return ""
}

func (m Model) singleRemovePrompt(e worktree.Entry, force bool) string {
	path := shortenHome(e.Path, m.home)
	if e.Stale {
		// Force flags are moot here: the directory is already gone, so x
		// and X both just drop the registration. The verb stays "Prune"
		// either way, the same word the batch prompt (P) uses: one
		// operation, one verb.
		return fmt.Sprintf("Prune the stale registration for %s? The directory is already gone; this only prunes the git metadata. [y/N]", path)
	}
	if force {
		return fmt.Sprintf("Force remove worktree %s at %s? Uncommitted changes will be discarded and branch %s deleted even if unmerged. [y/N]", e.Branch, path, e.Branch)
	}
	prompt := fmt.Sprintf("Remove worktree %s at %s?", e.Branch, path)
	switch e.MergeStatus {
	case worktree.MergeStatusMerged:
		prompt += " Branch is merged and will be deleted too."
	case "":
		// No merge relationship to report (shouldn't happen for a
		// non-main, non-stale entry, but say nothing rather than lie).
	default:
		prompt += fmt.Sprintf(" Branch is %s and will be kept.", e.MergeStatus)
	}
	if e.Dirty {
		prompt += " Warning: uncommitted changes, so wt will refuse (X force removes)."
	}
	return prompt + " [y/N]"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// removeSummary builds the notification text for a finished removal
// batch: a single entry gets a sentence of its own (with a force hint
// when wt refused a dirty worktree), a batch a compact count.
func removeSummary(results []worktree.RemoveResult) (text string, isErr bool) {
	failed := 0
	var firstErr error
	for _, r := range results {
		if r.Err != nil {
			failed++
			if firstErr == nil {
				firstErr = r.Err
			}
		}
	}
	if len(results) == 1 {
		if failed == 0 {
			return "removed " + results[0].Entry.Branch, false
		}
		return failureText(firstErr), true
	}
	if failed == 0 {
		return fmt.Sprintf("removed %d worktrees", len(results)), false
	}
	return fmt.Sprintf("removed %d of %d; %s", len(results)-failed, len(results), firstLine(firstErr.Error())), true
}

func failureText(err error) string {
	msg := firstLine(err.Error())
	if looksDirtyRefusal(msg) {
		return msg + " (press X to force remove)"
	}
	return msg
}

// looksDirtyRefusal reports whether wt's refusal reads like the
// uncommitted-changes one, so the notification can point at the X
// keybinding instead of leaving the user to guess why removal failed.
func looksDirtyRefusal(msg string) bool {
	lower := strings.ToLower(msg)
	for _, needle := range []string{"uncommitted", "untracked", "modified", "dirty"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
