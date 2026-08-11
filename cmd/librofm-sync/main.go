// Command librofm-sync syncs purchased audiobooks from libro.fm into a
// self-hosted audiobookshelf instance.
//
// At this checkpoint only the two clients are wired up — the format adapters
// and reconciler arrive in later phases. The CLI exposes two hidden
// subcommands for live validation of the clients against real services:
//
//	librofm-sync probe-librofm    # login + list library
//	librofm-sync probe-abs        # /me + list libraries + first page of items
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/abs"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/format"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/reconcile"
)

// Build-time identifiers injected via -ldflags. See Dockerfile and
// .goreleaser.yaml for the wiring.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("no subcommand; expected one of: sync, probe-librofm, probe-abs, probe-download, version")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch args[0] {
	case "version":
		fmt.Printf("librofm-sync %s (commit %s, built %s)\n", version, commit, date)
		return nil
	case "sync":
		return runSync(ctx, args[1:])
	case "probe-librofm":
		return runProbeLibroFm(ctx, args[1:])
	case "probe-abs":
		return runProbeABS(ctx, args[1:])
	case "probe-download":
		return runProbeDownload(ctx, args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

// envFlag binds a *string flag to an env-var fallback so both `--name` and
// $NAME work, with the flag taking precedence.
func envFlag(fs *flag.FlagSet, name, env, usage string) *string {
	val := os.Getenv(env)
	return fs.String(name, val, fmt.Sprintf("%s (env: %s)", usage, env))
}

// envDurationFlag binds a *time.Duration flag to an env-var fallback so both
// `--name 10m` and $NAME=10m work, with the flag taking precedence. An
// unparseable env value falls back to def (the flag itself still validates).
func envDurationFlag(fs *flag.FlagSet, name, env string, def time.Duration, usage string) *time.Duration {
	if v := os.Getenv(env); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			def = d
		}
	}
	return fs.Duration(name, def, fmt.Sprintf("%s (env: %s)", usage, env))
}

// headerFlag wires a repeatable `--librofm-header KEY=VALUE` into an
// http.Header. Escape hatch for when libro.fm tightens its auth
// fingerprint and our hardcoded defaults stop working.
func headerFlag(fs *flag.FlagSet, name, usage string) *http.Header {
	h := http.Header{}
	fs.Func(name, usage, func(v string) error {
		idx := strings.IndexByte(v, '=')
		if idx <= 0 {
			return fmt.Errorf("expected KEY=VALUE, got %q", v)
		}
		h.Add(strings.TrimSpace(v[:idx]), strings.TrimSpace(v[idx+1:]))
		return nil
	})
	return &h
}

func setupLogger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func runProbeLibroFm(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("probe-librofm", flag.ContinueOnError)
	user := envFlag(fs, "librofm-user", "LIBROFM_USER", "libro.fm username (email)")
	pass := envFlag(fs, "librofm-password", "LIBROFM_PASSWORD", "libro.fm password")
	headers := headerFlag(fs, "librofm-header", "extra HTTP header for libro.fm requests, KEY=VALUE (repeatable). Overrides defaults like X-LibroFm-AppVer when libro.fm bumps required values.")
	verbose := fs.Bool("v", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" || *pass == "" {
		return errors.New("--librofm-user and --librofm-password (or LIBROFM_USER/LIBROFM_PASSWORD) are required")
	}

	logger := setupLogger(*verbose)
	tokenPath, err := librofm.DefaultTokenPath()
	if err != nil {
		return err
	}
	client := librofm.NewClient(librofm.Options{
		Logger:       logger,
		TokenCache:   &librofm.TokenCache{Path: tokenPath},
		ExtraHeaders: *headers,
	})

	if err := client.Login(ctx, *user, *pass, false); err != nil {
		return err
	}
	logger.Info("login OK", "token_cache", tokenPath)

	books, err := client.Library(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("library: %d books\n", len(books))
	for _, b := range books {
		auths := strings.Join(b.Authors, ", ")
		fmt.Printf("  %s\t%s\t%s\n", b.ISBN, b.Title, auths)
	}
	return nil
}

func runProbeABS(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("probe-abs", flag.ContinueOnError)
	url := envFlag(fs, "abs-url", "ABS_URL", "audiobookshelf base URL")
	token := envFlag(fs, "abs-token", "ABS_API_TOKEN", "audiobookshelf API token")
	verbose := fs.Bool("v", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" || *token == "" {
		return errors.New("--abs-url and --abs-token (or ABS_URL/ABS_API_TOKEN) are required")
	}

	logger := setupLogger(*verbose)
	api, err := abs.NewAPI(abs.Options{
		BaseURL: *url,
		Token:   *token,
		Logger:  logger,
	})
	if err != nil {
		return err
	}

	meResp, err := api.GetMeWithResponse(ctx)
	if err != nil {
		return fmt.Errorf("GetMe: %w", err)
	}
	if meResp.JSON200 == nil {
		return fmt.Errorf("GetMe: HTTP %d (likely Authelia intercepted — see docs/02-audiobookshelf-api.md)", meResp.StatusCode())
	}
	username := ""
	if meResp.JSON200.Username != nil {
		username = *meResp.JSON200.Username
	}
	fmt.Printf("/api/me OK: user=%s\n", username)

	libsResp, err := api.ListLibrariesWithResponse(ctx)
	if err != nil {
		return fmt.Errorf("ListLibraries: %w", err)
	}
	if libsResp.JSON200 == nil {
		return fmt.Errorf("ListLibraries: HTTP %d", libsResp.StatusCode())
	}
	fmt.Printf("libraries: %d\n", len(libsResp.JSON200.Libraries))
	for _, lib := range libsResp.JSON200.Libraries {
		fmt.Printf("  %s\t%s\t%s\t%d folder(s)\n", lib.Id, lib.Name, lib.MediaType, len(lib.Folders))
	}
	return nil
}

// runSync is the real product entry point: one full reconciliation pass.
//
// Per ADR-004, this is one-shot — schedule via cron / systemd-timer / k8s
// CronJob. Exit code 0 = no per-book failures; non-zero = at least one
// per-book failure (the run still touches every book; per-book errors don't
// abort the rest).
func runSync(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	user := envFlag(fs, "librofm-user", "LIBROFM_USER", "libro.fm username (email)")
	pass := envFlag(fs, "librofm-password", "LIBROFM_PASSWORD", "libro.fm password")
	absURL := envFlag(fs, "abs-url", "ABS_URL", "audiobookshelf base URL")
	absToken := envFlag(fs, "abs-token", "ABS_API_TOKEN", "audiobookshelf API token")
	library := envFlag(fs, "abs-library", "ABS_LIBRARY", "target ABS library name (case-insensitive)")
	workDir := envFlag(fs, "work-dir", "WORK_DIR", "directory to stage downloads in (default: temp)")
	discoverTimeout := envDurationFlag(fs, "discover-timeout", "DISCOVER_TIMEOUT", abs.DefaultDiscoverTimeout,
		"total budget to poll ABS for a freshly-uploaded item before giving up; raise this on slow-indexing setups")
	dryRun := fs.Bool("dry-run", false, "report what would happen without uploading or patching")
	limit := fs.Int("limit", 0, "cap the number of libro.fm books considered (0 = no limit)")
	headers := headerFlag(fs, "librofm-header", "extra HTTP header for libro.fm requests, KEY=VALUE (repeatable). Overrides defaults like X-LibroFm-AppVer.")
	verbose := fs.Bool("v", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" || *pass == "" {
		return errors.New("--librofm-user and --librofm-password (or LIBROFM_USER/LIBROFM_PASSWORD) are required")
	}
	if *absURL == "" || *absToken == "" {
		return errors.New("--abs-url and --abs-token (or ABS_URL/ABS_API_TOKEN) are required")
	}
	if *library == "" {
		return errors.New("--abs-library (or ABS_LIBRARY) is required")
	}

	logger := setupLogger(*verbose)

	tokenPath, err := librofm.DefaultTokenPath()
	if err != nil {
		return err
	}
	lf := librofm.NewClient(librofm.Options{
		Logger:       logger,
		TokenCache:   &librofm.TokenCache{Path: tokenPath},
		ExtraHeaders: *headers,
	})
	if err := lf.Login(ctx, *user, *pass, false); err != nil {
		return err
	}

	api, err := abs.NewAPI(abs.Options{
		BaseURL: *absURL,
		Token:   *absToken,
		Logger:  logger,
	})
	if err != nil {
		return err
	}
	// /me serves as the Authelia-bypass probe (docs/02-audiobookshelf-api.md).
	if meResp, err := api.GetMeWithResponse(ctx); err != nil {
		return fmt.Errorf("ABS /api/me: %w", err)
	} else if meResp.JSON200 == nil {
		return fmt.Errorf("ABS /api/me: HTTP %d (likely Authelia intercepted; see docs/02-audiobookshelf-api.md)", meResp.StatusCode())
	}

	summary, err := reconcile.Run(ctx, lf, api, reconcile.Options{
		Library:         *library,
		DryRun:          *dryRun,
		Limit:           *limit,
		WorkDir:         *workDir,
		DiscoverTimeout: *discoverTimeout,
		Logger:          logger,
	})
	if err != nil {
		return err
	}

	fmt.Printf("librofm=%d considered=%d synced=%d repaired=%d already=%d failed=%d\n",
		summary.LibroFmTotal, summary.Considered, summary.Synced,
		summary.Repaired, summary.AlreadyPresent, summary.Failed)
	if summary.Failed > 0 {
		return fmt.Errorf("%d book(s) failed; see logs", summary.Failed)
	}
	return nil
}

// runProbeDownload exercises the format package end-to-end against real
// libro.fm: login, pick the book matching --isbn from the library, try the
// M4B path with MP3 fallback, and stage the files in --out-dir. Useful to
// visually inspect a downloaded audiobook (and its ID3 tags) before relying
// on the reconciler.
func runProbeDownload(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("probe-download", flag.ContinueOnError)
	user := envFlag(fs, "librofm-user", "LIBROFM_USER", "libro.fm username (email)")
	pass := envFlag(fs, "librofm-password", "LIBROFM_PASSWORD", "libro.fm password")
	isbn := fs.String("isbn", "", "ISBN to download (must be in your library)")
	outDir := fs.String("out-dir", "./out", "directory to stage downloaded files into")
	headers := headerFlag(fs, "librofm-header", "extra HTTP header for libro.fm requests, KEY=VALUE (repeatable). Overrides defaults like X-LibroFm-AppVer.")
	verbose := fs.Bool("v", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *user == "" || *pass == "" {
		return errors.New("--librofm-user and --librofm-password (or LIBROFM_USER/LIBROFM_PASSWORD) are required")
	}
	if *isbn == "" {
		return errors.New("--isbn is required")
	}

	logger := setupLogger(*verbose)
	tokenPath, err := librofm.DefaultTokenPath()
	if err != nil {
		return err
	}
	client := librofm.NewClient(librofm.Options{
		Logger:       logger,
		TokenCache:   &librofm.TokenCache{Path: tokenPath},
		ExtraHeaders: *headers,
	})
	if err := client.Login(ctx, *user, *pass, false); err != nil {
		return err
	}

	books, err := client.Library(ctx)
	if err != nil {
		return err
	}
	var book librofm.Book
	for _, b := range books {
		if b.ISBN == *isbn {
			book = b
			break
		}
	}
	if book.ISBN == "" {
		return fmt.Errorf("ISBN %s not found in your library", *isbn)
	}
	fmt.Printf("found: %s — %s\n", book.Title, strings.Join(book.Authors, ", "))

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", *outDir, err)
	}

	res, err := format.DownloadM4B(ctx, client, book, *outDir)
	if errors.Is(err, librofm.ErrNoM4B) {
		fmt.Println("M4B not available; falling back to MP3")
		res, err = format.DownloadMP3(ctx, client, book, *outDir)
	}
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	fmt.Printf("format: %s   workdir: %s\n", res.Format, res.WorkDir)
	for _, f := range res.Files {
		fmt.Printf("  %s\n", f.Name)
	}
	return nil
}
