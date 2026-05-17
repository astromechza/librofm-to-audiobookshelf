package format

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/abs"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
)

// DownloadMP3 fetches the multi-part MP3 manifest, downloads each part ZIP,
// extracts the MP3 tracks into workDir/<isbn>-mp3/, ID3-tags each track from
// the libro.fm Book + manifest tracks (including cover image as APIC), and
// returns the ordered list ready to upload.
//
// Track ordering: extracted MP3 filenames are sorted lexicographically (the
// libro.fm zip contents are pre-numbered like 001_Foo.mp3) — this matches the
// manifest's `tracks[].number` order for chapter titles.
func DownloadMP3(ctx context.Context, lf *librofm.Client, book librofm.Book, workDir string) (Result, error) {
	if book.ISBN == "" {
		return Result{}, errors.New("format.DownloadMP3: empty ISBN")
	}

	manifest, err := lf.MP3Manifest(ctx, book.ISBN)
	if err != nil {
		return Result{}, fmt.Errorf("format.DownloadMP3: manifest: %w", err)
	}
	if len(manifest.Parts) == 0 {
		return Result{}, errors.New("format.DownloadMP3: manifest has no parts")
	}

	dir := filepath.Join(workDir, book.ISBN+"-mp3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("format.DownloadMP3: mkdir: %w", err)
	}

	// Download cover once (used as APIC for every track).
	cover, coverMIME, err := fetchCover(ctx, lf, book.CoverURL)
	if err != nil {
		// Cover failure isn't fatal — the PATCH /api/items/:id/cover step
		// will set it from URL anyway. Log via the returned error chain on
		// non-cover errors; for cover specifically we proceed without.
		cover = nil
	}

	// Download + unzip each part. Parts are downloaded sequentially; libro.fm
	// hasn't been observed to rate-limit but we stay conservative.
	for i, part := range manifest.Parts {
		if err := downloadAndExtractZipPart(ctx, lf, part.URL, dir); err != nil {
			return Result{}, fmt.Errorf("format.DownloadMP3: part %d: %w", i+1, err)
		}
	}

	mp3s, err := listMP3s(dir)
	if err != nil {
		return Result{}, err
	}
	if len(mp3s) == 0 {
		return Result{}, fmt.Errorf("format.DownloadMP3: no MP3 files extracted to %q", dir)
	}

	// Tag each track. The track index maps positionally to manifest.Tracks
	// (sorted by .Number); chapter titles may be empty for some books.
	tracksByNumber := sortedTracks(manifest.Tracks)
	totalTracks := len(mp3s)
	uploads := make([]abs.UploadFile, 0, len(mp3s))
	for i, path := range mp3s {
		tags := buildTags(book, i+1, totalTracks, tracksByNumber)
		if cover != nil {
			tags.Cover = cover
			tags.CoverMIME = coverMIME
		}
		if err := WriteTags(path, tags); err != nil {
			return Result{}, fmt.Errorf("format.DownloadMP3: tag %q: %w", path, err)
		}
		uploads = append(uploads, abs.UploadFileFromPath(path))
	}

	return Result{Files: uploads, Format: "mp3", WorkDir: dir}, nil
}

// downloadAndExtractZipPart streams one zip from a presigned URL, writes it
// to a temp file, then extracts MP3 entries into destDir. The zip file is
// removed after successful extraction.
func downloadAndExtractZipPart(ctx context.Context, lf *librofm.Client, presignedURL, destDir string) error {
	tmpZip, err := os.CreateTemp(destDir, "part-*.zip")
	if err != nil {
		return fmt.Errorf("temp zip: %w", err)
	}
	tmpName := tmpZip.Name()
	defer func() { _ = os.Remove(tmpName) }()

	body, _, err := lf.Download(ctx, presignedURL)
	if err != nil {
		_ = tmpZip.Close()
		return fmt.Errorf("download: %w", err)
	}
	_, copyErr := io.Copy(tmpZip, body)
	closeBodyErr := body.Close()
	closeTmpErr := tmpZip.Close()
	if copyErr != nil {
		return fmt.Errorf("write zip: %w", copyErr)
	}
	if closeBodyErr != nil {
		return fmt.Errorf("close response: %w", closeBodyErr)
	}
	if closeTmpErr != nil {
		return fmt.Errorf("close temp zip: %w", closeTmpErr)
	}

	zr, err := zip.OpenReader(tmpName)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if !strings.EqualFold(filepath.Ext(entry.Name), ".mp3") {
			continue
		}
		if err := extractZipEntry(entry, destDir); err != nil {
			return err
		}
	}
	return nil
}

// extractZipEntry safely extracts one zip entry into destDir. Defends against
// path-traversal (zip slip): the destination must remain under destDir.
func extractZipEntry(entry *zip.File, destDir string) error {
	cleanedName := filepath.Base(entry.Name) // libro.fm zip entries are flat
	target := filepath.Join(destDir, cleanedName)

	// Defence in depth: ensure target stays inside destDir even if Base()
	// is fooled by an exotic name.
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("abs(destDir): %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("abs(target): %w", err)
	}
	if !strings.HasPrefix(absTarget, absDest+string(os.PathSeparator)) && absTarget != absDest {
		return fmt.Errorf("zip slip: %q escapes %q", entry.Name, destDir)
	}

	src, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open entry %q: %w", entry.Name, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644) // #nosec G304 — target is validated under destDir
	if err != nil {
		return fmt.Errorf("create %q: %w", target, err)
	}
	if _, err := io.Copy(dst, src); err != nil { // #nosec G110 — zip is from a trusted libro.fm presigned URL, but we still defend against zip slip above
		_ = dst.Close()
		return fmt.Errorf("write %q: %w", target, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close %q: %w", target, err)
	}
	return nil
}

// listMP3s returns the sorted absolute paths of every MP3 file in dir.
func listMP3s(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %q: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".mp3") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// sortedTracks copies manifest.Tracks into ascending order by Number.
func sortedTracks(in []librofm.Track) []librofm.Track {
	out := make([]librofm.Track, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// fetchCover GETs the cover URL into memory and returns the bytes + MIME
// hint. Returns (nil, "", err) on any failure; caller decides whether to
// proceed without a cover.
func fetchCover(ctx context.Context, lf *librofm.Client, coverURL string) ([]byte, string, error) {
	if coverURL == "" {
		return nil, "", errors.New("no cover URL")
	}
	body, _, err := lf.Download(ctx, coverURL)
	if err != nil {
		return nil, "", err
	}
	defer body.Close()
	const maxCover = 8 * 1024 * 1024 // 8 MiB hard cap
	b, err := io.ReadAll(io.LimitReader(body, maxCover))
	if err != nil {
		return nil, "", err
	}
	mime := "image/jpeg"
	if strings.HasSuffix(strings.ToLower(coverURL), ".png") {
		mime = "image/png"
	}
	return b, mime, nil
}
