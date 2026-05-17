package reconcile_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/abs"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/format"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/reconcile"
)

// callCounters tracks which mutating endpoints fired during a run.
type callCounters struct {
	uploads atomic.Int32
	patches atomic.Int32
	covers  atomic.Int32
}

// fixtures sets up a libro.fm client + an ABS API + counters, all backed by
// a single httptest server. The libroBooks and absLibraryItems args
// parameterise what the two libraries contain. Returns wired-up
// reconcile.Options with stub downloaders that just write a placeholder file.
func fixtures(t *testing.T, libroBooks []librofm.Book, absLibraryItems []map[string]any, libraryName string) (
	*librofm.Client, *abs.API, *callCounters, reconcile.Options,
) {
	t.Helper()
	counters := &callCounters{}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"t"}`))
	})
	mux.HandleFunc("/api/v10/library", func(w http.ResponseWriter, _ *http.Request) {
		payload := map[string]any{"page": 1, "total_pages": 1, "audiobooks": libroBooks}
		b, _ := json.Marshal(payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/api/libraries", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"libraries":[
			{"id":"lib-1","name":"` + libraryName + `","mediaType":"book","folders":[{"id":"f-1","fullPath":"/books"}]}
		]}`))
	})
	mux.HandleFunc("/api/libraries/lib-1/items", func(w http.ResponseWriter, _ *http.Request) {
		out := map[string]any{
			"results": absLibraryItems,
			"total":   len(absLibraryItems),
			"limit":   200,
			"page":    0,
		}
		b, _ := json.Marshal(out)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/api/me", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"username":"alice"}`))
	})
	mux.HandleFunc("/api/upload", func(w http.ResponseWriter, _ *http.Request) {
		counters.uploads.Add(1)
		w.WriteHeader(200)
	})
	mux.HandleFunc("/api/libraries/lib-1/search", func(w http.ResponseWriter, r *http.Request) {
		// Echo the queried title back as a freshly-created LibraryItem.
		title := r.URL.Query().Get("q")
		payload := map[string]any{
			"book": []map[string]any{
				{
					"libraryItem": map[string]any{
						"id":        "itm-new",
						"mediaType": "book",
						"media": map[string]any{
							"metadata": map[string]any{"title": title},
						},
					},
				},
			},
		}
		b, _ := json.Marshal(payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/api/items/", func(w http.ResponseWriter, r *http.Request) {
		// Routes: PATCH /api/items/:id/media, POST /api/items/:id/cover.
		switch {
		case strings.HasSuffix(r.URL.Path, "/media") && r.Method == http.MethodPatch:
			counters.patches.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"updated":true}`))
		case strings.HasSuffix(r.URL.Path, "/cover") && r.Method == http.MethodPost:
			counters.covers.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	lf := librofm.NewClient(librofm.Options{
		BaseURL:    srv.URL,
		TokenCache: &librofm.TokenCache{Path: filepath.Join(t.TempDir(), "tok")},
	})
	if err := lf.Login(context.Background(), "u", "p", false); err != nil {
		t.Fatalf("login: %v", err)
	}

	api, err := abs.NewAPI(abs.Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}

	stub := func(_ context.Context, _ *librofm.Client, b librofm.Book, workDir string) (format.Result, error) {
		// Write a placeholder file so abs.UploadItem has something to stream.
		// Path stays inside workDir → reconcile's defer-RemoveAll cleans it up.
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return format.Result{}, err
		}
		path := filepath.Join(workDir, b.ISBN+".bin")
		if err := os.WriteFile(path, []byte("stub"), 0o600); err != nil {
			return format.Result{}, err
		}
		return format.Result{
			Files:   []abs.UploadFile{abs.UploadFileFromPath(path)},
			Format:  "m4b",
			WorkDir: workDir,
		}, nil
	}

	opts := reconcile.Options{
		Library:     libraryName,
		WorkDir:     t.TempDir(),
		DownloadM4B: stub,
		DownloadMP3: stub,
	}
	return lf, api, counters, opts
}

func TestRun_NotPresentTriggersFullSync(t *testing.T) {
	t.Parallel()
	books := []librofm.Book{{ISBN: "111", Title: "Alpha", Authors: []string{"Author A"}}}
	lf, api, counters, opts := fixtures(t, books, []map[string]any{}, "Audiobooks")

	summary, err := reconcile.Run(context.Background(), lf, api, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Synced != 1 || summary.Failed != 0 || summary.AlreadyPresent != 0 {
		t.Errorf("summary = %+v", summary)
	}
	if counters.uploads.Load() != 1 || counters.patches.Load() != 1 {
		t.Errorf("calls: uploads=%d patches=%d covers=%d (want 1/1/?)",
			counters.uploads.Load(), counters.patches.Load(), counters.covers.Load())
	}
}

func TestRun_PresentSyncedSkips(t *testing.T) {
	t.Parallel()
	books := []librofm.Book{{ISBN: "222", Title: "Beta", Authors: []string{"Author B"}}}
	absItems := []map[string]any{{
		"id":        "itm-beta",
		"mediaType": "book",
		"media": map[string]any{
			"metadata": map[string]any{"title": "Beta", "isbn": "222", "authorName": "Author B"},
		},
	}}
	lf, api, counters, opts := fixtures(t, books, absItems, "Audiobooks")

	summary, err := reconcile.Run(context.Background(), lf, api, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.AlreadyPresent != 1 || summary.Synced != 0 || summary.Repaired != 0 {
		t.Errorf("summary = %+v", summary)
	}
	if counters.uploads.Load() != 0 || counters.patches.Load() != 0 {
		t.Errorf("expected no mutating calls, got uploads=%d patches=%d",
			counters.uploads.Load(), counters.patches.Load())
	}
}

func TestRun_PresentNoMetadataTriggersRepair(t *testing.T) {
	t.Parallel()
	books := []librofm.Book{{ISBN: "333", Title: "Gamma", Authors: []string{"Author C"}}}
	// User added this manually: title matches but no ISBN populated yet.
	absItems := []map[string]any{{
		"id":        "itm-gamma",
		"mediaType": "book",
		"media": map[string]any{
			"metadata": map[string]any{"title": "Gamma", "authorName": "Author C"},
		},
	}}
	lf, api, counters, opts := fixtures(t, books, absItems, "Audiobooks")

	summary, err := reconcile.Run(context.Background(), lf, api, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Repaired != 1 || summary.Synced != 0 || summary.AlreadyPresent != 0 {
		t.Errorf("summary = %+v", summary)
	}
	// Repair path does NOT upload — only PATCHes metadata + cover.
	if counters.uploads.Load() != 0 {
		t.Errorf("repair must not upload, got uploads=%d", counters.uploads.Load())
	}
	if counters.patches.Load() != 1 {
		t.Errorf("expected 1 PATCH, got %d", counters.patches.Load())
	}
}

// Regression: a partial upload where ABS's scanner pulled a subtitle suffix
// out of the audio file's ©nam tag should still be matched on a re-run, so we
// PATCH metadata onto it rather than duplicate-uploading.
func TestRun_FuzzMatchesSubtitleSuffix(t *testing.T) {
	t.Parallel()
	books := []librofm.Book{{ISBN: "444", Title: "Amberlough", Authors: []string{"Lara Elena Donnelly"}}}
	absItems := []map[string]any{{
		"id":        "itm-amber",
		"mediaType": "book",
		"media": map[string]any{
			"metadata": map[string]any{
				"title":      "Amberlough - A Novel",
				"authorName": "Lara Elena Donnelly",
			},
		},
	}}
	lf, api, counters, opts := fixtures(t, books, absItems, "Audiobooks")

	summary, err := reconcile.Run(context.Background(), lf, api, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Repaired != 1 || summary.Synced != 0 {
		t.Errorf("summary = %+v, want Repaired=1 (matched the partial upload)", summary)
	}
	if counters.uploads.Load() != 0 {
		t.Errorf("must not duplicate-upload, got uploads=%d", counters.uploads.Load())
	}
}

func TestRun_DryRunMakesNoMutations(t *testing.T) {
	t.Parallel()
	books := []librofm.Book{
		{ISBN: "111", Title: "Alpha"},
		{ISBN: "222", Title: "Beta", Authors: []string{"Author B"}},
	}
	absItems := []map[string]any{{
		"id":        "itm-beta",
		"mediaType": "book",
		"media": map[string]any{
			"metadata": map[string]any{"title": "Beta", "authorName": "Author B"}, // no ISBN → repair candidate
		},
	}}
	lf, api, counters, opts := fixtures(t, books, absItems, "Audiobooks")
	opts.DryRun = true

	summary, err := reconcile.Run(context.Background(), lf, api, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Synced != 1 || summary.Repaired != 1 || summary.Failed != 0 {
		t.Errorf("summary = %+v", summary)
	}
	if counters.uploads.Load() != 0 || counters.patches.Load() != 0 || counters.covers.Load() != 0 {
		t.Errorf("DryRun must skip all mutations; got uploads=%d patches=%d covers=%d",
			counters.uploads.Load(), counters.patches.Load(), counters.covers.Load())
	}
}

func TestRun_LimitTruncatesConsideration(t *testing.T) {
	t.Parallel()
	books := []librofm.Book{
		{ISBN: "111", Title: "Alpha"},
		{ISBN: "222", Title: "Beta"},
		{ISBN: "333", Title: "Gamma"},
	}
	lf, api, counters, opts := fixtures(t, books, []map[string]any{}, "Audiobooks")
	opts.Limit = 2

	summary, err := reconcile.Run(context.Background(), lf, api, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.LibroFmTotal != 3 || summary.Considered != 2 || summary.Synced != 2 {
		t.Errorf("summary = %+v", summary)
	}
	if counters.uploads.Load() != 2 {
		t.Errorf("uploads = %d, want 2", counters.uploads.Load())
	}
}

func TestRun_LibraryNotFound(t *testing.T) {
	t.Parallel()
	lf, api, _, opts := fixtures(t, nil, nil, "Audiobooks")
	opts.Library = "Nope"
	_, err := reconcile.Run(context.Background(), lf, api, opts)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want 'not found'", err)
	}
}
