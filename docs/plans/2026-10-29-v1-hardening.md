# proxctl V1 Hardening Implementation Plan

> Execute in this chat using the executing-plans and tdd-workflow skills.

**Goal:** Correct the reviewed V1 defects, add regression coverage, and document only supported behavior.

**Architecture:** Keep the existing Go CLI, JSON configuration, and standard-library HTTP proxy. Serialize configuration changes with an OS file lock, merge pending rule edits into the latest disk snapshot, and write credentials atomically with mode 0600. The server reloads validated rules every second, retaining the last valid snapshot on failure; listener ports, email, and certificate storage require restart. Cache proxies by domain and current target.

**Tech Stack:** Go 1.26, net/http, syscall.Flock on Linux/macOS, existing x/crypto/autocert. No new dependencies.

## Scope and decisions

- Keep existing root and user installations compatible. When a system configuration exists, select it before a personal configuration and report permission errors with a sudo hint; support `proxctl --config /absolute/path command` for explicit selection.
- New configuration directories use 0700, configuration and lock files use 0600, and loading an existing readable configuration tightens its permissions.
- Use request-local forwarding headers, sanitize client-supplied forwarding headers, and invalidate cached targets after rule changes.
- Handle CORS preflight before Basic Auth; actual protected requests still require authentication. Do not implicitly enable credentialed requests from arbitrary origins.
- Propagate configuration-save and systemctl errors. systemd units preserve the selected configuration path and reject unsupported platforms.
- Correct the install URL and document prerequisites, reload timing, configuration paths, daemon management, and failure recovery.
- Do not implement logs, expiry display, compression, rate limiting, or alternative certificate authorities in this change; remove unsupported claims.

## Task 1 — Regression tests (red)

Files: `internal/config/config_test.go`, `internal/cli/cli_test.go`, `internal/proxy/server_test.go`, plus focused regression test files.

Reproduce permission migration, independent config writers, rule reload/removal and malformed-file recovery, changing proxy targets, two-client forwarding, authenticated CORS, CLI error exit codes, daemon installation failures, and explicit config selection. Use temporary files and local test servers only.

Run: `go test ./internal/... -count=1` and confirm failures correspond to reviewed defects before implementation.

## Task 2 — Configuration and CLI fixes (green)

Files: `internal/config/config.go`, focused config storage file if needed, `internal/cli/cli.go`, `internal/cli/daemon.go`.

Implement atomic private writes and OS locking, merge rule mutations, validate boundary input, reload rules without overwriting runtime listener settings, parse explicit config paths before loading, preserve settings in the daemon unit, and return nonzero on failed actions.

Run focused config/CLI tests until green.

## Task 3 — Proxy and lifecycle fixes (green)

Files: `internal/proxy/server.go` and focused proxy lifecycle helpers/tests.

Implement periodic reload with cancellation, cache entries that track targets, Rewrite-based forwarding, correct CORS ordering, and reliable listener cleanup. Verify HTTP redirects, TLS routing with a local cached test certificate, and WebSocket forwarding without public ACME requests.

Run focused proxy tests until green.

## Task 4 — Documentation, installer and CI

Files: `README.md`, `scripts/install.sh`, `.github/workflows/release.yml`, CI workflow, `LICENSE`.

Fix the installation link, ensure shell failure cleanup, align feature claims, explain sudo/config paths and reload/restart behavior, add the declared MIT license, and run race/vet/build checks in CI for branches and pull requests.

## Task 5 — Final verification

Commands:

```sh
go test -race -coverprofile=/tmp/proxctl-hardening-coverage.out ./...
go tool cover -func=/tmp/proxctl-hardening-coverage.out
go vet ./...
bash -n scripts/install.sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/proxctl-linux-amd64 ./cmd/proxctl
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/proxctl-linux-arm64 ./cmd/proxctl
```

Expected: all tests/checks pass; at least 80% overall statement coverage. No external services or production systemd state are mutated. Real Linux systemd and public-domain ACME renewal remain manual acceptance checks and must be documented as unverified locally.

## Completion — 2026-10-29

- Tasks 1–5 completed on `codex/v1-hardening`; no dependencies added.
- Regression tests first reproduced the reviewed failures, then passed with the fixes.
- `go test -race -timeout 90s -coverprofile=... ./...`: passed; overall statement coverage **88.3%** (baseline **53.5%**).
- HTTP/HTTPS live routing, add/update/remove reloads, invalid-file recovery, WebSocket frames, concurrent processes, permission migration, CLI and simulated installer failures: passed.
- `go vet`, `bash -n`, `gofmt` and `git diff --check`: passed.
- Linux/macOS amd64/arm64 builds: passed. A native-binary CLI smoke check covers version/add/list/static/rm, file mode, and failure exit codes.
- Added Linux/macOS CI and strengthened release checks; these new workflow changes have not been pushed or executed on GitHub.
- Linux systemd boot behavior and public ACME issuance/renewal remain unverified locally. README records the acceptance steps and supported feature boundaries.
