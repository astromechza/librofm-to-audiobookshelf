// Package format downloads audiobook files from libro.fm in the format the
// reconciler should hand to audiobookshelf.
//
// Two paths:
//
//   - DownloadM4B: tries the packaged-M4B endpoint. Returns one file ready to
//     upload (M4B already has chapters + cover + minimal tags embedded by
//     libro.fm). Returns librofm.ErrNoM4B if no M4B exists for the ISBN.
//
//   - DownloadMP3: fetches the multi-part ZIP manifest, downloads each ZIP,
//     extracts the MP3 tracks, ID3-tags them with the libro.fm metadata
//     (including the cover image as APIC), returns the ordered MP3 list ready
//     to upload.
//
// Typical caller (the reconciler):
//
//	res, err := format.DownloadM4B(ctx, lf, book, workDir)
//	if errors.Is(err, librofm.ErrNoM4B) {
//	    res, err = format.DownloadMP3(ctx, lf, book, workDir)
//	}
//
// Neither function uploads anything itself; that's the caller's job via
// internal/abs.UploadItem.
package format

import (
	"regexp"
	"strings"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/abs"
)

// Result is what a downloader returns to the reconciler.
type Result struct {
	// Files are the staged outputs in the order they should be uploaded.
	// The reconciler hands these to abs.UploadItem as-is.
	Files []abs.UploadFile

	// Format is "m4b" or "mp3"; useful for logging and for the reconciler
	// to decide if it needs a fallback.
	Format string

	// WorkDir is where the files were staged. Caller is responsible for
	// removing it after the upload completes (or on error).
	WorkDir string
}

// illegalNameChars matches characters that are unsafe across Windows / macOS
// / Linux filesystems. We strip them rather than escape so the output stays
// readable. Trailing dots/spaces are also stripped (Windows quirk).
var illegalNameChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// SafeFileName sanitises a string for use as a single path component.
// Returns "untitled" if the result would be empty.
func SafeFileName(s string) string {
	s = illegalNameChars.ReplaceAllString(s, "")
	s = strings.TrimRight(s, ". ")
	s = strings.TrimSpace(s)
	if s == "" {
		return "untitled"
	}
	// Cap at a generous bound — long enough for any real title, short enough
	// to keep the full path well under PATH_MAX on all platforms.
	const maxLen = 180
	if len(s) > maxLen {
		s = strings.TrimSpace(s[:maxLen])
	}
	return s
}
