# Repository Guidelines

## Project Structure & Module Organization

Hikami-Go is a Go 1.25 service with an embedded Vue 3 admin UI. The executable entry point lives in `cmd/hikami/`. Backend packages are under `internal/`: `handler` for Gin routes and WebSocket endpoints, `session` and `state` for lifecycle management, `worker` for tasks, `recap` for AI recap generation, and adapters such as `download`, `live_record`, `asr`, `upload`, and `publisher`. Test fixtures and recap examples are in `test-recap/`; planning notes are in `plans/`.

Frontend code (`web/src/`) follows a layered structure:
- `api/` — typed HTTP client layer, the only place that talks to the backend (no UI side effects in new wrappers).
- `stores/` — Pinia entity caches with `loaded`/`byId`/`ensureLoaded()` (inflight-deduped) and `getByIdAfterLoad(id)` for query consumers.
- `composables/` — cross-domain reusable hooks: `useAdminToken`, `useExpertMode`, `usePolling`, `useWebSocket`, and `useAppRefreshCoordinator` (single owner of WebSocket + degraded polling + terminal-state sessions refresh).
- `features/` — domain logic by business area (the core addition of the refactor):
  - `features/recaps/sessionActions.ts` — explicit action matrix for the two review-page entry points (row vs drawer; `UIActionName` 8 actions, not the lifecycle `SessionActionName`); covered by `sessionActions.test.ts`.
  - `features/recaps/components/` — split RecapsView sub-components (Toolbar/Filters/Table/Drawer).
  - `features/settings/components/` — split SettingsView sections (Publish/Recap/WebDAV/AdminToken/BiliAccounts/ConfigBackup) + shared `settings-cards.css`.
  - `features/channel/` + `features/onboarding/` — composables extracted from self-managing components (`useBiliQRCodeLogin`, `useRecapTemplateEditor`, `useGlossaryEntries`, `useOnboardingWizard`).
- `components/` — shared/presentational components (layout, session drawers, channel editors); `components/shared/` must not self-fetch stores.
- `views/` — thin route shells: data loading dispatch, store orchestration, action handlers; business UI is delegated to `features/` sub-components.

See `docs/FRONTEND_ARCHITECTURE.md` for the full current architecture, and `docs/archive/refactor/FRONTEND_REFACTOR_BASELINE.md` + `docs/archive/refactor/FRONTEND_REFACTOR_PLAN.md` for the pre-refactor snapshot and the approved (now fully implemented) plan.

## Build, Test, and Development Commands

- `make build`: build the Vue UI, embed `web/dist`, then compile `./hikami`.
- `make build-go`: compile only the Go binary from `./cmd/hikami`.
- `make web-dev`: run the Vite dev server from `web/`.
- `make web-build`: install frontend dependencies and produce the embedded UI bundle.
- `make run`: run `go run ./cmd/hikami -config config.yaml`.
- `make test`: run all Go tests with `go test ./...`.
- `make fmt`: apply `gofmt -w cmd internal`.
- `make tidy`: update Go module metadata.

## Coding Style & Naming Conventions

Keep Go code idiomatic and small: package names are lowercase, files may use snake_case, exported identifiers use PascalCase, and tests follow `*_test.go`. Run `gofmt` before submitting changes. Prefer focused packages and interfaces only where they reduce coupling. In the frontend, use PascalCase Vue components, clear TypeScript module names, and existing Element Plus and Pinia patterns.

## Testing Guidelines

Backend tests use Go's standard `testing` package. Place tests beside the package they cover, name them `TestXxx`, and use table-driven tests for branching behavior. Run `make test` before a PR. For frontend changes, run `cd web && npm run type-check`; run `cd web && npx vitest run` for unit tests (the action/state-machine service has a test matrix in `features/recaps/sessionActions.test.ts`); run `npm run build` when routing, imports, or Vite config changes.

## Commit & Pull Request Guidelines

Recent history mostly follows Conventional Commits, such as `feat(recap): ...`, `fix(runtime): ...`, and `style: ...`. Use scopes that match packages or areas (`ui`, `recap`, `scheduler`). PRs should describe behavior changes, list verification commands, link issues, and include screenshots for visible UI changes.

## Security & Configuration Tips

Do not commit `config.yaml`, cookies, API keys, generated databases, or local output directories. Use `config.example.yaml` or `config.full.example.yaml` as templates. External tools are runtime dependencies, not vendored assets: `ffmpeg`/`ffprobe` are required (startup fails if missing); `yt-dlp` and `rclone` are optional and probed at startup — yt-dlp is only needed for replay download / multi-P fallback / discovery, rclone only as a fallback when WebDAV or ASR temp lack a native backend. Missing optional tools degrade the corresponding capability (reflected in `runtime.Probe` capabilities), they do not block startup.

## Go Skills (samber/cc-skills-golang)

[samber/cc-skills-golang](https://github.com/samber/cc-skills-golang) is installed project-local at `.agents/skills/` (43 skills; gitignored via `.agents/`). Codex auto-discovers every `SKILL.md` there. When working on Go code, actively consult the relevant skill:

- **`golang-how-to`** is the orchestrator — start here; it loads the most relevant skills for the task (e.g. writing a gRPC service → golang-grpc + golang-testing + golang-error-handling; debugging a panic → golang-troubleshooting + golang-safety).
- By task: tests → `golang-testing` / `golang-stretchry-testify`; concurrency & races → `golang-concurrency` / `golang-safety`; errors → `golang-error-handling`; debugging → `golang-troubleshooting`; style & naming → `golang-code-style` / `golang-naming`; project layout → `golang-project-layout`; libraries → `golang-samber-lo` / `golang-samber-slog` / `golang-samber-oops` / `golang-spf13-cobra` / `golang-spf13-viper`.

Reference the skill explicitly in your reasoning when it applies, and follow its guidance over generic assumptions.

## Go build environment

Go commands in this environment require the module cache paths set explicitly (HOME/GOPATH/GOMODCACHE are empty in non-login shells). Prefix every `go` invocation:

```bash
HOME=/root GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod go build ./...
HOME=/root GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod go test ./...
```

If `/root/.cache/go-build` is read-only (sandboxed runs), add `GOCACHE=/tmp/hikami-go-build-cache`.
