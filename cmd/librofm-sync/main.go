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
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/abs"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
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
		return errors.New("no subcommand; expected one of: probe-librofm, probe-abs, version")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch args[0] {
	case "version":
		fmt.Printf("librofm-sync %s (commit %s, built %s)\n", version, commit, date)
		return nil
	case "probe-librofm":
		return runProbeLibroFm(ctx, args[1:])
	case "probe-abs":
		return runProbeABS(ctx, args[1:])
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
		Logger:     logger,
		TokenCache: &librofm.TokenCache{Path: tokenPath},
	})

	if err := client.Login(ctx, *user, *pass, false); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	logger.Info("login OK", "token_cache", tokenPath)

	books, err := client.Library(ctx)
	if err != nil {
		return fmt.Errorf("library: %w", err)
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
