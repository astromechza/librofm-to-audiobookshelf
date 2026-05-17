package format

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/abs"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
)

// DownloadM4B downloads the packaged M4B for book.ISBN into a fresh subdir of
// workDir. Returns librofm.ErrNoM4B (wrapped) if no M4B exists for this ISBN —
// the caller should fall back to DownloadMP3.
//
// The returned Result has exactly one file, ready to hand to abs.UploadItem.
// The file is named from the URL's Content-Disposition (the title libro.fm
// embeds in the presigned URL) if extractable; otherwise from the book title.
func DownloadM4B(ctx context.Context, lf *librofm.Client, book librofm.Book, workDir string) (Result, error) {
	if book.ISBN == "" {
		return Result{}, errors.New("format.DownloadM4B: empty ISBN")
	}

	m4bURL, err := lf.M4BURL(ctx, book.ISBN)
	if err != nil {
		return Result{}, err // already wraps ErrNoM4B on 404
	}

	dir := filepath.Join(workDir, book.ISBN+"-m4b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("format.DownloadM4B: mkdir: %w", err)
	}

	fileName := m4bFileName(m4bURL, book.Title) + ".m4b"
	destPath := filepath.Join(dir, fileName)

	body, _, err := lf.Download(ctx, m4bURL)
	if err != nil {
		return Result{}, fmt.Errorf("format.DownloadM4B: fetch: %w", err)
	}
	defer body.Close()

	dest, err := os.Create(destPath) // #nosec G304 — destPath is fully derived from workDir + sanitised name
	if err != nil {
		return Result{}, fmt.Errorf("format.DownloadM4B: create %q: %w", destPath, err)
	}
	if _, err := io.Copy(dest, body); err != nil {
		_ = dest.Close()
		return Result{}, fmt.Errorf("format.DownloadM4B: write %q: %w", destPath, err)
	}
	if err := dest.Close(); err != nil {
		return Result{}, fmt.Errorf("format.DownloadM4B: close %q: %w", destPath, err)
	}

	return Result{
		Files:   []abs.UploadFile{abs.UploadFileFromPath(destPath)},
		Format:  "m4b",
		WorkDir: dir,
	}, nil
}

// m4bFileName pulls a human-friendly name from the presigned S3 URL's
// `response-content-disposition` query parameter (libro.fm puts the title
// there as `filename="Title - Author.m4b"`). Falls back to the book title.
func m4bFileName(presignedURL, bookTitle string) string {
	u, err := url.Parse(presignedURL)
	if err == nil {
		if cd := u.Query().Get("response-content-disposition"); cd != "" {
			if name := filenameFromContentDisposition(cd); name != "" {
				name = strings.TrimSuffix(name, ".m4b")
				name = strings.TrimSuffix(name, ".M4B")
				return SafeFileName(name)
			}
		}
	}
	return SafeFileName(bookTitle)
}

// filenameFromContentDisposition extracts the filename= parameter from a
// Content-Disposition string. Handles `filename="..."` and `filename=...`.
// Returns "" if absent. Decodes `+` to space.
func filenameFromContentDisposition(cd string) string {
	for _, part := range strings.Split(cd, ";") {
		part = strings.TrimSpace(part)
		const prefix = "filename="
		if !strings.HasPrefix(part, prefix) {
			continue
		}
		v := strings.TrimPrefix(part, prefix)
		v = strings.Trim(v, `"`)
		v = strings.ReplaceAll(v, "+", " ")
		// libro.fm percent-encodes the value too; best-effort decode.
		if decoded, err := url.QueryUnescape(v); err == nil {
			v = decoded
		}
		return v
	}
	return ""
}
