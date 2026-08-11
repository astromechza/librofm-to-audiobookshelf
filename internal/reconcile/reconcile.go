// Package reconcile drives a single end-to-end sync run.
//
// Per [docs/03-architecture-decisions.md ADR-005], every run is a full
// reconciliation, not a diff-apply. For each libro.fm book we:
//
//  1. classify it against the current ABS library state, then
//  2. take the matching action — full sync, repair-only, or skip.
//
// The reconciler does NOT keep any state of its own across runs; audiobookshelf
// itself is the source of truth (ADR-003). That means the algorithm is
// idempotent: a partial failure on run N is observed and recovered on run N+1.
//
// State map (the only branching that matters):
//
//	NotPresent        download + upload + discover + PATCH /media + cover
//	PresentNoMetadata discover + PATCH /media + cover     (no re-download)
//	PresentSynced     skip
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/astromechza/librofm-to-audiobookshelf/internal/abs"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/format"
	"github.com/astromechza/librofm-to-audiobookshelf/internal/librofm"
)

// Downloader downloads one book into workDir. Both format.DownloadM4B and
// format.DownloadMP3 satisfy this shape; tests inject stubs.
type Downloader func(ctx context.Context, lf *librofm.Client, book librofm.Book, workDir string) (format.Result, error)

// Options configures a single run.
type Options struct {
	// Library is the human-readable ABS library name (matches `library.name`,
	// case-insensitive). Required.
	Library string

	// DryRun reports what would happen without calling any mutating ABS
	// endpoints. Read-only requests (list libraries/items) still fire.
	DryRun bool

	// Limit caps the number of libro.fm books to consider (debug aid).
	// Zero means "no limit".
	Limit int

	// WorkDir is the staging dir for downloads. Per-book subdirs are
	// created under it. Each subdir is removed after a successful upload.
	// Defaults to a fresh dir under os.TempDir() if empty.
	WorkDir string

	// DiscoverTimeout is the total polling budget for post-upload item
	// discovery. Zero uses abs.DefaultDiscoverTimeout. Bump this on slow
	// installs where ABS indexes uploads well past the default (issue #4).
	DiscoverTimeout time.Duration

	// Logger receives per-book progress at info/warn/error levels.
	// Defaults to slog.Default().
	Logger *slog.Logger

	// DownloadM4B / DownloadMP3 default to format.DownloadM4B /
	// format.DownloadMP3. Override for tests.
	DownloadM4B Downloader
	DownloadMP3 Downloader
}

// Summary is the aggregate result of a run.
type Summary struct {
	LibroFmTotal   int // total books in the libro.fm library
	Considered     int // after applying Limit
	AlreadyPresent int // skipped (already in ABS with good metadata)
	Synced         int // freshly downloaded + uploaded + patched
	Repaired       int // existed in ABS, only metadata re-patched
	Failed         int // per-book failure (others still proceed)
}

// Run executes the reconciliation. Returns the summary; the error is non-nil
// only on a setup-level failure (couldn't list libraries, etc.) — per-book
// failures are counted in Summary.Failed and logged, but the run continues.
func Run(ctx context.Context, lf *librofm.Client, api *abs.API, opts Options) (Summary, error) {
	if opts.Library == "" {
		return Summary{}, errors.New("reconcile: Options.Library is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.DownloadM4B == nil {
		opts.DownloadM4B = format.DownloadM4B
	}
	if opts.DownloadMP3 == nil {
		opts.DownloadMP3 = format.DownloadMP3
	}
	if opts.WorkDir == "" {
		d, err := os.MkdirTemp("", "librofm-sync-*")
		if err != nil {
			return Summary{}, fmt.Errorf("reconcile: temp work dir: %w", err)
		}
		opts.WorkDir = d
		defer func() { _ = os.RemoveAll(d) }()
	}

	target, err := selectLibrary(ctx, api, opts.Library)
	if err != nil {
		return Summary{}, err
	}
	if len(target.Folders) == 0 {
		return Summary{}, fmt.Errorf("reconcile: library %q has no folders", opts.Library)
	}
	folderID := target.Folders[0].Id
	opts.Logger.Info("target library", "id", target.Id, "name", target.Name, "folder", folderID)

	absItems, err := listAllItems(ctx, api, target.Id)
	if err != nil {
		return Summary{}, fmt.Errorf("reconcile: list ABS items: %w", err)
	}
	opts.Logger.Info("ABS library indexed", "items", len(absItems))

	librofmBooks, err := lf.Library(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("reconcile: list libro.fm library: %w", err)
	}
	summary := Summary{LibroFmTotal: len(librofmBooks)}
	opts.Logger.Info("libro.fm library fetched", "books", summary.LibroFmTotal)

	if opts.Limit > 0 && opts.Limit < len(librofmBooks) {
		librofmBooks = librofmBooks[:opts.Limit]
	}
	summary.Considered = len(librofmBooks)

	idx := indexABS(absItems)

	for _, book := range librofmBooks {
		state, item := idx.classify(book)
		log := opts.Logger.With("isbn", book.ISBN, "title", book.Title)

		switch state {
		case statePresentSynced:
			log.Info("already synced", "abs_item", safeID(item))
			summary.AlreadyPresent++

		case statePresentNoMetadata:
			log.Info("present but metadata incomplete; re-PATCH")
			if opts.DryRun {
				summary.Repaired++
				continue
			}
			if err := patchMetadata(ctx, api, item.Id, book, log); err != nil {
				log.Error("PATCH failed", "err", err)
				summary.Failed++
				continue
			}
			if err := setCover(ctx, api, item.Id, book.CoverURL, log); err != nil {
				log.Warn("cover failed (non-fatal)", "err", err)
			}
			summary.Repaired++

		case stateNotPresent:
			if opts.DryRun {
				log.Info("would download + upload + patch + cover")
				summary.Synced++
				continue
			}
			if err := syncOne(ctx, lf, api, opts, target.Id, folderID, book, log); err != nil {
				log.Error("sync failed", "err", err)
				summary.Failed++
				continue
			}
			summary.Synced++
		}
	}

	opts.Logger.Info("run complete",
		"librofm_total", summary.LibroFmTotal,
		"considered", summary.Considered,
		"already_present", summary.AlreadyPresent,
		"synced", summary.Synced,
		"repaired", summary.Repaired,
		"failed", summary.Failed,
	)
	return summary, nil
}

// syncOne is the NotPresent path: download → upload → discover → PATCH → cover.
// Any step failure aborts the rest for this book.
func syncOne(ctx context.Context, lf *librofm.Client, api *abs.API, opts Options,
	libraryID, folderID string, book librofm.Book, log *slog.Logger,
) error {
	bookWorkDir := filepath.Join(opts.WorkDir, book.ISBN)
	defer func() { _ = os.RemoveAll(bookWorkDir) }()

	res, err := opts.DownloadM4B(ctx, lf, book, bookWorkDir)
	if errors.Is(err, librofm.ErrNoM4B) {
		log.Info("no M4B; falling back to MP3 parts")
		res, err = opts.DownloadMP3(ctx, lf, book, bookWorkDir)
	}
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	log.Info("downloaded", "format", res.Format, "files", len(res.Files))

	uploadTitle := format.SafeFileName(book.Title)
	uploadAuthor := ""
	if len(book.Authors) > 0 {
		uploadAuthor = format.SafeFileName(book.Authors[0])
	}
	uploadSeries := format.SafeFileName(book.Series)

	if err := api.UploadItem(ctx, abs.UploadItemInput{
		LibraryID: libraryID,
		FolderID:  folderID,
		Title:     uploadTitle,
		Author:    uploadAuthor,
		Series:    uploadSeries,
		Files:     res.Files,
	}); err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	// Kick an explicit library scan so the item is indexed promptly rather
	// than waiting on ABS's filesystem watcher, which can lag past the poll
	// budget on networked/LVM volumes (issue #4). Non-fatal: a non-admin
	// token 403s here, in which case we fall back to poll-only discovery.
	if err := api.TriggerScan(ctx, libraryID); err != nil {
		log.Warn("scan trigger failed (non-fatal); falling back to poll-only", "err", err)
	}
	log.Info("upload accepted; polling for new item")

	item, err := api.Discover(ctx, abs.DiscoverInput{
		LibraryID: libraryID,
		Title:     uploadTitle,
		Timeout:   opts.DiscoverTimeout,
	})
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	log.Info("new item discovered", "abs_item", item.Id)

	if err := patchMetadata(ctx, api, item.Id, book, log); err != nil {
		return fmt.Errorf("PATCH metadata: %w", err)
	}
	if err := setCover(ctx, api, item.Id, book.CoverURL, log); err != nil {
		// Cover is non-fatal: item still exists with good metadata; user
		// can fix the cover manually. Don't blow up the run.
		log.Warn("cover failed (non-fatal)", "err", err)
	}
	return nil
}

func patchMetadata(ctx context.Context, api *abs.API, itemID string, book librofm.Book, log *slog.Logger) error {
	payload := buildMetadataPayload(book)
	resp, err := api.UpdateItemMediaWithResponse(ctx, itemID, payload)
	if err != nil {
		return err
	}
	if resp.StatusCode() != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode())
	}
	log.Info("metadata PATCHed")
	return nil
}

func setCover(ctx context.Context, api *abs.API, itemID, coverURL string, log *slog.Logger) error {
	if coverURL == "" {
		return errors.New("no cover URL")
	}
	resp, err := api.SetItemCoverWithResponse(ctx, itemID, abs.SetItemCoverJSONRequestBody{Url: coverURL})
	if err != nil {
		return err
	}
	if resp.StatusCode() != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode())
	}
	log.Info("cover set")
	return nil
}

// selectLibrary finds the ABS library whose name matches `wanted`
// (case-insensitive). Returns an error if not found.
func selectLibrary(ctx context.Context, api *abs.API, wanted string) (abs.Library, error) {
	resp, err := api.ListLibrariesWithResponse(ctx)
	if err != nil {
		return abs.Library{}, err
	}
	if resp.StatusCode() != 200 || resp.JSON200 == nil {
		return abs.Library{}, fmt.Errorf("ListLibraries: HTTP %d", resp.StatusCode())
	}
	want := strings.ToLower(wanted)
	for _, lib := range resp.JSON200.Libraries {
		if strings.ToLower(lib.Name) == want {
			return lib, nil
		}
	}
	names := make([]string, 0, len(resp.JSON200.Libraries))
	for _, lib := range resp.JSON200.Libraries {
		names = append(names, lib.Name)
	}
	return abs.Library{}, fmt.Errorf("library %q not found; available: %s", wanted, strings.Join(names, ", "))
}

// listAllItems paginates GET /api/libraries/:id/items?minified=1 until empty.
func listAllItems(ctx context.Context, api *abs.API, libraryID string) ([]abs.LibraryItem, error) {
	const pageSize = 200
	minified := abs.ListLibraryItemsParamsMinified("1")
	var all []abs.LibraryItem
	for page := 0; ; page++ {
		limit := pageSize
		params := &abs.ListLibraryItemsParams{
			Page:     &page,
			Limit:    &limit,
			Minified: &minified,
		}
		resp, err := api.ListLibraryItemsWithResponse(ctx, libraryID, params)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode() != 200 || resp.JSON200 == nil {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode())
		}
		all = append(all, resp.JSON200.Results...)
		// Stop when we've fetched everything: either fewer results than
		// the page size, or we've reached the reported total.
		if len(resp.JSON200.Results) < pageSize {
			break
		}
		if resp.JSON200.Total > 0 && len(all) >= resp.JSON200.Total {
			break
		}
	}
	return all, nil
}

// safeID returns the item ID or "<empty>" if the item is the zero value.
func safeID(item abs.LibraryItem) string {
	if item.Id == "" {
		return "<empty>"
	}
	return item.Id
}
