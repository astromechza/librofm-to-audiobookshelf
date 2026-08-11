package abs

import (
	"context"
	"errors"
	"fmt"
)

// TriggerScan asks ABS to scan a library's folders immediately (POST
// /api/libraries/{id}/scan) instead of waiting on the filesystem watcher,
// which can lag well past a minute on networked/LVM volumes. We call this
// after an upload so the new item is indexed promptly and becomes
// discoverable — see issue #4.
//
// The scan is asynchronous: ABS returns 200 right away and indexes in the
// background, so callers must still poll via Discover afterwards. A non-admin
// token yields 403; callers should treat that (and any error here) as
// non-fatal and fall back to poll-only discovery.
func (c *API) TriggerScan(ctx context.Context, libraryID string) error {
	if libraryID == "" {
		return errors.New("abs.TriggerScan: libraryID is required")
	}
	resp, err := c.ScanLibraryWithResponse(ctx, libraryID, &ScanLibraryParams{})
	if err != nil {
		return fmt.Errorf("abs.TriggerScan: %w", err)
	}
	if resp.StatusCode() != 200 {
		return fmt.Errorf("abs.TriggerScan: HTTP %d", resp.StatusCode())
	}
	return nil
}
