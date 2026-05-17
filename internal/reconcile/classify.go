package reconcile

import (
	"strings"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/abs"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
)

// bookState is the result of comparing a libro.fm book against the current
// ABS library snapshot.
type bookState int

const (
	stateNotPresent        bookState = iota // no matching ABS item
	statePresentNoMetadata                  // ABS item exists but key fields missing
	statePresentSynced                      // ABS item exists with sufficient metadata
)

// absIndex is a search-side index over ABS items, built once per run.
type absIndex struct {
	byISBN map[string]abs.LibraryItem
	byFuzz map[string]abs.LibraryItem // key: lower("<title>|<first-author>")
}

func indexABS(items []abs.LibraryItem) absIndex {
	idx := absIndex{
		byISBN: make(map[string]abs.LibraryItem, len(items)),
		byFuzz: make(map[string]abs.LibraryItem, len(items)),
	}
	for _, it := range items {
		if isbn := metaISBN(it); isbn != "" {
			idx.byISBN[isbn] = it
		}
		author := metaFirstAuthor(it)
		// Index under the full title and (if different) the subtitle-
		// stripped title — handles the common case where ABS's scanner
		// pulled "Title - Subtitle" from an embedded ©nam tag while the
		// libro.fm canonical title is just "Title". First write wins.
		for _, t := range titleVariants(metaTitle(it)) {
			key := fuzzKey(t, author)
			if key == "" {
				continue
			}
			if _, dup := idx.byFuzz[key]; !dup {
				idx.byFuzz[key] = it
			}
		}
	}
	return idx
}

// classify returns the bookState for a libro.fm book against the ABS index.
// ISBN match is authoritative; the title+author fuzz catches items the user
// added by hand (no ISBN populated) so we don't re-upload them.
func (idx absIndex) classify(book librofm.Book) (bookState, abs.LibraryItem) {
	if it, ok := idx.byISBN[book.ISBN]; ok {
		if isMetadataComplete(it) {
			return statePresentSynced, it
		}
		return statePresentNoMetadata, it
	}
	firstAuthor := ""
	if len(book.Authors) > 0 {
		firstAuthor = book.Authors[0]
	}
	// Try the libro.fm title as-is, then its subtitle-stripped variant. The
	// index already contains stripped forms of ABS titles, so this covers
	// both directions: libro.fm short title vs ABS long title, and vice
	// versa.
	for _, t := range titleVariants(book.Title) {
		if it, ok := idx.byFuzz[fuzzKey(t, firstAuthor)]; ok {
			return statePresentNoMetadata, it
		}
	}
	return stateNotPresent, abs.LibraryItem{}
}

// titleVariants returns the original title and, if it contains a common
// subtitle separator, the prefix preceding the separator. The result is
// deduplicated and never empty (an empty input returns an empty slice).
func titleVariants(title string) []string {
	t := strings.TrimSpace(title)
	if t == "" {
		return nil
	}
	out := []string{t}
	for _, sep := range []string{" - ", ": ", "; "} {
		if i := strings.Index(t, sep); i > 0 {
			pref := strings.TrimSpace(t[:i])
			if pref != "" && pref != t {
				out = append(out, pref)
			}
		}
	}
	return out
}

// isMetadataComplete is the "good enough, skip" predicate. ISBN is the join
// key for future runs, so its absence is what defines "incomplete".
func isMetadataComplete(it abs.LibraryItem) bool {
	if it.Media.Metadata == nil {
		return false
	}
	if it.Media.Metadata.Isbn == nil || strings.TrimSpace(*it.Media.Metadata.Isbn) == "" {
		return false
	}
	return true
}

// fuzzKey builds the lowercased "<title>|<first-author>" key used by the
// fuzzy fallback index. Returns "" if both inputs are blank.
func fuzzKey(title, firstAuthor string) string {
	t := strings.TrimSpace(strings.ToLower(title))
	a := strings.TrimSpace(strings.ToLower(firstAuthor))
	if t == "" && a == "" {
		return ""
	}
	return t + "|" + a
}

func metaISBN(it abs.LibraryItem) string {
	if it.Media.Metadata == nil || it.Media.Metadata.Isbn == nil {
		return ""
	}
	return strings.TrimSpace(*it.Media.Metadata.Isbn)
}

func metaTitle(it abs.LibraryItem) string {
	if it.Media.Metadata == nil || it.Media.Metadata.Title == nil {
		return ""
	}
	return *it.Media.Metadata.Title
}

// metaFirstAuthor returns the first author from the minified `authorName`
// field. ABS joins multiple authors with ", " in minified mode.
func metaFirstAuthor(it abs.LibraryItem) string {
	if it.Media.Metadata == nil || it.Media.Metadata.AuthorName == nil {
		return ""
	}
	full := *it.Media.Metadata.AuthorName
	if comma := strings.Index(full, ","); comma >= 0 {
		return strings.TrimSpace(full[:comma])
	}
	return strings.TrimSpace(full)
}
