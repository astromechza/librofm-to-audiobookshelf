// Package librofm is the libro.fm HTTP client.
//
// libro.fm has no published API. The endpoints we hit are reverse-engineered
// from the open-source Android-app-mimicking clients listed in
// docs/01-libro-fm-protocol.md. The auth flow is OAuth2 password grant
// returning a long-lived bearer token; do NOT confuse this with a browser
// session.
package librofm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the production API root. The User-Agent we send mimics the
// Android app so libro.fm doesn't return a different shape for "web" callers.
const (
	DefaultBaseURL   = "https://libro.fm"
	defaultUserAgent = "okhttp/3.14.9"
)

// Sentinel errors callers may want to match with errors.Is.
var (
	// ErrUnauthorized is returned for 401 responses. Trigger: token expired or
	// rejected. Caller should re-auth and retry once. The error string keeps
	// the US spelling to match `http.StatusUnauthorized` / RFC 7235.
	ErrUnauthorized = errors.New("librofm: unauthorized") //nolint:misspell // RFC-7235 / stdlib alignment
	// ErrNoM4B is returned by M4BURL when libro.fm has no packaged M4B for
	// the ISBN (HTTP 404). Caller should fall back to MP3 manifest.
	ErrNoM4B = errors.New("librofm: no packaged M4B for ISBN")
)

// Client speaks to libro.fm.
type Client struct {
	baseURL    string
	http       *http.Client
	userAgent  string
	logger     *slog.Logger
	extraHdrs  http.Header
	tokenCache TokenCache
	token      string
}

// Options for NewClient.
type Options struct {
	// BaseURL overrides the default. Empty string means use DefaultBaseURL.
	BaseURL string
	// HTTPClient overrides the default 60s-timeout client.
	HTTPClient *http.Client
	// Logger receives debug-level request/response logs with secrets redacted.
	Logger *slog.Logger
	// ExtraHeaders are appended to every request. Useful escape hatch if
	// libro.fm starts demanding new headers (app-version, device-id, etc.)
	// without us cutting a release.
	ExtraHeaders http.Header
	// TokenCache, when non-nil, persists the bearer token across runs.
	TokenCache *TokenCache
}

// NewClient constructs a libro.fm client. The token isn't fetched until
// Login() is called; Login is a no-op if the TokenCache already has a token.
func NewClient(opts Options) *Client {
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	c := &Client{
		baseURL:   base,
		http:      opts.HTTPClient,
		userAgent: defaultUserAgent,
		logger:    opts.Logger,
		extraHdrs: opts.ExtraHeaders.Clone(),
	}
	if opts.TokenCache != nil {
		c.tokenCache = *opts.TokenCache
	}
	return c
}

// Login obtains and caches a bearer token. If a cached token is already
// present (from a previous run or a prior Login on this instance), Login is
// a no-op. Pass force=true to re-authenticate even with a cached token.
func (c *Client) Login(ctx context.Context, username, password string, force bool) error {
	if !force && c.token != "" {
		return nil
	}
	if !force && c.tokenCache.Path != "" {
		if tok, err := c.tokenCache.Load(); err != nil {
			return err
		} else if tok != "" {
			c.token = tok
			return nil
		}
	}

	body, err := json.Marshal(loginRequest{
		GrantType: "password",
		Username:  username,
		Password:  password,
	})
	if err != nil {
		return fmt.Errorf("login: marshal request: %w", err)
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/oauth/token", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		body := readBodySnippet(resp.Body)
		c.logger.Debug("librofm login non-2xx", "status", resp.StatusCode, "body", body)

		// Build the cleanest description we can:
		//   - prefer OAuth-parsed detail (`error_description` > `error`)
		//   - fall back to the raw body snippet
		//   - fall back to a "HTTP N with empty body" marker so the user
		//     never sees a bare 'unauthorized' with no context.
		detail := parseOAuthDetail(body)
		var msg string
		switch {
		case detail != "":
			msg = detail
		case body != "":
			msg = body
		default:
			msg = fmt.Sprintf("HTTP %d with empty body", resp.StatusCode)
		}

		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("%w: %s", ErrUnauthorized, msg)
		}
		return fmt.Errorf("login: HTTP %d: %s", resp.StatusCode, msg)
	}

	var out loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("login: decode response: %w", err)
	}
	if out.AccessToken == "" {
		return errors.New("login: empty access_token in response")
	}
	c.token = out.AccessToken

	if c.tokenCache.Path != "" {
		if err := c.tokenCache.Save(c.token); err != nil {
			// Cache failures are warnings; the in-memory token still works.
			c.logger.Warn("librofm: failed to cache token", "err", err)
		}
	}
	return nil
}

// Library walks /api/v10/library and returns every audiobook. Pages are
// fetched serially; libro.fm has not been observed to rate-limit but we
// stay conservative.
func (c *Client) Library(ctx context.Context) ([]Book, error) {
	var all []Book
	for page := 1; ; page++ {
		var p LibraryPage
		if err := c.getJSON(ctx, fmt.Sprintf("/api/v10/library?page=%d", page), &p); err != nil {
			return nil, fmt.Errorf("librofm.Library page %d: %w", page, err)
		}
		all = append(all, p.Audiobooks...)
		if p.TotalPages <= 0 || page >= p.TotalPages {
			break
		}
	}
	return all, nil
}

// MP3Manifest fetches the multi-part MP3 download manifest for an ISBN.
// Each manifest.Parts[i].URL is a presigned S3 URL with a short TTL — fetch
// promptly.
func (c *Client) MP3Manifest(ctx context.Context, isbn string) (DownloadManifest, error) {
	if isbn == "" {
		return DownloadManifest{}, errors.New("librofm.MP3Manifest: empty isbn")
	}
	var m DownloadManifest
	if err := c.getJSON(ctx, "/api/v10/download-manifest?isbn="+url.QueryEscape(isbn), &m); err != nil {
		return DownloadManifest{}, fmt.Errorf("librofm.MP3Manifest: %w", err)
	}
	return m, nil
}

// M4BURL fetches the packaged-M4B download URL for an ISBN. Returns
// ErrNoM4B (wrapped) on 404, signalling the caller should fall back to MP3.
func (c *Client) M4BURL(ctx context.Context, isbn string) (string, error) {
	if isbn == "" {
		return "", errors.New("librofm.M4BURL: empty isbn")
	}
	req, err := c.newAuthRequest(ctx, http.MethodGet, "/api/v10/audiobooks/"+url.PathEscape(isbn)+"/packaged_m4b", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("librofm.M4BURL: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("librofm.M4BURL %s: %w", isbn, ErrNoM4B)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrUnauthorized
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("librofm.M4BURL %s: HTTP %d: %s", isbn, resp.StatusCode, readBodySnippet(resp.Body))
	}
	var out M4BResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("librofm.M4BURL: decode: %w", err)
	}
	if out.URL == "" {
		return "", errors.New("librofm.M4BURL: empty m4b_url in response")
	}
	return out.URL, nil
}

// AudiobookDetails fetches enriched metadata for any ISBN. Useful when the
// /library entry is sparse and we need fields like description or
// publication_date.
func (c *Client) AudiobookDetails(ctx context.Context, isbn string) (Book, error) {
	if isbn == "" {
		return Book{}, errors.New("librofm.AudiobookDetails: empty isbn")
	}
	var out audiobookDetailsResponse
	if err := c.getJSON(ctx, "/api/v10/explore/audiobook_details/"+url.PathEscape(isbn), &out); err != nil {
		return Book{}, fmt.Errorf("librofm.AudiobookDetails: %w", err)
	}
	return out.Data.Audiobook, nil
}

// Download GETs an arbitrary URL (typically a presigned S3 URL from a
// manifest or M4BURL). Bearer auth is attached only if the URL host ends in
// `libro.fm` — presigned S3 URLs already carry their own auth and would
// reject extra headers in some configurations.
//
// The caller MUST close the returned io.ReadCloser.
func (c *Client) Download(ctx context.Context, fileURL string) (io.ReadCloser, int64, error) {
	u, err := url.Parse(fileURL)
	if err != nil {
		return nil, 0, fmt.Errorf("librofm.Download: parse url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("librofm.Download: build request: %w", err)
	}
	c.applyDefaultHeaders(req)
	if strings.HasSuffix(u.Hostname(), "libro.fm") && c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("librofm.Download: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		body := readBodySnippet(resp.Body)
		resp.Body.Close()
		return nil, 0, fmt.Errorf("librofm.Download %s: HTTP %d: %s", u.Host, resp.StatusCode, body)
	}
	return resp.Body, resp.ContentLength, nil
}

// Token returns the current cached bearer token, or empty if not logged in.
// Exposed mostly for diagnostic printing.
func (c *Client) Token() string { return c.token }

// internals -------------------------------------------------------

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := c.newAuthRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, readBodySnippet(resp.Body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// newRequest is the shared http.NewRequest builder. It sets the User-Agent
// and any configured ExtraHeaders, but does NOT add Authorization — that's
// the caller's job (some endpoints, like /oauth/token, are auth-less).
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("librofm: build request: %w", err)
	}
	c.applyDefaultHeaders(req)
	return req, nil
}

// newAuthRequest is like newRequest but adds the bearer token. Returns an
// error if Login has not been called.
func (c *Client) newAuthRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if c.token == "" {
		return nil, errors.New("librofm: not logged in (call Login first)")
	}
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return req, nil
}

func (c *Client) applyDefaultHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.userAgent)
	for k, vs := range c.extraHdrs {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := c.http.Do(req)
	dur := time.Since(start)
	args := []any{
		"method", req.Method,
		"url", req.URL.Redacted(),
		"duration_ms", dur.Milliseconds(),
	}
	if req.Header.Get("Authorization") != "" {
		args = append(args, "auth", "Bearer ***")
	}
	if err != nil {
		args = append(args, "error", err.Error())
		c.logger.Debug("librofm http", args...)
		return nil, err
	}
	args = append(args, "status", resp.StatusCode)
	c.logger.Debug("librofm http", args...)
	return resp, nil
}

// readBodySnippet pulls up to 2 KiB of body for diagnostics. Errors are
// swallowed — the body is already in the error path, we just want to give a
// human-readable hint.
func readBodySnippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 2048))
	return strings.TrimSpace(string(b))
}

// parseOAuthDetail pulls the human-readable string out of an OAuth2-style
// error body. libro.fm typically returns something like
//
//	{"error":"invalid_grant","error_description":"Invalid email or password."}
//
// on bad credentials, but the actual shape isn't documented — older
// responses may include only `error`, and some non-OAuth errors are plain
// text or empty.
//
// Returns "" if the body has nothing parseable. The caller decides what to
// substitute (e.g. the raw body snippet, or a sentinel like "empty body").
func parseOAuthDetail(body string) string {
	if body == "" {
		return ""
	}
	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	switch {
	case payload.ErrorDescription != "":
		return payload.ErrorDescription
	case payload.Error != "":
		return payload.Error
	}
	return ""
}
