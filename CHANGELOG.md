# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `youtrack-docgen --version` prints the version and commit.
- Release workflow that builds macOS and Linux binaries (amd64, arm64) with SHA-256 checksums when a `v*` tag is pushed.
- Dependabot updates for Go modules and GitHub Actions.
- `govulncheck` in CI.
- Public Go packages `model`, `collector` and `docgen`: `collector.Collector`, `collector.Settings`, `collector.Secret` and `collector.Factory`, plus `docgen.New()`, `(*App).Register`, `(*App).Run`, `(*App).Main` and `docgen.ExitCode`, so a separate module can add collectors to the command line.
- `examples/overlay`, a separate module that builds youtrack-docgen with an extra collector; CI builds it.
- Collectors are registered by name; built-in collectors use the same path. An unknown collector name now fails `generate`, `diff` and `publish` with the list of registered names.

### Changed

- The YouTrack sink lists the articles of a project through the documented `GET /api/admin/projects/{id}/articles` endpoint instead of the undocumented `query` parameter of `GET /api/articles`. Project names with spaces no longer need quoting. The integration test also covers stale marking and restore, and the `YOUTRACK_IT_PROJECT_QUERY` variable is no longer used.
- The README lists the token permissions the YouTrack sink needs, and notes that the project article listing is only confirmed with an administrator token.
- Breaking: the `model` and `collector` packages moved out of `internal/` (`.../internal/model` is now `.../model`, `.../internal/collector` is now `.../collector`). Forks that imported them through a patch series must update their imports. The API is v0.x and may change; breaking changes are listed here.
