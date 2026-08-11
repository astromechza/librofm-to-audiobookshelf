# librofm-to-audiobookshelf

Sync purchased audiobooks from [libro.fm](https://libro.fm) into a self-hosted
[audiobookshelf](https://www.audiobookshelf.org/) instance.

## Quickstart

One-shot reconciliation:

```bash
export LIBROFM_USER='you@example.com'
export LIBROFM_PASSWORD='your-libro-fm-password'
export ABS_URL='https://audiobookshelf.example.com'
export ABS_API_TOKEN='eyJ...'              # ABS UI → Settings → Users → API Tokens
export ABS_LIBRARY='Audiobooks'            # case-insensitive name of the target library

librofm-sync sync                          # the real thing
librofm-sync sync --dry-run                # preview what would happen
librofm-sync sync --limit 1 -v             # one book, debug logs
```

Live validation against real services before trusting `sync`:

```bash
librofm-sync probe-librofm                 # login + list your library
librofm-sync probe-abs                     # /api/me + list ABS libraries (proves Authelia bypass)
librofm-sync probe-download --isbn 9781…   # full download path for one book → ./out/
```

Schedule with cron, systemd-timer, or k8s CronJob — there's no built-in scheduler.

### Slow-indexing ABS instances

After uploading a book, the tool triggers an ABS library scan and then polls for
the new item so it can stamp the ISBN (its dedup key for future runs). On ABS
instances backed by networked/LVM volumes the filesystem watcher can lag past
the default poll budget; extend it with `--discover-timeout` / `DISCOVER_TIMEOUT`
(Go duration, default `5m`):

```bash
export DISCOVER_TIMEOUT='10m'              # wait up to 10 minutes for the item to index
librofm-sync sync --discover-timeout 10m   # or via flag
```

The scan trigger needs an admin API token; a non-admin token logs a warning and
falls back to poll-only discovery.

## Documentation

See [`docs/`](./docs):

- [01-libro-fm-protocol.md](./docs/01-libro-fm-protocol.md) — reverse-engineered libro.fm API
- [02-audiobookshelf-api.md](./docs/02-audiobookshelf-api.md) — ABS endpoints we use + Authelia bypass
- [03-architecture-decisions.md](./docs/03-architecture-decisions.md) — ADRs
- [04-implementation-plan.md](./docs/04-implementation-plan.md) — phased plan and module layout
- [05-probe-scripts.md](./docs/05-probe-scripts.md) — bash probes (no Go required)
- [06-code-quality-ci.md](./docs/06-code-quality-ci.md) — lint/test/Docker/GoReleaser/Actions strategy

## License

[MIT](./LICENSE).
