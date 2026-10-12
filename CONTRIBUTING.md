# Contributing

## Build

Go 1.26+ and [mise](https://mise.jdx.dev) for the pinned tools.

```sh
just setup    # installs go, gotestsum, golangci-lint, goreleaser, govulncheck, bats
just build    # builds ucmix to dist/
go build ./cmd/ucdaw
```

## Test

```sh
just test        # unit tests
just test-race   # with the race detector
just test-e2e    # BATS suite against the fake board
```

Run `golangci-lint run` before pushing. CI runs lint, tests, race, e2e and
govulncheck.

Tests never need a mixer or a running Studio Pro. Hardware tests
(`just test-hw`) are manual and never run in CI.

## No real captures

This repository is public. Do not commit packet captures, network addresses,
client or computer identifiers, licence replies, machine names, or anything
else taken from a real mixer or a real Studio Pro. Fixtures under `testdata/`
are hand-written or sanitized, and tests must pass without any private data.
Real captures stay out of the repo; `testdata/private/` is gitignored for local
use.

## Commits

Conventional commits (`feat(ucdaw): ...`, `fix: ...`, `docs: ...`). Prefer a
failing test first.
