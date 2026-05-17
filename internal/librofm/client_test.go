package librofm_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
)

func newClient(t *testing.T, mux *http.ServeMux) (*librofm.Client, string) {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cache := &librofm.TokenCache{Path: filepath.Join(t.TempDir(), "token")}
	c := librofm.NewClient(librofm.Options{BaseURL: srv.URL, TokenCache: cache})
	return c, srv.URL
}

func TestLogin_Success(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("User-Agent"); got != "okhttp/3.14.9" {
			t.Errorf("User-Agent = %q, want okhttp/3.14.9", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"grant_type":"password"`) {
			t.Errorf("body missing grant_type=password: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-abc","token_type":"bearer"}`))
	})
	c, _ := newClient(t, mux)

	if err := c.Login(context.Background(), "u", "p", false); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if c.Token() != "tok-abc" {
		t.Errorf("Token() = %q, want tok-abc", c.Token())
	}
}

func TestLogin_BadCredentials(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid email or password."}`))
	})
	c, _ := newClient(t, mux)

	err := c.Login(context.Background(), "u", "wrong", false)
	if !errors.Is(err, librofm.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	// The error_description from the response body should bubble up into the
	// error message so the user sees a human-readable hint.
	if !strings.Contains(err.Error(), "Invalid email or password") {
		t.Errorf("err = %v, want 'Invalid email or password' in message", err)
	}
}

func TestLogin_Non2xx_SurfacesErrorDescription(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"missing grant_type"}`))
	})
	c, _ := newClient(t, mux)

	err := c.Login(context.Background(), "u", "p", false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 422") || !strings.Contains(err.Error(), "missing grant_type") {
		t.Errorf("err = %v, want 'HTTP 422' + 'missing grant_type'", err)
	}
}

func TestLogin_TokenCacheHit(t *testing.T) {
	t.Parallel()
	// Pre-seed the cache so Login never hits HTTP.
	cachePath := filepath.Join(t.TempDir(), "token")
	if err := (librofm.TokenCache{Path: cachePath}).Save("preset-tok"); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(_ http.ResponseWriter, _ *http.Request) {
		t.Errorf("/oauth/token should not be called with cache hit")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := librofm.NewClient(librofm.Options{
		BaseURL:    srv.URL,
		TokenCache: &librofm.TokenCache{Path: cachePath},
	})

	if err := c.Login(context.Background(), "u", "p", false); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if c.Token() != "preset-tok" {
		t.Errorf("Token() = %q, want preset-tok", c.Token())
	}
}

func TestLibrary_Pagination(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"t"}`))
	})
	mux.HandleFunc("/api/v10/library", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer t" {
			t.Errorf("Authorization = %q, want Bearer t", got)
		}
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "1":
			_, _ = w.Write([]byte(`{"page":1,"total_pages":2,"audiobooks":[
				{"isbn":"111","title":"A","authors":["X"]}
			]}`))
		case "2":
			_, _ = w.Write([]byte(`{"page":2,"total_pages":2,"audiobooks":[
				{"isbn":"222","title":"B","authors":["Y"]}
			]}`))
		default:
			t.Errorf("unexpected page %q", page)
		}
	})
	c, _ := newClient(t, mux)
	if err := c.Login(context.Background(), "u", "p", false); err != nil {
		t.Fatal(err)
	}

	books, err := c.Library(context.Background())
	if err != nil {
		t.Fatalf("Library: %v", err)
	}
	if len(books) != 2 || books[0].ISBN != "111" || books[1].ISBN != "222" {
		t.Errorf("books = %+v", books)
	}
}

func TestMP3Manifest(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"t"}`))
	})
	mux.HandleFunc("/api/v10/download-manifest", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("isbn"); got != "9781234567890" {
			t.Errorf("isbn = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"isbn":"9781234567890",
			"parts":[{"url":"https://s3/x","size_bytes":1234}],
			"tracks":[{"number":1,"length_sec":60,"chapter_title":"Chapter 1"}]
		}`))
	})
	c, _ := newClient(t, mux)
	if err := c.Login(context.Background(), "u", "p", false); err != nil {
		t.Fatal(err)
	}

	m, err := c.MP3Manifest(context.Background(), "9781234567890")
	if err != nil {
		t.Fatalf("MP3Manifest: %v", err)
	}
	if len(m.Parts) != 1 || m.Parts[0].URL != "https://s3/x" {
		t.Errorf("Parts = %+v", m.Parts)
	}
	if len(m.Tracks) != 1 || m.Tracks[0].ChapterTitle != "Chapter 1" {
		t.Errorf("Tracks = %+v", m.Tracks)
	}
}

func TestM4BURL_Success(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"t"}`))
	})
	mux.HandleFunc("/api/v10/audiobooks/9781/packaged_m4b", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"m4b_url":"https://s3/m4b"}`))
	})
	c, _ := newClient(t, mux)
	if err := c.Login(context.Background(), "u", "p", false); err != nil {
		t.Fatal(err)
	}

	u, err := c.M4BURL(context.Background(), "9781")
	if err != nil {
		t.Fatalf("M4BURL: %v", err)
	}
	if u != "https://s3/m4b" {
		t.Errorf("url = %q", u)
	}
}

func TestM4BURL_NotFound(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"t"}`))
	})
	mux.HandleFunc("/api/v10/audiobooks/missing/packaged_m4b", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	c, _ := newClient(t, mux)
	if err := c.Login(context.Background(), "u", "p", false); err != nil {
		t.Fatal(err)
	}

	_, err := c.M4BURL(context.Background(), "missing")
	if !errors.Is(err, librofm.ErrNoM4B) {
		t.Fatalf("err = %v, want ErrNoM4B", err)
	}
}

func TestAudiobookDetails(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"t"}`))
	})
	mux.HandleFunc("/api/v10/explore/audiobook_details/9781", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"audiobook":{
			"isbn":"9781","title":"T","authors":["A"],"publisher":"P",
			"audiobook_info":{"narrators":["N"],"duration":3600,"track_count":5}
		}}}`))
	})
	c, _ := newClient(t, mux)
	if err := c.Login(context.Background(), "u", "p", false); err != nil {
		t.Fatal(err)
	}

	b, err := c.AudiobookDetails(context.Background(), "9781")
	if err != nil {
		t.Fatalf("AudiobookDetails: %v", err)
	}
	if b.Title != "T" || b.AudiobookInfo == nil || b.AudiobookInfo.Duration != 3600 {
		t.Errorf("book = %+v", b)
	}
}

func TestTokenCache_RoundTrip(t *testing.T) {
	t.Parallel()
	cache := librofm.TokenCache{Path: filepath.Join(t.TempDir(), "sub", "token")}
	if err := cache.Save("hello-token"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := cache.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "hello-token" {
		t.Errorf("got %q, want hello-token", got)
	}
	if err := cache.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, err = cache.Load()
	if err != nil {
		t.Fatalf("Load after Clear: %v", err)
	}
	if got != "" {
		t.Errorf("after Clear: got %q, want empty", got)
	}
}

func TestTokenCache_EmptyTokenRejected(t *testing.T) {
	t.Parallel()
	cache := librofm.TokenCache{Path: filepath.Join(t.TempDir(), "token")}
	if err := cache.Save(""); err == nil {
		t.Fatalf("Save(\"\") should error")
	}
}
