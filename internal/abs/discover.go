package abs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// ErrNotFound is returned by Discover when the polling deadline is reached
// without the new item appearing. Callers can wrap this to surface a clearer
// message ("scanner never picked up the upload").
var ErrNotFound = errors.New("abs: item not found")

// DiscoverInput controls a post-upload polling search.
type DiscoverInput struct {
	LibraryID string
	// Title to match. We pass the same un-uploaded title we sent to
	// /api/upload, which is also what ABS will assign to the item until our
	// subsequent PATCH /media call rewrites it.
	//
	// Title comparison is tolerant: ABS's scanner may overwrite the title
	// from an audio file's embedded tags (often adding a subtitle suffix
	// like " - A Novel"), and the upload-side SafeFileName strips path-
	// unsafe characters (like ":"), so we use a normalised compare instead
	// of strict equality.
	Title string
	// Backoffs is the sleep sequence between polls, e.g. {1s, 2s, 4s, 8s, 16s}.
	// Default is one fixed retry sequence baked in below.
	Backoffs []time.Duration
}

var defaultBackoffs = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
	30 * time.Second,
}

// Discover polls /api/libraries/{id}/search with exponential-ish backoff until
// it sees a library item whose minified metadata.title is a tolerant match for
// the upload's title. Returns ErrNotFound if the deadline is reached without a
// hit, or the context error if the ctx is canceled.
//
// We match by title (not ISBN) because the ISBN isn't populated until we PATCH
// /media in a subsequent step.
func (c *API) Discover(ctx context.Context, in DiscoverInput) (LibraryItem, error) {
	if in.LibraryID == "" || in.Title == "" {
		return LibraryItem{}, errors.New("abs.Discover: LibraryID and Title are required")
	}
	backoffs := in.Backoffs
	if backoffs == nil {
		backoffs = defaultBackoffs
	}

	for i, delay := range backoffs {
		select {
		case <-ctx.Done():
			return LibraryItem{}, ctx.Err()
		case <-time.After(delay):
		}

		item, ok, err := c.searchOnce(ctx, in.LibraryID, in.Title)
		if err != nil {
			return LibraryItem{}, fmt.Errorf("abs.Discover: poll %d: %w", i+1, err)
		}
		if ok {
			return item, nil
		}
	}
	return LibraryItem{}, ErrNotFound
}

func (c *API) searchOnce(ctx context.Context, libraryID, title string) (LibraryItem, bool, error) {
	// ABS's library search rejects or silently misses queries containing FTS
	// metacharacters like ":". Send a punctuation-stripped query and rely on
	// titleMatches for the comparison instead.
	params := &SearchLibraryParams{Q: sanitiseSearchQuery(title)}
	resp, err := c.SearchLibraryWithResponse(ctx, libraryID, params)
	if err != nil {
		return LibraryItem{}, false, err
	}
	if resp.StatusCode() != 200 {
		return LibraryItem{}, false, fmt.Errorf("HTTP %d", resp.StatusCode())
	}
	if resp.JSON200 == nil || resp.JSON200.Book == nil {
		return LibraryItem{}, false, nil
	}
	for _, hit := range *resp.JSON200.Book {
		if got, ok := itemTitle(hit.LibraryItem); ok && titleMatches(got, title) {
			return hit.LibraryItem, true, nil
		}
	}
	return LibraryItem{}, false, nil
}

// sanitiseSearchQuery strips characters that confuse ABS's search backend
// (notably ":" which behaves as an FTS field operator on some configs) and
// collapses runs of whitespace. Letters, digits, and ASCII space survive.
func sanitiseSearchQuery(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// titleMatches is the tolerant title comparison used in post-upload discovery.
// It accepts:
//   - case-insensitive exact match;
//   - either title being a prefix of the other across a common subtitle
//     separator (" - ", ": ", "; "), which covers ABS pulling "Title - Subtitle"
//     out of an embedded `©nam` tag while we passed just "Title";
//   - punctuation-insensitive equality (covers "Foo: Bar" vs "Foo Bar"
//     after SafeFileName strips the colon).
func titleMatches(got, want string) bool {
	g := strings.ToLower(strings.TrimSpace(got))
	w := strings.ToLower(strings.TrimSpace(want))
	if g == w {
		return true
	}
	for _, sep := range []string{" - ", ": ", "; "} {
		if strings.HasPrefix(g, w+sep) || strings.HasPrefix(w, g+sep) {
			return true
		}
	}
	return sanitiseSearchQuery(g) == sanitiseSearchQuery(w)
}

// itemTitle reads the strongly-typed Title field out of the minified
// libraryItem.media.metadata blob. Returns "", false if Title is unset.
// The generated BookMetadata.Get() only walks AdditionalProperties, so we
// read the strong field directly.
func itemTitle(item LibraryItem) (string, bool) {
	if item.Media.Metadata == nil || item.Media.Metadata.Title == nil {
		return "", false
	}
	return *item.Media.Metadata.Title, true
}
