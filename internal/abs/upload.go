package abs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
)

// UploadFile is one file to send with an UploadItem call.
type UploadFile struct {
	// Name becomes the part filename in the multipart body. Use the basename
	// you want ABS to see; the on-disk path is irrelevant.
	Name string

	// Open returns a fresh reader each time it is called. Callers can either
	// pass a file-backed implementation (see UploadFileFromPath) or wrap an
	// in-memory bytes.Buffer. The caller is responsible for closing the
	// returned reader if needed.
	Open func() (io.ReadCloser, error)
}

// UploadFileFromPath is a convenience constructor that streams a file from
// disk. The file is re-opened on each call to Open, so retries get a fresh
// stream from byte zero.
func UploadFileFromPath(path string) UploadFile {
	return UploadFile{
		Name: filepath.Base(path),
		Open: func() (io.ReadCloser, error) { return os.Open(path) }, // #nosec G304 — caller-controlled
	}
}

// UploadItemInput is the payload for POST /api/upload.
type UploadItemInput struct {
	LibraryID string
	FolderID  string
	Title     string // required; used only for folder naming, not persisted
	Author    string // optional; folder naming only
	Series    string // optional; folder naming only
	Files     []UploadFile
}

// UploadItem POSTs to /api/upload using a streamed multipart body. We hand-write
// this rather than going through the generated client because oapi-codegen's
// multipart codegen is awkward (it materialises the whole body in memory and
// doesn't model `files[]` cleanly).
//
// ABS responds 200 with no body on success. The new library item appears in
// the library only after the scanner picks it up — use DiscoverItem to poll.
func (c *API) UploadItem(ctx context.Context, in UploadItemInput) error {
	if in.LibraryID == "" || in.FolderID == "" || in.Title == "" {
		return errors.New("abs.UploadItem: LibraryID, FolderID, and Title are required")
	}
	if len(in.Files) == 0 {
		return errors.New("abs.UploadItem: at least one file is required")
	}

	// Build the multipart body in a pipe so we can stream large audio files
	// without buffering them in memory. The mw.Close() finalises the
	// trailing boundary; the pipe writer is closed afterwards to signal EOF
	// to the HTTP transport.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		var firstErr error
		set := func(err error) {
			if firstErr == nil && err != nil {
				firstErr = err
			}
		}
		write := func(field, val string) {
			if val == "" {
				return
			}
			set(mw.WriteField(field, val))
		}

		// ABS destructures `library`/`folder` from the body, not `libraryId`/`folderId`
		// (server/controllers/MiscController.js handleUpload).
		write("library", in.LibraryID)
		write("folder", in.FolderID)
		write("title", in.Title)
		write("author", in.Author)
		write("series", in.Series)

		for _, f := range in.Files {
			fw, err := mw.CreatePart(filePartHeader(f.Name))
			if err != nil {
				set(err)
				break
			}
			rc, err := f.Open()
			if err != nil {
				set(err)
				break
			}
			_, copyErr := io.Copy(fw, rc)
			closeErr := rc.Close()
			set(copyErr)
			set(closeErr)
			if firstErr != nil {
				break
			}
		}

		set(mw.Close())
		// CloseWithError(nil) is equivalent to Close(); using CloseWithError
		// here propagates any earlier error to the HTTP transport as a body
		// read error, which surfaces as a clean RoundTrip failure.
		_ = pw.CloseWithError(firstErr)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/upload", pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return fmt.Errorf("abs.UploadItem: build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	// Use the underlying http.Client we already configured (with auth editor
	// and logging transport). We have to dial it through the generated
	// Client's HTTPClient so the editor runs.
	if err := c.applyAuth(ctx, req); err != nil {
		return err
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("abs.UploadItem: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// ABS returns no body on success — drain to allow connection reuse.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	// Pull up to 2 KiB of body for diagnostics. ABS sometimes returns plain
	// text errors (e.g. "Invalid request body"), sometimes nothing.
	bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return &UploadError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(bodySnippet))}
}

// UploadError is returned by UploadItem on a non-2xx response.
type UploadError struct {
	StatusCode int
	Body       string
}

func (e *UploadError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("abs.UploadItem: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("abs.UploadItem: HTTP %d: %s", e.StatusCode, e.Body)
}

func filePartHeader(filename string) textproto.MIMEHeader {
	h := make(textproto.MIMEHeader)
	// ABS expects `files` (the field repeats for each file).
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename=%q`, filename))
	h.Set("Content-Type", "application/octet-stream")
	return h
}

// applyAuth runs the same logical RequestEditorFn the generated client uses,
// so the multipart request gets the same Bearer header as everything else.
// The generated client's editor slice isn't reachable from outside the package,
// so we replicate the single editor we register in NewClient.
func (c *API) applyAuth(_ context.Context, req *http.Request) error {
	if c.token == "" {
		return errors.New("abs: no token configured")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return nil
}

func (c *API) httpClient() *http.Client {
	if c.http == nil {
		return http.DefaultClient
	}
	return c.http
}
