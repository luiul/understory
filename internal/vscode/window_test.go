package vscode

import "testing"

func TestMatchWindowTitleMatchesTheBareFolderName(t *testing.T) {
	title, ok := matchWindowTitle([]string{"canopy — main", "understory"}, "/x/understory")
	if !ok || title != "understory" {
		t.Fatalf("got (%q, %v), want (\"understory\", true)", title, ok)
	}
}

func TestMatchWindowTitleMatchesFolderNamePlusBranchSuffix(t *testing.T) {
	title, ok := matchWindowTitle([]string{"canopy — main", "dotfiles — implement-workmux"}, "/x/dotfiles")
	if !ok || title != "dotfiles — implement-workmux" {
		t.Fatalf("got (%q, %v), want (\"dotfiles — implement-workmux\", true)", title, ok)
	}
}

func TestMatchWindowTitleDoesNotMatchAnUnrelatedLongerName(t *testing.T) {
	// "understory-lab" must not match a search for "understory": the
	// character right after the shared prefix isn't a word boundary.
	_, ok := matchWindowTitle([]string{"understory-lab — main"}, "/x/understory")
	if ok {
		t.Fatalf("want no match, got one")
	}
}

func TestMatchWindowTitleNoMatchWhenNothingIsOpenForThatPath(t *testing.T) {
	_, ok := matchWindowTitle([]string{"canopy — main", "dotfiles — implement-workmux"}, "/x/understory")
	if ok {
		t.Fatalf("want no match, got one")
	}
}

func TestMatchWindowTitleEmptyTitleListNeverMatches(t *testing.T) {
	_, ok := matchWindowTitle(nil, "/x/understory")
	if ok {
		t.Fatalf("want no match against an empty title list")
	}
}

func TestOpenPathsReportsOpenAndClosedSeparately(t *testing.T) {
	titles := []string{"understory — main"}
	got := openPaths(titles, []string{"/x/understory", "/x/canopy"})
	if !got["/x/understory"] {
		t.Fatalf("got %+v, want /x/understory open", got)
	}
	if got["/x/canopy"] {
		t.Fatalf("got %+v, want /x/canopy not open", got)
	}
}

func TestOpenPathsHandlesAnEmptyTitleList(t *testing.T) {
	got := openPaths(nil, []string{"/x/understory"})
	if got["/x/understory"] {
		t.Fatalf("got %+v, want nothing open when there are no windows at all", got)
	}
}
