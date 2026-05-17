package reconcile

import (
	"strconv"
	"strings"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/abs"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
)

// buildMetadataPayload turns a libro.fm Book into the JSON body we send to
// PATCH /api/items/:id/media. Array fields are always full sets, not partial
// updates — ABS replaces them outright (per docs/02-audiobookshelf-api.md).
func buildMetadataPayload(book librofm.Book) abs.UpdateMediaRequest {
	m := abs.UpdateMediaMetadata{
		Title:    ptr(book.Title),
		Subtitle: ptr(book.Subtitle),
		Isbn:     ptr(book.ISBN),
	}

	if len(book.Authors) > 0 {
		authors := make([]abs.AuthorRef, 0, len(book.Authors))
		for _, name := range book.Authors {
			n := strings.TrimSpace(name)
			if n != "" {
				authors = append(authors, abs.AuthorRef{Name: n})
			}
		}
		m.Authors = &authors
	}

	if book.AudiobookInfo != nil && len(book.AudiobookInfo.Narrators) > 0 {
		narrators := make([]string, 0, len(book.AudiobookInfo.Narrators))
		for _, n := range book.AudiobookInfo.Narrators {
			n = strings.TrimSpace(n)
			if n != "" {
				narrators = append(narrators, n)
			}
		}
		m.Narrators = &narrators
	}

	if book.AudiobookInfo != nil && book.AudiobookInfo.AudioLanguage != "" {
		m.Language = ptr(book.AudiobookInfo.AudioLanguage)
	}

	if book.PublicationDate != nil {
		m.PublishedYear = ptr(strconv.Itoa(book.PublicationDate.Year()))
	}
	if book.Publisher != "" {
		m.Publisher = ptr(book.Publisher)
	}
	if book.Description != "" {
		m.Description = ptr(book.Description)
	}
	if len(book.Genres) > 0 {
		genres := make([]string, 0, len(book.Genres))
		for _, g := range book.Genres {
			n := strings.TrimSpace(g.Name)
			if n != "" {
				genres = append(genres, n)
			}
		}
		m.Genres = &genres
	}
	if book.Series != "" {
		seq := ""
		if book.SeriesNum != nil {
			// ABS stores sequence as a string so "1", "1.5", "0.5" are all
			// representable.
			seq = strconv.FormatFloat(*book.SeriesNum, 'f', -1, 64)
		}
		series := []abs.SeriesRef{{Name: book.Series, Sequence: ptr(seq)}}
		m.Series = &series
	}

	return abs.UpdateMediaRequest{Metadata: &m}
}

func ptr[T any](v T) *T { return &v }
