# ucmix — build conventions

`ucmix` is a Go library + CLI that controls PreSonus StudioLive Series III mixers
over the UCNET protocol. Library-first: `package ucmix` at the module root, CLI in
`cmd/ucmix`, everything else in `internal/<noun>`.

Unofficial — not affiliated with or endorsed by PreSonus. The README leads with that
disclaimer and the public repo must stay that way.

## Layout

- `ucmix.go` + root package — public library API (`Connect`, `Client`, helpers, options).
- `cmd/ucmix/main.go` — CLI entry, calls `internal/cli.Execute()`.
- `cmd/fakeboard/` — test-only UCNET server binary; excluded from GoReleaser builds.
- `internal/` — `proto`, `transport`, `state`, `schema`, `taper`, `boardconfig`,
  `fakeboard`, `cli`, `ui`, `errs`. No `pkg/`, no `utils`.

## Rules

- Commands constructor-style (`newXxxCmd() *cobra.Command`), no package-level command
  globals; thin, delegating to `internal/`.
- No mock frameworks. Seams are small interfaces + package-level func vars
  (`dial`, `sleep`).
- No code without tests. Table-driven, stdlib `testing` only, `t.TempDir()`/`t.Setenv()`.
- Run tests via `just` (gotestsum), never raw `go test` locally.
- Errors: `fmt.Errorf("context: %w", err)`; `errs.CLIError{Message, Hint}` surfaced
  centrally in `cmd/ucmix/main.go`; `SilenceUsage`/`SilenceErrors` on root.
- Dual styled / `--json` output on every command.
- Conventional commits; TDD (failing test → minimal code → pass → commit).

## Fixtures — public vs private

The public repo never contains real board data. `testdata/` holds only
sanitized/synthetic fixtures. Real captures live in Hugo
(`knowledge/ucnet-studiolive/`) and, for local runs, in the gitignored
`testdata/private/`. Tests must pass without the private tree.

## Source of truth

- Protocol spec + phased plan + design: Hugo `knowledge/ucnet-studiolive/`
  (`protocol-and-port-plan.md`, `ucmix-design.md`, `ucmix-implementation-plan.md`).
  Interface names in the design doc are authoritative.

## Distribution

GoReleaser → `steveclarke/homebrew-tap` (brew), plus deb/rpm via nfpms. Only
`cmd/ucmix` is released.
