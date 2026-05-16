package librofm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DefaultTokenPath returns the path where we cache the libro.fm bearer token
// across runs. Prefers $XDG_RUNTIME_DIR (per-user 0700 on systemd hosts);
// falls back to ~/.cache/librofm-sync. /tmp is intentionally NOT used —
// symlink-racy on multi-user hosts.
func DefaultTokenPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); dir != "" {
		return filepath.Join(dir, "librofm-sync", "token"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("librofm: locate home dir: %w", err)
	}
	return filepath.Join(home, ".cache", "librofm-sync", "token"), nil
}

// TokenCache is a tiny on-disk file storing the libro.fm bearer token. The
// file is created with 0600 perms; the parent directory is created 0700.
type TokenCache struct {
	Path string
}

// Load returns the cached token. If the file is missing, ("", nil) is
// returned — callers should treat that as "no cache, must re-auth".
func (c TokenCache) Load() (string, error) {
	b, err := os.ReadFile(c.Path) // #nosec G304 — caller-controlled path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("librofm: read token cache: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Save writes the token to disk atomically (write to temp + rename). Creates
// the parent directory if missing. Resulting file mode is 0600.
func (c TokenCache) Save(token string) error {
	if token == "" {
		return errors.New("librofm: refuse to save empty token")
	}
	dir := filepath.Dir(c.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("librofm: mkdir %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".token-")
	if err != nil {
		return fmt.Errorf("librofm: open temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("librofm: chmod temp file: %w", err)
	}
	if _, err := tmp.WriteString(token); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("librofm: write token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("librofm: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, c.Path); err != nil {
		cleanup()
		return fmt.Errorf("librofm: rename token file: %w", err)
	}
	return nil
}

// Clear deletes the cached token, ignoring "file not found".
func (c TokenCache) Clear() error {
	if err := os.Remove(c.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("librofm: remove token cache: %w", err)
	}
	return nil
}
