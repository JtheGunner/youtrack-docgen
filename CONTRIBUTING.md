# Contributing

Thanks for helping out. Issues and pull requests are welcome.

## Reporting issues

Describe what you ran, what you expected, and what happened. Do not paste real hostnames, IP addresses, tokens, or configuration from your site. Replace them with `example.com` names and documentation address ranges.

## Proposing changes

1. Open an issue first if the change is larger than a small fix.
2. Branch off `main` and keep commits focused. Use conventional commit subjects such as `feat:`, `fix:`, `docs:`, in the imperative, in English.
3. Add or update a test with every behaviour change.
4. Open a pull request that summarises the whole branch and lists how you tested it.

## Local development

Requirements: Go (see `go.mod` for the minimum version) and [golangci-lint](https://golangci-lint.run) v2.

```text
go build ./...
go test -race ./...
golangci-lint run
```

Run all three before you open a pull request. CI runs the same checks.

## Conventions

- **Style:** `gofmt`, `go vet`, and `golangci-lint` must pass.
- **Tests:** table-driven. Renderers and the CLI use golden files in `testdata/`. Rewrite them with `go test ./internal/<package> -update` and review the diff before committing.
- **Public repository:** everything committed must be generic. Fixtures, examples, and docs use `example.com` and RFC 5737 or RFC 1918 addresses. Never commit real hostnames, tokens, or site-specific logic. That belongs in a private [overlay](docs/overlay-pattern.md).
- **Redaction:** a change to a collector or renderer needs a test that proves seeded fake secrets never reach a sink.
- **Architecture:** the pipeline is collector, model, filter, render, sink. Collectors and sinks never import each other; they share only the model.
- **Secrets in config** come only from `${ENV}` expansion in `docgen.yaml`.

## Guides

- [Write your own collector](docs/write-your-own-collector.md)
- [Overlay pattern for private collectors](docs/overlay-pattern.md)
- [Scheduled runs](docs/scheduled-runs.md)
