package format

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	id3 "github.com/bogem/id3v2/v2"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
)

// Tags is the metadata we want stamped onto every MP3 track.
//
// Fields map to ID3v2.4 frames:
//
//	Title    → TIT2 (or chapter title if available)
//	Artist   → TPE1 (author)
//	Album    → TALB (book title)
//	Year     → TYER (publication year)
//	Genre    → TCON (first libro.fm genre, if any)
//	Track    → TRCK ("N/Total")
//	Composer → TCOM (used for narrator — common audiobook convention)
//	Cover    → APIC (cover image bytes)
type Tags struct {
	Title     string
	Artist    string
	Album     string
	Year      string
	Genre     string
	Track     string
	Composer  string
	Comment   string
	Cover     []byte
	CoverMIME string
}

// WriteTags opens path, replaces its ID3v2 tag with the supplied values, and
// saves. Existing audio bytes are preserved.
func WriteTags(path string, t Tags) error {
	if path == "" {
		return errors.New("format.WriteTags: empty path")
	}
	tag, err := id3.Open(path, id3.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer tag.Close()

	tag.SetDefaultEncoding(id3.EncodingUTF8)
	tag.SetVersion(4) // ID3v2.4

	tag.SetTitle(t.Title)
	tag.SetArtist(t.Artist)
	tag.SetAlbum(t.Album)
	tag.SetYear(t.Year)
	tag.SetGenre(t.Genre)

	if t.Track != "" {
		// SetTextFrame writes raw — convention is "track/total".
		tag.AddTextFrame(tag.CommonID("Track number/Position in set"), id3.EncodingUTF8, t.Track)
	}
	if t.Composer != "" {
		tag.AddTextFrame(tag.CommonID("Composer"), id3.EncodingUTF8, t.Composer)
	}
	if t.Comment != "" {
		tag.AddCommentFrame(id3.CommentFrame{
			Encoding:    id3.EncodingUTF8,
			Language:    "eng",
			Description: "",
			Text:        t.Comment,
		})
	}
	if len(t.Cover) > 0 {
		mime := t.CoverMIME
		if mime == "" {
			mime = "image/jpeg"
		}
		tag.AddAttachedPicture(id3.PictureFrame{
			Encoding:    id3.EncodingUTF8,
			MimeType:    mime,
			PictureType: id3.PTFrontCover,
			Description: "Cover",
			Picture:     t.Cover,
		})
	}

	if err := tag.Save(); err != nil {
		return fmt.Errorf("save tag %q: %w", path, err)
	}
	return nil
}

// buildTags constructs a Tags from a libro.fm Book + the track's positional
// info. The chapter title (if present) becomes the per-track Title; otherwise
// we fall back to "<Book Title> - Part <n>".
func buildTags(book librofm.Book, trackNum, totalTracks int, tracks []librofm.Track) Tags {
	title := book.Title
	if trackNum-1 < len(tracks) {
		if ct := strings.TrimSpace(tracks[trackNum-1].ChapterTitle); ct != "" {
			title = ct
		} else {
			title = fmt.Sprintf("%s - Part %d", book.Title, trackNum)
		}
	}

	artist := strings.Join(book.Authors, ", ")
	year := ""
	if book.PublicationDate != nil {
		year = strconv.Itoa(book.PublicationDate.Year())
	}
	genre := ""
	if len(book.Genres) > 0 {
		genre = book.Genres[0].Name
	}
	composer := ""
	if book.AudiobookInfo != nil && len(book.AudiobookInfo.Narrators) > 0 {
		composer = strings.Join(book.AudiobookInfo.Narrators, ", ")
	}

	return Tags{
		Title:    title,
		Artist:   artist,
		Album:    book.Title,
		Year:     year,
		Genre:    genre,
		Track:    fmt.Sprintf("%d/%d", trackNum, totalTracks),
		Composer: composer,
		Comment:  fmt.Sprintf("ISBN %s", book.ISBN),
	}
}
