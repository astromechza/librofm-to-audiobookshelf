package abs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Client wraps the generated ClientWithResponses. It owns the Bearer auth
// injection, a redacting request logger, and an http.Client with sensible
// timeouts. Callers should always go through this type rather than calling
// NewClientWithResponses directly.
type API struct {
	*ClientWithResponses
	baseURL string
	token   string
	http    *http.Client
	logger  *slog.Logger
}

// Options for NewClient. Zero values are accepted for HTTPClient (defaults to a
// 60s-timeout client) and Logger (defaults to slog.Default()). BaseURL and
// Token are required.
type Options struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// NewClient constructs the wrapped client. It attaches a request editor that
// sets `Authorization: Bearer <token>` on every request, plus a transport
// that logs each request/response at slog.LevelDebug with the Authorization
// header redacted.
func NewAPI(opts Options) (*API, error) {
	if opts.BaseURL == "" {
		return nil, errors.New("abs: BaseURL is required")
	}
	if opts.Token == "" {
		return nil, errors.New("abs: Token is required")
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	inner := opts.HTTPClient.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	httpClient := *opts.HTTPClient
	httpClient.Transport = &loggingTransport{inner: inner, log: opts.Logger}

	editor := func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
		return nil
	}

	base := strings.TrimRight(opts.BaseURL, "/")
	gen, err := NewClientWithResponses(base, WithHTTPClient(&httpClient), WithRequestEditorFn(editor))
	if err != nil {
		return nil, fmt.Errorf("abs: construct generated client: %w", err)
	}
	return &API{
		ClientWithResponses: gen,
		baseURL:             base,
		token:               opts.Token,
		http:                &httpClient,
		logger:              opts.Logger,
	}, nil
}

// BaseURL returns the configured ABS base URL (without a trailing slash).
func (c *API) BaseURL() string { return c.baseURL }

// loggingTransport logs each request/response at debug level, redacting the
// Authorization header so the bearer never lands in a log line.
type loggingTransport struct {
	inner http.RoundTripper
	log   *slog.Logger
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.inner.RoundTrip(req)
	dur := time.Since(start)
	args := []any{
		"method", req.Method,
		"url", req.URL.Redacted(),
		"auth", redactAuthHeader(req.Header.Get("Authorization")),
		"duration_ms", dur.Milliseconds(),
	}
	if err != nil {
		args = append(args, "error", err.Error())
		t.log.Debug("abs http", args...)
		return resp, err
	}
	args = append(args, "status", resp.StatusCode)
	t.log.Debug("abs http", args...)
	return resp, nil
}

// redactAuthHeader replaces a Bearer token with `Bearer ***last8` so logs are
// useful for diagnostics without leaking the secret.
func redactAuthHeader(h string) string {
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "***"
	}
	tok := h[len(prefix):]
	if len(tok) <= 8 {
		return prefix + "***"
	}
	return prefix + "***" + tok[len(tok)-8:]
}
