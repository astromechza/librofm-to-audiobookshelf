package format_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	id3 "github.com/bogem/id3v2/v2"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/format"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
)

// newLibroClientAndURL stands up an httptest server with the supplied mux,
// registers a stub /oauth/token handler, and returns a logged-in libro.fm
// client + the server's base URL (for handlers to self-reference).
func newLibroClientAndURL(t *testing.T, baseMux *http.ServeMux) (*librofm.Client, string) {
	t.Helper()
	srv := httptest.NewServer(baseMux)
	t.Cleanup(srv.Close)
	baseMux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"t"}`))
	})
	c := librofm.NewClient(librofm.Options{
		BaseURL:    srv.URL,
		TokenCache: &librofm.TokenCache{Path: filepath.Join(t.TempDir(), "tok")},
	})
	if err := c.Login(context.Background(), "u", "p", false); err != nil {
		t.Fatalf("login: %v", err)
	}
	return c, srv.URL
}

func TestDownloadM4B_Happy(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	body := []byte("fake m4b bytes \x00\x01\x02")
	mux.HandleFunc("/api/v10/audiobooks/9781/packaged_m4b", func(w http.ResponseWriter, r *http.Request) {
		host := "http://" + r.Host
		_, _ = w.Write([]byte(`{"m4b_url":"` + host + `/dl?response-content-disposition=attachment%3Bfilename%3D%22The+Wanted+Title.m4b%22"}`))
	})
	mux.HandleFunc("/dl", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
	c, _ := newLibroClientAndURL(t, mux)

	res, err := format.DownloadM4B(context.Background(), c, librofm.Book{ISBN: "9781", Title: "fallback"}, t.TempDir())
	if err != nil {
		t.Fatalf("DownloadM4B: %v", err)
	}
	if res.Format != "m4b" {
		t.Errorf("Format = %q, want m4b", res.Format)
	}
	if len(res.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(res.Files))
	}
	if !strings.HasSuffix(res.Files[0].Name, ".m4b") {
		t.Errorf("file name = %q, want .m4b extension", res.Files[0].Name)
	}
	if !strings.Contains(res.Files[0].Name, "The Wanted Title") {
		t.Errorf("file name = %q, want title from URL", res.Files[0].Name)
	}
	rc, err := res.Files[0].Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), body) {
		t.Errorf("body mismatch")
	}
}

func TestDownloadM4B_404FallsThrough(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v10/audiobooks/missing/packaged_m4b", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	c, _ := newLibroClientAndURL(t, mux)

	_, err := format.DownloadM4B(context.Background(), c, librofm.Book{ISBN: "missing", Title: "x"}, t.TempDir())
	if !errors.Is(err, librofm.ErrNoM4B) {
		t.Fatalf("err = %v, want ErrNoM4B", err)
	}
}

func TestDownloadMP3_HappyWithCoverAndChapters(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()

	// Two-part download: part 1 → 01.mp3, 02.mp3; part 2 → 03.mp3.
	zip1 := buildZip(t, map[string][]byte{"01.mp3": {}, "02.mp3": {}})
	zip2 := buildZip(t, map[string][]byte{"03.mp3": {}})

	mux.HandleFunc("/api/v10/download-manifest", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("isbn"); got != "9781" {
			t.Errorf("isbn = %q, want 9781", got)
		}
		host := "http://" + r.Host
		out := map[string]any{
			"isbn": "9781",
			"parts": []map[string]any{
				{"url": host + "/part1.zip", "size_bytes": len(zip1)},
				{"url": host + "/part2.zip", "size_bytes": len(zip2)},
			},
			"tracks": []map[string]any{
				{"number": 1, "chapter_title": "One"},
				{"number": 2, "chapter_title": "Two"},
				{"number": 3, "chapter_title": ""}, // empty chapter title is OK
			},
		}
		b, _ := json.Marshal(out)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/part1.zip", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(zip1) })
	mux.HandleFunc("/part2.zip", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(zip2) })
	mux.HandleFunc("/covers/9781.jpg", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10}) // minimal JPEG
	})

	c, baseURL := newLibroClientAndURL(t, mux)
	book := librofm.Book{
		ISBN:    "9781",
		Title:   "The Book",
		Authors: []string{"Jane Doe"},
		// CoverURL is intentionally on a non-libro.fm host so the librofm
		// Download path won't attach the bearer (mirrors production).
		// But here we point it at our test server so we can serve bytes.
		CoverURL: baseURL + "/covers/9781.jpg",
	}

	res, err := format.DownloadMP3(context.Background(), c, book, t.TempDir())
	if err != nil {
		t.Fatalf("DownloadMP3: %v", err)
	}
	if res.Format != "mp3" {
		t.Errorf("Format = %q, want mp3", res.Format)
	}
	if len(res.Files) != 3 {
		t.Fatalf("expected 3 files, got %d: %+v", len(res.Files), res.Files)
	}

	// File order should match track order — 01, 02, 03.
	wantNames := []string{"01.mp3", "02.mp3", "03.mp3"}
	for i, want := range wantNames {
		if res.Files[i].Name != want {
			t.Errorf("Files[%d].Name = %q, want %q", i, res.Files[i].Name, want)
		}
	}

	// Re-open the first file and confirm it was tagged with chapter title +
	// album + cover.
	rc, err := res.Files[0].Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	tmp := filepath.Join(t.TempDir(), "first.mp3")
	if err := writeFromReader(tmp, rc); err != nil {
		t.Fatalf("stage: %v", err)
	}
	tag, err := id3.Open(tmp, id3.Options{Parse: true})
	if err != nil {
		t.Fatalf("re-open id3: %v", err)
	}
	defer tag.Close()
	if tag.Title() != "One" {
		t.Errorf("Title = %q, want One", tag.Title())
	}
	if tag.Album() != "The Book" {
		t.Errorf("Album = %q, want The Book", tag.Album())
	}
	if tag.Artist() != "Jane Doe" {
		t.Errorf("Artist = %q, want Jane Doe", tag.Artist())
	}
	if pics := tag.GetFrames(tag.CommonID("Attached picture")); len(pics) != 1 {
		t.Errorf("APIC frame count = %d, want 1", len(pics))
	}
}

func TestDownloadMP3_NoManifestParts(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v10/download-manifest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"isbn":"x","parts":[]}`))
	})
	c, _ := newLibroClientAndURL(t, mux)

	_, err := format.DownloadMP3(context.Background(), c, librofm.Book{ISBN: "x"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no parts") {
		t.Fatalf("err = %v, want 'no parts'", err)
	}
}

// Helpers ----------------------------------------------------------

// buildZip constructs an in-memory zip with the given (name → content) entries.
func buildZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatalf("zip create %q: %v", n, err)
		}
		if _, err := w.Write(entries[n]); err != nil {
			t.Fatalf("zip write %q: %v", n, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// writeFromReader copies r into a fresh file at path (mode 0600).
func writeFromReader(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}
