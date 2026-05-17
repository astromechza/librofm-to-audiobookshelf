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
