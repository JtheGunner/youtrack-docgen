# Overlay pattern for private collectors

Some knowledge is specific to one site: a particular network layout, an internal naming scheme, a source nobody else has. That logic does not belong in this public repository. The overlay pattern keeps it in a separate, private repository that builds on this one.

```text
  public: youtrack-docgen                private: your overlay
  ┌────────────────────────────┐         ┌──────────────────────────┐
  │ model, collector, docgen   │ ◀────── │ site-specific collector  │
  │ (public Go API); filter,   │ imports │ main.go, docgen.yaml, CI │
  │ render, sinks stay inside  │         └──────────────────────────┘
  └────────────────────────────┘
```

The overlay owns:

- one or more collectors that implement `collector.Collector`,
- a small `main` that registers them and runs the command line,
- its own `docgen.yaml` and scheduling (see [Scheduled runs](scheduled-runs.md)),
- the secrets, injected as environment variables.

The public repository owns everything else. Site-specific logic never flows back into it, and the public code never learns the overlay exists.

## How it works

An overlay is a separate Go module that imports three public packages:

| | Package | Use |
|---|---|---|
| 🧱 | `github.com/JtheGunner/youtrack-docgen/model` | Entities, relations and the graph a collector fills |
| 🔌 | `github.com/JtheGunner/youtrack-docgen/collector` | The `Collector` interface, `Settings`, `Secret` and `Factory` |
| 🚀 | `github.com/JtheGunner/youtrack-docgen/docgen` | `docgen.New()`, `Register` and `Main` to build your own binary |

The overlay creates an app, registers its collectors under the names used in `docgen.yaml`, and runs it:

```go
package main

import (
	"fmt"
	"os"

	"github.com/JtheGunner/youtrack-docgen/docgen"
)

func main() {
	app := docgen.New()
	if err := app.Register("inventory", newInventory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	app.Main()
}
```

`newInventory` is a `collector.Factory`; see [Write your own collector](write-your-own-collector.md). The resulting binary has every command of `youtrack-docgen`, with the built-in `netbox` and `kubernetes` collectors plus yours. Registered collectors go through the same filter, redaction, linking and rendering as the built-in ones.

A complete, buildable module lives in [`examples/overlay`](../examples/overlay).

Unknown collector names fail `generate` with a list of the registered names. `validate-config` accepts any collector name.

## Stability

`model`, `collector` and `docgen` are the public API. Everything under `internal/` is not: do not depend on it and expect it to change without notice.

The API is v0.x. Breaking changes can happen in any release and are listed in [CHANGELOG.md](../CHANGELOG.md). Pin the version in your overlay's `go.mod` and read the changelog before you upgrade.

## What stays stable

Keep your collector inside the contract so it survives upstream changes:

- Implement `Name()` and `Collect(ctx, *model.Graph)`, and expose a `collector.Factory`.
- Import only `model`, `collector`, `docgen`, the standard library and your own packages. Never import another collector or a sink.
- Keep your settings in a `collectors.<name>` block of `docgen.yaml`. Settings of a collector with a name other than the built-in ones are not checked by `validate-config`; your factory validates them.
- Prefix your IDs with your own source name, so they never collide with public collectors.

See [Write your own collector](write-your-own-collector.md) for the details.

## Keeping the overlay private

- Keep the overlay repository private and depend on this one through its public module path. Nothing from the overlay needs to be published.
- Do not copy site-specific names, addresses, or tokens into the public repository, including issues, pull requests, and test fixtures. Fixtures in public contributions use `example.com` and RFC 5737 or RFC 1918 ranges.
- Generic improvements you find while building an overlay (a missing model field, a filter option) are welcome as pull requests here.
