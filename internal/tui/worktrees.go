package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luiul/dashkit/loam"
	"github.com/luiul/dashkit/sieve"
	"github.com/luiul/dashkit/trellis"
	"github.com/luiul/understory/internal/worktree"
)

// Readable widths leave room between each header title and its border.
// Repo, Branch, and Path share surplus space after plain content fits.
const (
	repoColWidth     = 16
	branchColWidth   = 20
	createdColWidth  = 8
	worktreeColWidth = 9
	mergeColWidth    = 9
	vscodeColWidth   = 8
	minPathWidth     = 20
	hardMinColWidth  = 8
)

// Hard floors apply to automatic layouts and mouse drags alike.
// Worktree needs six cells for "parked", not just five for "clean".
const (
	createdContentWidth  = 6
	worktreeContentWidth = 6
	mergeContentWidth    = 8
	vscodeContentWidth   = 4
)

// Column indexes into both worktreeColumns' return value and each
// buildWorktreeRows row, in display order. colorizeRows (see colorize.go)
// uses colWorktree/colMerge to recolor those two columns post-render and
// colBranch for the mismatch suffix's segment coloring; the VS Code
// column renders in the default plain style, like Created/Path. There's no dedicated cursor column: see loam.Sentinel's
// doc (loam pkg) for how the selected row is identified instead now that
// the whole row is highlighted (colorize.go) rather than a leading
// marker glyph.
const (
	colRepo = iota
	colBranch
	colCreated
	colWorktree
	colMerge
	colVSCode
	colPath
)

// repoColumnWidth is the Repo column's width for the given worktree set:
// repoColWidth's floor, or the longest displayed repo label's own display
// width if that's wider. Every entry is measured, not just the ones whose
// label actually renders (buildWorktreeRows blanks a repeated label within
// a group), since the un-blanked first row of that same group still needs
// room for it. ContentWidth uses the table's display cells, not bytes.
func repoColumnWidth(worktrees []worktree.Entry) int {
	width := repoColWidth
	for _, w := range worktrees {
		if lw := trellis.ContentWidth(repoLabel(w)); lw > width {
			width = lw
		}
	}
	return width
}

// branchLabel is the Branch column's cell text for one worktree: the
// branch name, plus the path's branch segment for a Mismatch row (a
// worktree sitting at another branch's path, e.g. its directory was
// created for that branch and later `git switch`ed by hand). Without the
// segment, a scan for the path's own branch finds no row: the worktree
// is keyed by its checked-out branch, and the branch the path names
// looks like it has no worktree at all. The trailing slash marks the
// segment as a directory name, not a second branch.
func branchLabel(w worktree.Entry) string {
	if !w.Mismatch || w.Path == "" {
		return w.Branch
	}
	return fmt.Sprintf("%s @ %s/", w.Branch, filepath.Base(filepath.Dir(w.Path)))
}

// branchColumnWidth is the Branch column's width for the given worktree
// set: branchColWidth's floor, or the longest branch label's own display
// width if that's wider (same reasoning as repoColumnWidth: a fixed
// width truncated long branch names instead of showing them in full).
// Measured on branchLabel, not the raw branch name, so a mismatch row's
// ' @ <segment>/' suffix is funded too rather than truncated away.
func branchColumnWidth(worktrees []worktree.Entry) int {
	width := branchColWidth
	for _, w := range worktrees {
		if lw := trellis.ContentWidth(branchLabel(w)); lw > width {
			width = lw
		}
	}
	return width
}

// worktreeColumnPolicies measures plain labels before filtering and group blanking.
// Repo and Branch receive content space before Path. Compact fields fit their
// readable defaults or current real values, without row tags or placeholders.
func worktreeColumnPolicies(worktrees []worktree.Entry, home string, now time.Time, vscode map[string]vscodeState) []trellis.ColumnPolicy {
	policies := []trellis.ColumnPolicy{
		{Minimum: repoColWidth, HardMinimum: hardMinColWidth, Preferred: repoColumnWidth(worktrees), Weight: 1, ShrinkPriority: 1},
		{Minimum: branchColWidth, HardMinimum: hardMinColWidth, Preferred: branchColumnWidth(worktrees), Weight: 2, ShrinkPriority: 1},
		{Minimum: createdColWidth, HardMinimum: createdContentWidth, Preferred: createdColWidth},
		{Minimum: worktreeColWidth, HardMinimum: worktreeContentWidth, Preferred: worktreeColWidth},
		{Minimum: mergeColWidth, HardMinimum: mergeContentWidth, Preferred: mergeColWidth},
		{Minimum: vscodeColWidth, HardMinimum: vscodeContentWidth, Preferred: vscodeColWidth},
		{Minimum: minPathWidth, HardMinimum: hardMinColWidth, Preferred: minPathWidth, Weight: 2, ShrinkPriority: 2},
	}
	for _, w := range worktrees {
		labels := []string{
			humanizeSince(now.Sub(w.CreatedTime)),
			worktreeStatusLabel(w),
			mergeStatusLabel(w),
			vscodeCell(vscode[w.Path]),
			shortenHome(w.Path, home),
		}
		for i, label := range labels {
			col := colCreated + i
			policies[col].Preferred = max(policies[col].Preferred, trellis.ContentWidth(label))
		}
	}
	return policies
}

// worktreeColumns delegates both automatic sizing and manual proportions to trellis.
// The caller shows a warning when even the hard floors cannot fit.
func worktreeColumns(width int, policies []trellis.ColumnPolicy, preferences trellis.Preferences) ([]table.Column, bool) {
	cols := []table.Column{
		{Title: "Repo"},
		{Title: "Branch"},
		{Title: "Created"},
		{Title: "Worktree"},
		{Title: "Merge"},
		{Title: "VS Code"},
		{Title: "Path"},
	}
	widths, fits := preferences.Allocate(width, policies)
	return trellis.Apply(cols, widths), fits
}

// columnMinWidths matches the policy hard floors, including "parked".
func columnMinWidths() []int {
	return []int{
		hardMinColWidth,
		hardMinColWidth,
		createdContentWidth,
		worktreeContentWidth,
		mergeContentWidth,
		vscodeContentWidth,
		hardMinColWidth,
	}
}

// sortWorktrees orders worktrees for display: grouped so every worktree
// of the same repo (by repoLabel) sits together in one contiguous block,
// rather than interleaved purely by recency, which scattered a repo's
// branches throughout the list and made repos sharing the same fallback
// label (see worktree.Entry doc on Owner/Repo) look like repeated,
// ungrouped duplicates. Blocks are themselves ordered by their own most
// recently committed worktree, and rows within a block are ordered by
// recency too, so "most recently active first" still holds at both
// levels. Does not mutate worktrees.
func sortWorktrees(worktrees []worktree.Entry) []worktree.Entry {
	var order []string
	groups := map[string][]worktree.Entry{}
	for _, w := range worktrees {
		key := repoLabel(w)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], w)
	}

	for _, key := range order {
		g := groups[key]
		sort.SliceStable(g, func(i, j int) bool {
			return g[i].CommitTime.After(g[j].CommitTime)
		})
	}

	// Each group is already sorted most-recent-first, so its own first
	// entry's CommitTime is the group's most recent commit.
	sort.SliceStable(order, func(i, j int) bool {
		return groups[order[i]][0].CommitTime.After(groups[order[j]][0].CommitTime)
	})

	sorted := make([]worktree.Entry, 0, len(worktrees))
	for _, key := range order {
		sorted = append(sorted, groups[key]...)
	}
	return sorted
}

// visibleWorktrees is the row set before any text filter: every known
// worktree, grouped by repo and most-recently-committed first (see
// sortWorktrees), with each repo's main worktree (Entry.IsMain, the base
// branch checkout `wt`/coppice created the others alongside) dropped
// unless m.showMain is set. Hiding it by default keeps the view focused
// on the worktrees actually being worked in: main rarely has anything to
// do (see worktreeSummaryLine/buildWorktreeRows' "-" Merge cell for it),
// and having it always take the first row of every repo's block was
// mostly just noise.
//
// This, not displayedWorktrees, is the set worktreeColumns sizes Repo/
// Branch against: those columns grow to fit their widest displayed value
// (see repoColumnWidth/branchColumnWidth), and the text filter changes
// the displayed set on every keystroke — sizing off it would resize the
// whole table under the user's fingers while typing a query.
func (m Model) visibleWorktrees() []worktree.Entry {
	sorted := sortWorktrees(m.worktrees)
	if m.showMain {
		return sorted
	}
	filtered := make([]worktree.Entry, 0, len(sorted))
	for _, w := range sorted {
		if w.IsMain {
			continue
		}
		filtered = append(filtered, w)
	}
	return filtered
}

// displayedWorktrees is the view's current row set: visibleWorktrees,
// plus the fuzzy text filter (filterQuery, see
// github.com/luiul/dashkit/sieve) while one is applied. m.worktrees
// itself always holds the full polled set, so polls and the
// confirmation prompts' revalidation never see the filter; what renders
// and what the cursor can land on is filtered, and the summary line's
// counts follow the displayed set too (see summaryLine) — a filtered
// view gets filtered counts, and main-hiding stays visible there via
// the "(+N main hidden)" suffix so the total stays comparable with
// `cop list`. Only column sizing deliberately looks past the filter
// (see visibleWorktrees).
func (m Model) displayedWorktrees() []worktree.Entry {
	visible := m.visibleWorktrees()
	if m.filterQuery == "" {
		return visible
	}
	filtered := make([]worktree.Entry, 0, len(visible))
	for _, w := range visible {
		if sieve.Match(m.filterQuery, filterCells(w, m.home, m.vscode)...) {
			filtered = append(filtered, w)
		}
	}
	return filtered
}

// filterCells are the cell strings a filterQuery is matched against (see
// github.com/luiul/dashkit/sieve): the row's stable text columns, which
// are Repo, Branch (mismatch suffix included, so the path's own branch
// segment finds the row too), the Worktree and Merge status words, the
// VS Code state word, and the shortened Path. Created is deliberately
// excluded: it ticks over under the user's fingers ("12s" becomes
// "13s"), so a row would match-or-not from one poll to the next for
// reasons invisible in the query.
func filterCells(w worktree.Entry, home string, vscode map[string]vscodeState) []string {
	return []string{
		repoLabel(w),
		branchLabel(w),
		worktreeStatusLabel(w),
		mergeStatusLabel(w),
		vscodeCell(vscode[w.Path]),
		shortenHome(w.Path, home),
	}
}

// resolveWorktreeCursor finds path's index in displayed, or falls back to
// fallback (clamped): "prefer the same key, else keep roughly where you
// were" resolution for a fresh poll that may have reordered rows.
func resolveWorktreeCursor(displayed []worktree.Entry, path string, fallback int) int {
	cursor := fallback
	if path != "" {
		for i, w := range displayed {
			if w.Path == path {
				cursor = i
				break
			}
		}
	}
	return clampCursor(cursor, len(displayed))
}

// applyWorktrees stores a fresh worktree poll in which every repo
// succeeded — the shape applyPollResults reduces to when nothing failed
// and per-repo identity doesn't matter. Tests drive this shorthand
// directly; production polls go through applyPollResults (see the
// pollResultMsg case in Update).
func (m *Model) applyWorktrees(fresh []worktree.Entry) {
	previousPath := m.selectedPath() // against the OLD set, before the swap
	m.worktrees = fresh
	m.pollsLanded++
	m.pollFailures = 0
	m.revalidateConfirm(fresh)
	m.redisplay(previousPath)
}

// mergeRepoRows folds one repo's poll outcome into the worktree set: a
// successful poll replaces that repo's entries wholesale (including
// down to zero — a worktree removed elsewhere must disappear), while a
// failed one keeps its last-known entries, so one slow repo's rows no
// longer flicker out and back between polls (issue #7). Entry order
// within m.worktrees is NOT preserved (a successful repo's entries move
// to the back); nothing consumes the set unsorted — visibleWorktrees
// always goes through sortWorktrees.
func (m *Model) mergeRepoRows(r worktree.RepoResult) {
	if r.Err != nil {
		return // keep last-known
	}
	kept := make([]worktree.Entry, 0, len(m.worktrees))
	for _, e := range m.worktrees {
		if e.RepoPath != r.RepoPath {
			kept = append(kept, e)
		}
	}
	m.worktrees = append(kept, r.Entries...)
}

// applyStreamedRepo folds one streamed repo result into the view
// (issue #8's progressive half: every repo renders the moment it polls,
// not when the slowest one does): the row merge (see mergeRepoRows),
// fresh window states for its entries when this poll's snapshot already
// arrived (a repo landing BEFORE the snapshot keeps last-known states;
// applySnapToPolledRepos catches those up when it lands), confirmation
// revalidation, and a redisplay preserving the current selection.
func (m *Model) applyStreamedRepo(r worktree.RepoResult, snap vscodeSnapshot) {
	previousPath := m.selectedPath()
	m.mergeRepoRows(r)
	if r.Err == nil && snap != nil {
		m.applyRepoVSCode(r.Entries, snap)
	}
	m.revalidateConfirm(m.worktrees)
	m.redisplay(previousPath)
}

// applyRepoVSCode sets fresh window states for one successfully polled
// set of entries from the poll's snapshot, the same keep-nothing-back
// answers vscodeStates/vscodeStrictStates compute for the batch path:
// a failed listing marks every entry vscodeUnknown rather than a wrong
// "-". States for paths no longer present are NOT pruned here:
// finishPoll prunes both maps to the current row set at the end of
// every poll.
func (m *Model) applyRepoVSCode(entries []worktree.Entry, snap vscodeSnapshot) {
	if m.vscode == nil {
		m.vscode = map[string]vscodeState{}
	}
	if m.vscodeStrict == nil {
		m.vscodeStrict = map[string]bool{}
	}
	for _, e := range entries {
		if snap.Err() != nil {
			m.vscode[e.Path] = vscodeUnknown
			continue
		}
		if snap.IsOpen(e.Path) {
			m.vscode[e.Path] = vscodeOpen
		} else {
			m.vscode[e.Path] = vscodeClosed
		}
		m.vscodeStrict[e.Path] = snap.IsOpenOnWorktree(e.Path)
	}
}

// reconcileMembership folds one in-process membership probe (see
// worktree.Probe) into the row set: a probed path with no row gets a
// skeleton entry (Branch/Path/Created known from the repo's admin dir,
// everything else "…" until the targeted poll lands), and a non-main
// row whose admin dir is gone drops out immediately — the admin dir IS
// git's own worktree registration, so its absence is as authoritative
// as `git worktree list`. Repos the probe couldn't read (absent from
// probed) are left strictly alone: a missing probe answer is
// "unknown", never "empty", the same keep-last-known rule
// mergeRepoRows follows for a failed poll (issue #7). Main worktree
// rows are never touched: the probe reports linked worktrees only.
// Returns the repos whose membership changed, sorted, for the targeted
// poll the caller then triggers.
func (m *Model) reconcileMembership(probed map[string][]worktree.ProbeEntry) []string {
	if len(probed) == 0 {
		return nil
	}
	previousPath := m.selectedPath()
	changed := map[string]bool{}

	// Drop rows whose on-disk registration vanished.
	kept := make([]worktree.Entry, 0, len(m.worktrees))
	for _, e := range m.worktrees {
		entries, ok := probed[e.RepoPath]
		if !ok || e.IsMain {
			kept = append(kept, e)
			continue
		}
		found := false
		for _, pe := range entries {
			if pe.Path == e.Path {
				found = true
				break
			}
		}
		if found {
			kept = append(kept, e)
		} else {
			changed[e.RepoPath] = true
		}
	}
	m.worktrees = kept

	// Insert skeletons for probed paths no row covers.
	existing := map[string]map[string]bool{}
	for _, e := range m.worktrees {
		if existing[e.RepoPath] == nil {
			existing[e.RepoPath] = map[string]bool{}
		}
		existing[e.RepoPath][e.Path] = true
	}
	for repo, entries := range probed {
		for _, pe := range entries {
			if existing[repo][pe.Path] {
				continue
			}
			changed[repo] = true
			if pe.Branch == "" {
				// Detached HEAD: no branch label to render, so no
				// skeleton — but membership did change, so the
				// targeted poll still runs and lets wt classify it.
				continue
			}
			m.worktrees = append(m.worktrees, skeletonEntry(repo, pe, m.worktrees))
		}
	}

	if len(changed) == 0 {
		return nil
	}
	m.revalidateConfirm(m.worktrees)
	m.redisplay(previousPath)
	repos := make([]string, 0, len(changed))
	for repo := range changed {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos
}

// skeletonEntry builds the probe-inserted placeholder row for a
// worktree no poll has reported on yet (see worktree.Entry.Skeleton):
// RepoPath/Branch/Path/CreatedTime from the probe, Owner/Repo copied
// from the repo's existing rows so its group label matches its
// siblings (falling back to the repo dir's basename for a repo with no
// rows yet, the same rule applyRepoFallback uses), and a zero
// CommitTime — which sorts the skeleton last in its repo group (see
// sortWorktrees) until the targeted poll lands its real commit info.
func skeletonEntry(repoPath string, pe worktree.ProbeEntry, entries []worktree.Entry) worktree.Entry {
	var owner, repo string
	for _, e := range entries {
		if e.RepoPath == repoPath && (e.Owner != "" || e.Repo != "") {
			owner, repo = e.Owner, e.Repo
			break
		}
	}
	if owner == "" && repo == "" {
		repo = filepath.Base(filepath.Clean(repoPath))
	}
	return worktree.Entry{
		Owner:       owner,
		Repo:        repo,
		Branch:      pe.Branch,
		Path:        pe.Path,
		RepoPath:    repoPath,
		CreatedTime: pe.CreatedTime,
		Skeleton:    true,
	}
}

// unionStrings appends the items of b missing from a, preserving order.
func unionStrings(a, b []string) []string {
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			a = append(a, s)
			seen[s] = true
		}
	}
	return a
}

// applySnapToPolledRepos applies a late-arriving window snapshot to the
// repos that already landed successfully before it (the common case is
// the reverse: the snapshot wins the race against every repo and each
// result applies it as it lands, see applyStreamedRepo). Repos whose
// poll failed keep last-known states, the same keep-last-known rule the
// rows follow.
func (m *Model) applySnapToPolledRepos() {
	if m.poll == nil || m.poll.snap == nil {
		return
	}
	previousPath := m.selectedPath()
	var entries []worktree.Entry
	for _, e := range m.worktrees {
		if m.poll.okRepos[e.RepoPath] {
			entries = append(entries, e)
		}
	}
	m.applyRepoVSCode(entries, m.poll.snap)
	m.redisplay(previousPath)
}

// finishPoll closes out a poll, streamed or batched: rows of repos that
// weren't polled at all this cycle are pruned (a repo dropped from the
// registry mid-poll was only ever authoritative while registered), the
// window-state maps are pruned to the surviving rows (states for
// deleted worktrees never linger), the failure count updates, the
// merged view is cached for the next launch's warm start (issue #8),
// and the user is notified on the 0→N failure transition only — repos
// staying unreachable are already visible in the summary line's suffix
// on every render, so re-notifying every poll would drown the status
// line.
//
// A TARGETED poll (issue #10: a probe-triggered poll over a subset of
// repos, see targetedPollOnce) is different on two counts: nothing gets
// pruned for being unpolled (the repos it didn't cover are healthy,
// just out of scope), and the full poll's failure bookkeeping
// (m.pollFailures, the 0→N notification) is left alone — the count
// always describes the last FULL poll, and a one-repo targeted failure
// must neither fake a repo-wide outage nor clear a real one.
func (m *Model) finishPoll(failures int, polled map[string]bool, targeted bool) tea.Cmd {
	previousPath := m.selectedPath()
	if !targeted {
		kept := m.worktrees[:0]
		for _, e := range m.worktrees {
			if polled[e.RepoPath] {
				kept = append(kept, e)
			}
		}
		m.worktrees = kept
	}
	live := make(map[string]bool, len(m.worktrees))
	for _, e := range m.worktrees {
		live[e.Path] = true
	}
	for path := range m.vscode {
		if !live[path] {
			delete(m.vscode, path)
		}
	}
	for path := range m.vscodeStrict {
		if !live[path] {
			delete(m.vscodeStrict, path)
		}
	}
	prevFailures := m.pollFailures
	if !targeted {
		m.pollFailures = failures
	}
	m.revalidateConfirm(m.worktrees)
	m.redisplay(previousPath)
	savePollCache(m.worktrees, m.vscode, m.vscodeStrict)
	if !targeted && prevFailures == 0 && m.pollFailures > 0 {
		return m.notify(repoCountLabel(m.pollFailures)+" timed out or errored; keeping last-known rows", true)
	}
	return nil
}

// applyPollResults merges one batched poll's per-repo results (the
// pre-streaming shape, kept for tests and one-shot callers; the
// production poll path is the streamed pollStartedMsg → repoResultMsg
// → pollDoneMsg sequence in app.go, built from the same primitives).
// Rows merge per repo exactly as the streamed path does (see
// mergeRepoRows); the window-state maps arrive computed for the whole
// poll and swap in wholesale, with last-known states kept for failed
// repos — they arrive covering only the successful repos, so a failed
// repo's kept rows would otherwise render "?" for a poll, the same
// flicker the row merge exists to kill.
func (m *Model) applyPollResults(msg pollResultMsg) tea.Cmd {
	failed := map[string]bool{}
	polled := map[string]bool{}
	for _, r := range msg.results {
		polled[r.RepoPath] = true
		if r.Err != nil {
			failed[r.RepoPath] = true
			continue
		}
		m.mergeRepoRows(r)
	}

	if msg.vscode == nil {
		msg.vscode = map[string]vscodeState{}
	}
	if msg.vscodeStrict == nil {
		msg.vscodeStrict = map[string]bool{}
	}
	for _, e := range m.worktrees {
		if !failed[e.RepoPath] {
			continue
		}
		if state, ok := m.vscode[e.Path]; ok {
			msg.vscode[e.Path] = state
		}
		if strict, ok := m.vscodeStrict[e.Path]; ok {
			msg.vscodeStrict[e.Path] = strict
		}
	}
	m.vscode, m.vscodeStrict = msg.vscode, msg.vscodeStrict

	// The batch path is only ever a full poll (tests and one-shot
	// callers); the targeted subset shape exists solely in the streamed
	// path (see targetedPollCmd).
	return m.finishPoll(len(failed), polled, false)
}

// repoCountLabel renders "1 repo" / "N repos" for the poll-health
// messages (the placeholder, the summary line's unreachable suffix, the
// 0→N transition notification).
func repoCountLabel(n int) string {
	if n == 1 {
		return "1 repo"
	}
	return fmt.Sprintf("%d repos", n)
}

// placeholder is the empty table's message, honest about WHY it's empty
// (issue #7: the view used to claim "no known worktrees" for the whole
// first poll's duration, and again whenever every repo's poll timed out
// in the same cycle): the first poll still in flight ("loading
// worktrees…"), the last poll failed for one or more repos and there is
// nothing — fresh or kept — to show ("couldn't poll…"), or the poll
// genuinely found nothing (noWorktreesMessage). An active filter's own
// no-match message (see buildWorktreeRows) wins over all of these.
func (m Model) placeholder() string {
	if m.pollsLanded == 0 {
		return "loading worktrees…"
	}
	if m.pollFailures > 0 && len(m.worktrees) == 0 {
		return fmt.Sprintf("couldn't poll worktrees (%s timed out or errored); r retries", repoCountLabel(m.pollFailures))
	}
	return noWorktreesMessage()
}

// summaryLine is the header's one-line worktree breakdown
// (worktreeSummaryLine over the displayed set), plus the two caveats
// that keep its count honest against what the user can list elsewhere:
// "(+N main hidden)" while main worktrees are hidden (see
// visibleWorktrees), so the total stays comparable with `cop list`'s;
// and "N repos unreachable" while any repo's poll is failing, since a
// failed repo's rows (and counts) are last-known (see applyPollResults).
func (m Model) summaryLine() string {
	summary := worktreeSummaryLine(m.displayedWorktrees())
	if summary == "" {
		return ""
	}
	if !m.showMain {
		mains := 0
		for _, w := range m.worktrees {
			if w.IsMain {
				mains++
			}
		}
		if mains > 0 {
			summary += subtleStyle.Render(fmt.Sprintf(" · (+%d main hidden)", mains))
		}
	}
	// A warm-started view (see New's cache seed) shows last session's
	// data while the first poll is still landing; say so. Cold starts
	// hit this too once the first streamed repo lands mid-poll — equally
	// true there.
	if m.pollsLanded == 0 && m.pollInFlight && len(m.worktrees) > 0 {
		summary += subtleStyle.Render(" · refreshing…")
	}
	if m.pollFailures > 0 {
		summary += subtleStyle.Render(" · " + repoCountLabel(m.pollFailures) + " unreachable")
	}
	return summary
}

// selectedPath returns the path of the currently selected row, or "" if
// there are none showing.
func (m Model) selectedPath() string {
	displayed := m.displayedWorktrees()
	idx := clampCursor(m.table.Cursor(), len(displayed))
	if idx < 0 || idx >= len(displayed) {
		return ""
	}
	return displayed[idx].Path
}

// redisplay preserves selection by path and refreshes rows after a poll or filter.
// Automatic layouts fit new content. Manual layouts keep their proportions.
// An active drag freezes columns until release, even when new rows arrive.
// The caller captures previousPath before changing the worktree set.
func (m *Model) redisplay(previousPath string) {
	oldCursor := clampCursor(m.table.Cursor(), len(m.displayedWorktrees()))

	newDisplayed := m.displayedWorktrees()
	m.cursor = resolveWorktreeCursor(newDisplayed, previousPath, oldCursor)

	// Size before the text filter so typing cannot move column borders.
	m.allocateColumns()
	m.table.SetRows(buildWorktreeRows(newDisplayed, m.cursor, m.home, m.pathWidth(), time.Now(), m.vscode, m.filterQuery, m.placeholder()))
	m.table.SetCursor(m.cursor)
}

// pathWidth is the Path column's current content width, handed to
// buildWorktreeRows so pathCellText can pre-truncate paths to it. 0 when
// the table isn't built with understory's column set yet (buildWorktreeRows
// treats it as "no truncation").
func (m Model) pathWidth() int {
	cols := m.table.Columns()
	if colPath >= len(cols) {
		return 0
	}
	return cols[colPath].Width
}

// selectedWorktree returns the worktree.Entry backing the currently
// highlighted row (table.Cursor(), the live ground truth), or ok=false if
// there are none showing (e.g. only the placeholder row).
func (m Model) selectedWorktree() (worktree.Entry, bool) {
	displayed := m.displayedWorktrees()
	if len(displayed) == 0 {
		return worktree.Entry{}, false
	}
	idx := m.table.Cursor()
	if idx < 0 || idx >= len(displayed) {
		return worktree.Entry{}, false
	}
	return displayed[idx], true
}

// vscodeState is the VS Code column's per-worktree tri-state: whether
// a VS Code window is currently open on the worktree, answered by
// mycelium's read-only snapshot once per poll (see vscodeStates). The
// same match cascade Enter's open-or-focus runs backs it, so "open"
// means Enter would focus an existing window rather than open a new
// one.
type vscodeState int

const (
	// vscodeUnknown is the zero value on purpose: a worktree missing
	// from the poll's map (or a poll whose window listing failed, most
	// likely because the vscode-window-registry extension isn't
	// installed or its directory is unreadable) renders "?" — the
	// listing can't claim "not open", so the cell never does either.
	vscodeUnknown vscodeState = iota
	vscodeClosed              // checked, no window open on this worktree
	vscodeOpen                // a window is open on this worktree
)

// vscodeCell renders the VS Code column's plain-word cell: "open", "-"
// for none (the Merge column's own not-applicable word), "?" for
// unknown (canopy's Location column's own convention).
func vscodeCell(state vscodeState) string {
	switch state {
	case vscodeOpen:
		return "open"
	case vscodeUnknown:
		return "?"
	default:
		return "-"
	}
}

// buildWorktreeRows constructs the view's rows from an already-sorted
// (see sortWorktrees) worktree list. cursor picks which row gets tagged
// with loam.Sentinel (see loam pkg doc) so colorize.go's
// colorizeRows knows to highlight that row's whole line; there's no
// dedicated cursor column/glyph to place it in any more, now that the
// row highlight itself is the selection indicator. placeholder is the
// message the empty table's single row shows (see Model.placeholder for
// which message and why). vscode carries the latest known per-path VS
// Code window states (see vscodeStates and applyPollResults); a missing
// entry renders as vscodeUnknown.
//
// The Repo cell is only printed on the first row of each repo's block:
// sortWorktrees already guarantees every worktree of the same repo is
// contiguous, so repeating the same label down every one of its rows
// added nothing but visual noise (and, worse, made same-labeled but
// unrelated repos look identical); blanking the repeat turns "first row
// of a block has a label, the rest don't" into the separator between one
// repo's block and the next.
//
// Worktree/Merge replace the single opaque Status glyph column (wt's own
// compact "^|", "c▶", "!⚑" symbols, still available on Entry.Symbols for
// anyone piping raw output, but not self-explanatory in this view) with
// the same two plain-word signals coppice's own worktree table shows:
// whether the working tree itself is dirty/clean/stale, and separately
// whether the branch has been merged into main yet.
func buildWorktreeRows(worktrees []worktree.Entry, cursor int, home string, pathWidth int, now time.Time, vscode map[string]vscodeState, filterQuery, placeholder string) []table.Row {
	if len(worktrees) == 0 {
		// An active filter with zero matches says so (and how to back
		// out) rather than claiming there are no worktrees at all — the
		// unfiltered message would be a lie about why the table is
		// empty.
		if filterQuery != "" {
			return []table.Row{{"", "", "", "", "", "", fmt.Sprintf("no worktrees match filter %q (esc clears)", filterQuery)}}
		}
		return []table.Row{{"", "", "", "", "", "", placeholder}}
	}

	rows := make([]table.Row, len(worktrees))
	for i, w := range worktrees {
		label := repoLabel(w)
		if i > 0 && label == repoLabel(worktrees[i-1]) {
			label = ""
		}
		created := humanizeSince(now.Sub(w.CreatedTime))
		if worktreeStatusLabel(w) == "parked" {
			// The row's grey-out tag, for greyOutParkedRows (colorize.go):
			// keyed on the same condition the Worktree column's own
			// "parked" word is (stale wins over parked there, so a
			// stale-but-marked row renders as a removal candidate, not
			// greyed out — the same precedence coppice's `not stale and
			// parked` check follows). Prepended for the same truncation-
			// survival reason as the cursor tag below.
			created = parkedMarker + created
		}
		if i == cursor {
			// Prepended, not appended: bubbles/table truncates a
			// too-long cell from the tail (runewidth.Truncate keeps the
			// head + an ellipsis), so a leading zero-width tag always
			// survives regardless of how long the cell's real content
			// is, where a trailing one could get truncated away along
			// with the tail. Created's own content is always short
			// (humanizeSince, e.g. "12s"/"3d") and never truncated in
			// practice, but the tag's placement is written to hold even
			// if that ever changed.
			created = loam.Sentinel + created
		}
		rows[i] = table.Row{
			label,
			branchLabel(w),
			created,
			worktreeStatusLabel(w),
			mergeStatusLabel(w),
			vscodeCell(vscode[w.Path]),
			pathCellText(w.Path, home, pathWidth),
		}
	}
	return rows
}

// pathCellText is the Path column's cell: the ~-shortened path
// pre-truncated to the column's current width keeping the TAIL
// (loam.TruncateHead — "…speed-up-ci/global-ops", not "~/worktrees/hello…"):
// the head is the same prefix on nearly every row, while the tail is
// what identifies the worktree. Pre-truncation is what makes the cut
// point controllable at all (bubbles/table's own truncation keeps the
// head); the width comes from the live column via buildWorktreeRows,
// rebuilt on every poll, resize, and drag. Width allocation still
// measures the full label (see worktreeColumnPolicies), and the filter
// still matches the full path (see filterCells).
func pathCellText(path, home string, width int) string {
	return loam.TruncateHead(shortenHome(path, home), width)
}

// worktreeStatusLabel is the Worktree column's plain-word rendering of
// Entry's working-tree health: "stale" (see Entry.Stale's doc) takes
// priority over everything, since a prunable worktree's uncommitted-
// changes state is meaningless once its directory is already gone. A
// parked worktree (Entry.Parked: coppice's task-complete mark, with no
// follow-up since) shows "parked" ahead of dirty/clean too: the mark is
// the stronger signal (one visual state per row, the same precedence
// coppice's own dimmed parked rows follow), and a dirty parked one was
// already acknowledged when it got parked.
func worktreeStatusLabel(w worktree.Entry) string {
	if w.Skeleton {
		return "…" // probe-inserted, not yet polled (see Entry.Skeleton)
	}
	if w.Stale {
		return "stale"
	}
	if w.Parked() {
		return "parked"
	}
	if w.Dirty {
		return "dirty"
	}
	return "clean"
}

// mergeStatusLabel is the Merge column's rendering of Entry.MergeStatus:
// "-" for its zero value (not applicable to the main worktree, or moot
// for a stale one), the computed label otherwise.
func mergeStatusLabel(w worktree.Entry) string {
	if w.Skeleton {
		return "…" // probe-inserted, not yet polled (see Entry.Skeleton)
	}
	if w.MergeStatus == "" {
		return "-"
	}
	return w.MergeStatus
}

func repoLabel(w worktree.Entry) string {
	if w.Owner != "" {
		return w.Owner + "/" + w.Repo
	}
	return w.Repo
}

// noWorktreesMessage distinguishes "wt (worktrunk) isn't installed" (a
// setup gap, worth naming) from "no worktrees found yet" (e.g. the poll
// just hasn't landed).
func noWorktreesMessage() string {
	if !worktree.Available() {
		return "wt (worktrunk) is not installed; see https://worktrunk.dev"
	}
	return "no known worktrees (registered in ~/.cache/wt/known-repos, or the repo you're standing in)"
}

// worktreeSummaryLine returns a one-line "N worktrees: N dirty · N stale ·
// N parked · N conflict · N merged · N clean · N unknown" breakdown, one
// mutually-exclusive bucket per worktree (most-actionable-first
// classification, same spirit as canopy's own summaryLine over its State
// column, folded down to a single dimension since "needs a look" is
// really one axis here even though it's backed by two Entry fields):
// Stale wins first (see Entry.Stale's doc: Dirty/MergeStatus are
// meaningless once true), then Parked (task-complete and set aside,
// nothing to do unless follow-up arrives — which flips it back to
// active, see Entry.Parked), then Dirty (uncommitted work, worth a look
// now), then a conflicting branch (MergeStatusConflict: the one Merge
// state that gets worse on its own the longer main moves, so it sorts
// ahead of every other merge relationship), then a merged branch
// (nothing left to do but it's a removal candidate), then clean
// (still-open work with nothing outstanding). unknown is its own bucket
// rather than folded into clean: when `wt` couldn't determine a branch's
// relationship to main at all (Entry.MergeStatus's MergeStatusUnknown),
// that's not confidently "clean" (it might be safe to remove, might not,
// wt just doesn't know), so it's counted and labeled separately instead
// of silently overstating certainty. It sorts last since it's exactly as
// low-priority as clean in the Merge column's own coloring (colorize.go's
// mergeStatusStyles gives "unknown" the same dim grey as "-"), just
// without a confirmed merge relationship to back that up. Every
// MergeStatus value needs its own explicit case here: the switch's
// default bucket is clean, so an unhandled status would be summarized
// as "nothing outstanding" when it might be anything but. Colored to
// match the Worktree/Merge table columns via the same style lookups.
// Returns "" if there are no worktrees, since the placeholder row
// already says so.
func worktreeSummaryLine(entries []worktree.Entry) string {
	if len(entries) == 0 {
		return ""
	}

	counts := map[string]int{}
	for _, e := range entries {
		switch {
		case e.Skeleton:
			// Probe-inserted, poll hasn't landed (see Entry.Skeleton):
			// its own honest bucket rather than a wrong "clean".
			counts["probing"]++
		case e.Stale:
			counts["stale"]++
		case e.Parked():
			counts["parked"]++
		case e.Dirty:
			counts["dirty"]++
		case e.MergeStatus == worktree.MergeStatusConflict:
			counts["conflict"]++
		case e.MergeStatus == worktree.MergeStatusMerged:
			counts["merged"]++
		case e.MergeStatus == worktree.MergeStatusUnknown:
			counts["unknown"]++
		default:
			counts["clean"]++
		}
	}

	var parts []string
	for _, bucket := range []string{"dirty", "stale", "parked", "conflict", "merged", "clean", "unknown", "probing"} {
		n := counts[bucket]
		if n == 0 {
			continue
		}
		// merged/conflict/unknown are Merge-column words
		// (mergeStatusStyles), dirty/stale/parked/clean are Worktree-column
		// words (worktreeStatusStyles); looking each up in its own map
		// rather than reusing one for both keeps this tied to the same
		// source of truth the table itself renders from, even though
		// "clean" and "unknown" happen to resolve to the same dim grey
		// today. probing is transient by definition (seconds at most), so
		// it gets the summary line's own quiet grey rather than a status
		// color that would out-shout the real buckets.
		style := worktreeStatusStyle(bucket)
		if bucket == "merged" || bucket == "conflict" || bucket == "unknown" {
			style = mergeStatusStyle(bucket)
		}
		if bucket == "probing" {
			style = subtleStyle
		}
		parts = append(parts, style.Render(fmt.Sprintf("%d %s", n, bucket)))
	}

	label := "worktrees"
	if len(entries) == 1 {
		label = "worktree"
	}
	return subtleStyle.Render(fmt.Sprintf("%d %s: ", len(entries), label)) +
		strings.Join(parts, subtleStyle.Render(" · "))
}
