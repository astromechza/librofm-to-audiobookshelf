package abs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/abs"
)

const testToken = "secret-very-long-bearer-token-XXXXXXXX"

func newTestAPI(t *testing.T, mux *http.ServeMux) *abs.API {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	api, err := abs.NewAPI(abs.Options{BaseURL: srv.URL, Token: testToken})
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	return api
}

func assertAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("Authorization header = %q, want Bearer + token", got)
	}
}

// openString returns an UploadFile.Open that yields s as a fresh ReadCloser.
func openString(s string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(s)), nil }
}

func TestNewAPI_Validation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opts abs.Options
	}{
		{"missing url", abs.Options{Token: "x"}},
		{"missing token", abs.Options{BaseURL: "http://x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := abs.NewAPI(c.opts); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestGetMe(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/me", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"u1","username":"alice","isAdmin":true,"type":"admin"}`))
	})
	api := newTestAPI(t, mux)

	resp, err := api.GetMeWithResponse(context.Background())
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if resp.StatusCode() != 200 || resp.JSON200 == nil {
		t.Fatalf("bad response: %d", resp.StatusCode())
	}
	if got := *resp.JSON200.Username; got != "alice" {
		t.Errorf("username = %q, want alice", got)
	}
}

func TestListLibraries(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/libraries", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"libraries":[
			{"id":"lib1","name":"Books","mediaType":"book","folders":[{"id":"f1","fullPath":"/books"}]},
			{"id":"lib2","name":"Podcasts","mediaType":"podcast","folders":[{"id":"f2","fullPath":"/pod"}]}
		]}`))
	})
	api := newTestAPI(t, mux)

	resp, err := api.ListLibrariesWithResponse(context.Background())
	if err != nil {
		t.Fatalf("ListLibraries: %v", err)
	}
	if resp.JSON200 == nil || len(resp.JSON200.Libraries) != 2 {
		t.Fatalf("bad libraries response")
	}
	if resp.JSON200.Libraries[0].Id != "lib1" {
		t.Errorf("libraries[0].id = %q, want lib1", resp.JSON200.Libraries[0].Id)
	}
}

func TestUploadItem_HappyPath(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	var seenFields map[string]string
	var seenFileNames []string
	mux.HandleFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("wrong Content-Type: %q", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		seenFields = map[string]string{
			"library": r.FormValue("library"),
			"folder":  r.FormValue("folder"),
			"title":   r.FormValue("title"),
			"author":  r.FormValue("author"),
			"series":  r.FormValue("series"),
		}
		if r.MultipartForm != nil {
			for _, fhs := range r.MultipartForm.File {
				for _, fh := range fhs {
					seenFileNames = append(seenFileNames, fh.Filename)
				}
			}
		}
		w.WriteHeader(200)
	})
	api := newTestAPI(t, mux)

	err := api.UploadItem(context.Background(), abs.UploadItemInput{
		LibraryID: "lib1",
		FolderID:  "f1",
		Title:     "My Book",
		Author:    "Some Author",
		Series:    "", // empty fields must be skipped, not sent as blanks
		Files: []abs.UploadFile{
			{Name: "01.mp3", Open: openString("audio-bytes")},
		},
	})
	if err != nil {
		t.Fatalf("UploadItem: %v", err)
	}
	if seenFields["title"] != "My Book" || seenFields["library"] != "lib1" || seenFields["folder"] != "f1" {
		t.Errorf("wrong form fields: %+v", seenFields)
	}
	if seenFields["author"] != "Some Author" {
		t.Errorf("author = %q, want Some Author", seenFields["author"])
	}
	if seenFields["series"] != "" {
		t.Errorf("series should be unset (got %q)", seenFields["series"])
	}
	if got, want := strings.Join(seenFileNames, ","), "01.mp3"; got != want {
		t.Errorf("file names = %q, want %q", got, want)
	}
}

func TestUploadItem_NonSuccess(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/upload", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte("not allowed"))
	})
	api := newTestAPI(t, mux)

	err := api.UploadItem(context.Background(), abs.UploadItemInput{
		LibraryID: "lib1", FolderID: "f1", Title: "x",
		Files: []abs.UploadFile{{Name: "a.mp3", Open: openString("a")}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var ue *abs.UploadError
	if !errors.As(err, &ue) {
		t.Fatalf("error type = %T, want *UploadError", err)
	}
	if ue.StatusCode != 403 || !strings.Contains(ue.Body, "not allowed") {
		t.Errorf("unexpected UploadError: %+v", ue)
	}
}

func TestUploadItem_Validation(t *testing.T) {
	t.Parallel()
	api := newTestAPI(t, http.NewServeMux())
	cases := []abs.UploadItemInput{
		{}, // all empty
		{LibraryID: "lib", FolderID: "f", Title: "t"}, // no files
	}
	for i, in := range cases {
		if err := api.UploadItem(context.Background(), in); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestDiscover_FindsByTitle(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/api/libraries/lib1/search", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		calls++
		// Sanity: q must be the title we're looking for, URL-encoded.
		if got := r.URL.Query().Get("q"); got != "Wanted Title" {
			t.Errorf("q = %q, want %q", got, "Wanted Title")
		}
		w.Header().Set("Content-Type", "application/json")
		// On the second call, return a matching hit so we exercise the retry path.
		if calls < 2 {
			_, _ = w.Write([]byte(`{"book":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"book":[
			{"libraryItem":{"id":"itm-7","mediaType":"book","media":{
				"metadata":{"title":"Wanted Title","authorName":"Jane"}
			}}}
		]}`))
	})
	api := newTestAPI(t, mux)

	item, err := api.Discover(context.Background(), abs.DiscoverInput{
		LibraryID: "lib1",
		Title:     "Wanted Title",
		// Override the backoffs to keep tests fast.
		Backoffs: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if item.Id != "itm-7" {
		t.Errorf("item.Id = %q, want itm-7", item.Id)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one miss + one hit)", calls)
	}
}

func TestDiscover_TolerantMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		searchFor string
		wantQuery string // query the server should receive (post-sanitise)
		indexedAs string // title ABS's scanner ended up storing
	}{
		{
			name:      "subtitle suffix added by scanner",
			searchFor: "Amberlough",
			wantQuery: "Amberlough",
			indexedAs: "Amberlough - A Novel",
		},
		{
			name:      "colon stripped from upload title but preserved by scanner",
			searchFor: "Exodus The Archimedes Engine", // SafeFileName output
			wantQuery: "Exodus The Archimedes Engine",
			indexedAs: "Exodus: The Archimedes Engine",
		},
		{
			name:      "case differs",
			searchFor: "wanted title",
			wantQuery: "wanted title",
			indexedAs: "Wanted Title",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			mux.HandleFunc("/api/libraries/lib1/search", func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("q"); got != tc.wantQuery {
					t.Errorf("q = %q, want %q", got, tc.wantQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"book":[
					{"libraryItem":{"id":"itm-1","mediaType":"book","media":{
						"metadata":{"title":%q,"authorName":"Jane"}
					}}}
				]}`, tc.indexedAs)
			})
			api := newTestAPI(t, mux)

			item, err := api.Discover(context.Background(), abs.DiscoverInput{
				LibraryID: "lib1",
				Title:     tc.searchFor,
				Backoffs:  []time.Duration{time.Millisecond},
			})
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if item.Id != "itm-1" {
				t.Errorf("item.Id = %q, want itm-1", item.Id)
			}
		})
	}
}

func TestDiscover_TimeoutBuildsSchedule(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/api/libraries/lib1/search", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"book":[]}`))
	})
	api := newTestAPI(t, mux)

	// A tiny Timeout (no explicit Backoffs) must derive a short schedule and
	// give up quickly with ErrNotFound rather than hanging on the default.
	start := time.Now()
	_, err := api.Discover(context.Background(), abs.DiscoverInput{
		LibraryID: "lib1", Title: "absent",
		Timeout: 3 * time.Millisecond,
	})
	if !errors.Is(err, abs.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if calls == 0 {
		t.Error("expected at least one poll")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Discover took %s; Timeout budget was ignored", elapsed)
	}
}

func TestTriggerScan(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	var got struct {
		path   string
		method string
	}
	mux.HandleFunc("/api/libraries/lib1/scan", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		got.path, got.method = r.URL.Path, r.Method
		w.WriteHeader(200)
	})
	api := newTestAPI(t, mux)

	if err := api.TriggerScan(context.Background(), "lib1"); err != nil {
		t.Fatalf("TriggerScan: %v", err)
	}
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/api/libraries/lib1/scan" {
		t.Errorf("path = %q", got.path)
	}
}

func TestTriggerScan_NonAdmin403(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/libraries/lib1/scan", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	api := newTestAPI(t, mux)

	err := api.TriggerScan(context.Background(), "lib1")
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, want it to mention 403", err)
	}
}

func TestTriggerScan_Validation(t *testing.T) {
	t.Parallel()
	api := newTestAPI(t, http.NewServeMux())
	if err := api.TriggerScan(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty libraryID")
	}
}

func TestDiscover_NotFound(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/libraries/lib1/search", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"book":[]}`))
	})
	api := newTestAPI(t, mux)

	_, err := api.Discover(context.Background(), abs.DiscoverInput{
		LibraryID: "lib1", Title: "absent",
		Backoffs: []time.Duration{time.Millisecond, time.Millisecond},
	})
	if !errors.Is(err, abs.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
