# Overlay pattern for private collectors

Some knowledge is specific to one site: a particular network layout, an internal naming scheme, a source nobody else has. That logic does not belong in this public repository. The overlay pattern keeps it in a separate, private repository that builds on this one.

```text
  public: youtrack-docgen                private: your overlay
  ┌────────────────────────────┐         ┌──────────────────────────┐
  │ model, filter, render,     │ ◀────── │ site-specific collector  │
  │ sinks, collector.Collector │ builds  │ your docgen.yaml, CI     │
  └────────────────────────────┘ against └──────────────────────────┘
```

The overlay owns:

- one or more collectors that implement `collector.Collector`,
- its own `docgen.yaml` and scheduling (see [Scheduled runs](scheduled-runs.md)),
- the secrets, injected as environment variables.

The public repository owns everything else. Site-specific logic never flows back into it, and the public code never learns the overlay exists.

## What works today

> [!WARNING]
> The `Collector` interface and the `model` package are under `internal/`. The Go toolchain refuses to import `internal/` packages from another module, so a separate overlay module cannot compile against them as the repository is laid out today. The command line also has no registration hook: it builds its collectors from a fixed list in `internal/cli/generate.go`.

An overlay therefore has to take one of these routes until the public API is exposed:

| Route | How | Trade-off |
|---|---|---|
| Fork | Keep a private fork. Add the collector package and one `case` in `buildCollectors`. Merge upstream regularly. | Simple. You carry the merge work. |
| Private branch or patch set | Keep the overlay as a small patch series applied on top of a tagged release before building. | Smaller diff than a fork, but patches can conflict. |

Both routes keep the site-specific code in a private repository, which is the point of the pattern. What they cannot do is depend on a stable public Go API.

## What stays stable

Keep your collector inside the contract so it survives upstream changes and can move to a real overlay module later:

- Implement only `Name()` and `Collect(ctx, *model.Graph)`.
- Import nothing but `model` (and the standard library and your own packages). Never import another collector or a sink.
- Keep your settings in a `collectors.<name>` block of `docgen.yaml`. Modules with a name other than the built-in ones keep their settings unchecked by `validate-config`.
- Prefix your IDs with your own source name, so they never collide with public collectors.

See [Write your own collector](write-your-own-collector.md) for the details.

## Keeping the overlay private

- Do not copy site-specific names, addresses, or tokens into the public repository, including issues, pull requests, and test fixtures. Fixtures in public contributions use `example.com` and RFC 5737 or RFC 1918 ranges.
- Generic improvements you find while building an overlay (a missing model field, a filter option) are welcome as pull requests here.
