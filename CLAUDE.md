# youtrack-docgen

Go CLI that collects infrastructure data (NetBox, Kubernetes manifests) into a neutral model and publishes generated documentation to the YouTrack Knowledge Base. Public repository (MIT): everything committed must be generic.

## Public-repo rules

- No internal hostnames, domains, IPs, tenant names, or real tokens anywhere: code, tests, fixtures, docs, examples. Fixtures are synthetic (`example.com`, RFC 5737/1918 ranges).
- Site-specific logic never lands here. It lives in a private repository (see `docs/overlay-pattern.md`). The `collector` and `model` packages are under `internal/`, so today such code needs a private fork or a patch series; a public collector API is not yet available.
- Redaction is a hard requirement: any change to a collector or renderer needs a test proving seeded fake secrets never reach a sink.

## Architecture

Pipeline: `collector` -> `model` -> `filter` (allow/deny, redaction) -> `render` -> `sink`.

- `model` is the stable center. Collectors and sinks never import each other.
- Renderers produce sink-neutral `Document` trees; YouTrack specifics live only in `internal/sink/youtrack`.
- Entity IDs are stable (`source:kind:key`); cross-source linking happens after collection, not inside collectors.

## YouTrack output contract

- Generated sections are wrapped in `docgen:begin/end <section-id>` markers; only marked blocks are replaced, manual content is preserved.
- Updates are idempotent: a second run on unchanged data makes no API call.
- Never delete articles. Objects that disappear get the `docgen-stale` tag and a banner.
- Article title is the hostname/service name; IPs, FQDN, and aliases are repeated as plain text so YouTrack search finds them.

## Conventions

- Go: `gofmt`, `go vet`, `golangci-lint`; table-driven tests; golden files for renderers (`testdata/`, update with `-update`).
- Config secrets only via `${ENV}` expansion in `docgen.yaml`; a missing required variable fails loudly.
- The real-YouTrack integration test is opt-in via env vars and must never run in default CI.

## Project management

- The design concept is kept out of this repository and out of superpowers plan files; it lives in the YouTrack KB.
- Work is tracked as YouTrack tickets and handled with `/youtrack-task`.
