# Overlay example

A minimal overlay: youtrack-docgen with one extra collector, `inventory`, built from outside the main module. It is a separate Go module (`example.com/overlay`) that depends on the public `docgen`, `collector` and `model` packages only.

| File | Purpose |
|---|---|
| `main.go` | Creates the app with `docgen.New()`, registers the collector, runs `app.Main()` |
| `collector.go` | The `inventory` collector: reads a JSON host list and adds one host entity per record |
| `inventory.json` | Synthetic sample data (`example.com`, RFC 5737 addresses) |
| `docgen.yaml` | Config with the `inventory` collector and the Markdown sink |

The collector gets its settings through `collector.Settings`; the `path` key of its block in `docgen.yaml` arrives in `Settings.Options["path"]`.

## Build and run

```text
cd examples/overlay
go build ./...
go run . generate --config docgen.yaml
```

The Markdown documents are written to `./out` (git-ignored).

The `replace` directive in `go.mod` points at the repository root so the example always builds against the current sources. An overlay in its own repository drops it and requires a released version instead.
