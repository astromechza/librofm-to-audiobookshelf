package format_test

import (
	"os"
	"path/filepath"
	"testing"

	id3 "github.com/bogem/id3v2/v2"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/format"
)

func TestWriteTags_RoundTrip(t *testing.T) {
	t.Parallel()

	// id3v2 is happy to tag any file — the "audio bytes" can be empty, the
	// library just appends/replaces the tag at the top of the file.
	path := filepath.Join(t.TempDir(), "track.mp3")
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	tags := format.Tags{
		Title:     "Chapter Three",
		Artist:    "Jane Doe",
		Album:     "The Book",
		Year:      "2024",
		Genre:     "Fiction",
		Track:     "3/12",
		Composer:  "Narrator Ned",
		Comment:   "ISBN 9781234567890",
		Cover:     []byte{0xff, 0xd8, 0xff, 0xe0}, // minimal JPEG SOI marker
		CoverMIME: "image/jpeg",
	}
	if err := format.WriteTags(path, tags); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := id3.Open(path, id3.Options{Parse: true})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer got.Close()

	if got.Title() != "Chapter Three" {
		t.Errorf("Title = %q, want Chapter Three", got.Title())
	}
	if got.Artist() != "Jane Doe" {
		t.Errorf("Artist = %q, want Jane Doe", got.Artist())
	}
	if got.Album() != "The Book" {
		t.Errorf("Album = %q, want The Book", got.Album())
	}
	if got.Year() != "2024" {
		t.Errorf("Year = %q, want 2024", got.Year())
	}
	if got.Genre() != "Fiction" {
		t.Errorf("Genre = %q, want Fiction", got.Genre())
	}
	if pics := got.GetFrames(got.CommonID("Attached picture")); len(pics) != 1 {
		t.Errorf("expected 1 APIC frame, got %d", len(pics))
	}
	// Track + Composer are stored as text frames.
	if frames := got.GetTextFrame(got.CommonID("Track number/Position in set")); frames.Text != "3/12" {
		t.Errorf("Track = %q, want 3/12", frames.Text)
	}
	if frames := got.GetTextFrame(got.CommonID("Composer")); frames.Text != "Narrator Ned" {
		t.Errorf("Composer = %q, want Narrator Ned", frames.Text)
	}
}
