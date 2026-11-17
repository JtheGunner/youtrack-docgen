<div align="center">

# 🗂️ youtrack-docgen

**Generate infrastructure documentation and publish it to the YouTrack Knowledge Base.**

<code>NetBox · Kubernetes manifests</code> &nbsp;→&nbsp; <code>neutral model</code> &nbsp;→&nbsp; <code>YouTrack KB articles</code>

[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-single%20binary-3776ab?style=flat-square&logo=go&logoColor=white)](#-development)
[![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux-0ea5e9?style=flat-square)](#-development)
[![Status](https://img.shields.io/badge/status-early%20development-f59e0b?style=flat-square)](#-status)
[![CI](https://img.shields.io/github/actions/workflow/status/JtheGunner/youtrack-docgen/ci.yml?branch=main&style=flat-square&label=CI)](https://github.com/JtheGunner/youtrack-docgen/actions/workflows/ci.yml)

</div>

---

## 🎯 What it is

Infrastructure documentation goes stale because nobody enjoys maintaining it. `youtrack-docgen` reads the systems that already know the truth, such as NetBox and Kubernetes manifests. It writes one searchable article per host and service into your YouTrack Knowledge Base.

The articles are built for troubleshooting: *what is this IP, where does it run, what does it depend on, and what depends on it?*

```text
  collectors ──▶ neutral model ──▶ filter ──▶ sinks
  NetBox            hosts, networks    allow/deny    YouTrack KB
  Kubernetes        services,          redaction     Markdown files
  your own          relations
```

Collectors and sinks never depend on each other. They only share the model, so a new source or output does not touch the other side.

### Diagrams

Every host article has a **Diagram** section with its direct relations (`runs_on`, `exposes`, `depends_on`, `same_as`, ...) and its networks. The Services overview has one diagram per service, and a **Network diagram** overview shows each network with its hosts. Without any network prefix it shows the IP addresses per host instead. Diagrams are capped for readability and are built only from the filtered graph, so denied entities never appear.

Diagrams are written as fenced `mermaid` code blocks. This assumes nothing about Mermaid support: where the target renders Mermaid the block becomes a drawing, and everywhere else it stays readable as plain text. There is no switch for this yet.

---

## 🚧 Status

The project is in early development. The pipeline runs end to end from NetBox and Kubernetes manifests to a Markdown directory with `generate`, and to the YouTrack Knowledge Base with `publish`. Run `publish` against a sandbox project first.

---

## ⚙️ Configuration

The tool reads `docgen.yaml`. Every collector and sink has its own block, so each module is configured on its own.

```yaml
youtrack:                      # connection, used by the youtrack sink
  url: https://youtrack.example.com
  token: ${YOUTRACK_TOKEN}
  project: INFRA
collectors:
  netbox:
    url: https://netbox.example.com
    token: ${NETBOX_TOKEN}
    allow: [{ tags: [documented] }]       # keep only entities matching one rule
    deny:  [{ roles: [lab] }]             # drop entities matching any rule; deny wins
  kubernetes:
    paths: [./kubernetes-config]
redact:                        # optional, adds to the built-in secret patterns
  deny_patterns: ['CORP-[A-Z]+-\d+']   # regexes removed from free text
  secret_keys: ['internal-.*']         # attribute/annotation keys whose value is dropped
sinks:
  youtrack:
    root_article: Infrastructure
  markdown:
    enabled: false             # modules can be switched off individually
    dir: ./out
```

- **Secrets come from the environment.** `${VAR}` must be set and non-empty. `${VAR:-default}` falls back to a default, and `$$` writes a literal `$`. All missing variables are reported at once, by name and config path, never with a value.
- **Unknown keys are errors.** Typos in the top level, in `youtrack`, and in the settings of the built-in modules (`netbox`, `kubernetes`, `youtrack`, `markdown`) are caught. Modules with another name, such as one from a private overlay, keep their own settings unchecked.
- **Allow/deny rules** are lists. A rule can combine `kinds`, `tags`, `roles`, `tenants`, `namespaces` and `labels` (all must match); values are case-insensitive globs. An empty rule is an error.
- **A disabled module still needs its variables.** Use `${VAR:-}` for a token you do not set while the module is off.

Check a config without running anything:

```text
youtrack-docgen validate-config --config docgen.yaml
```

---

## ▶️ Generate

`generate` runs the whole pipeline and writes the documentation as Markdown files, so you can review the result before anything touches YouTrack:

```text
youtrack-docgen generate --config docgen.yaml
```

- It uses every enabled collector, applies the allow/deny rules and redaction, renders the documents, and writes them with the `markdown` sink into `sinks.markdown.dir`.
- `index.md` is the entry page. Every other document gets its own file named after its ID, for example `netbox-host-srv01-example-com.md`. Files the tool does not generate are left alone.
- The `netbox` and `kubernetes` collectors are supported; set `enabled: false` on any other collector. Kubernetes workloads pinned to a node, services with `externalIPs` and ingress hostnames are linked to NetBox hosts (`runs_on`, `exposes`) when exactly one host matches by IP or hostname. An enabled `youtrack` sink is skipped with a note; use [`publish`](#-publish) to write to YouTrack.

---

## 🚀 Publish

`publish` runs the same pipeline and writes the documents to the YouTrack Knowledge Base:

```text
youtrack-docgen publish --config docgen.yaml
```

- It needs the `youtrack` sink to be enabled and `youtrack.url`, `youtrack.token` and `youtrack.project` to be set. `youtrack.project` is the project key, for example `INFRA`; an unknown key stops the run before anything is written.
- Articles are created or updated, never deleted. Only the blocks between the `docgen` markers are replaced; text you add outside them stays.
- An article whose object is gone from the sources gets the `docgen-stale` tag and a banner. Both are removed when the object comes back.
- If a collector or the filter fails, nothing is sent to YouTrack.
- The Markdown sink is not used by this command.
- A summary line closes the output, for example `sink youtrack: 9 created, 0 updated, 0 unchanged, 0 marked stale, 0 restored`.

> [!WARNING]
> Every article in the project that `docgen` created earlier and that is missing from the current run is marked as stale. Preview a run with `diff` first, and keep one project for one `docgen.yaml`.

---

## 🔍 Dry run and diff

`diff` shows what `publish` would create or update, and writes nothing. `publish --dry-run` does the same.

```text
youtrack-docgen diff --config docgen.yaml
youtrack-docgen publish --dry-run --config docgen.yaml
```

- It needs the `youtrack` sink to be enabled and `youtrack.url`, `youtrack.token` and `youtrack.project` to be set.
- It runs the same pipeline as `generate`, reads the articles of the project once, and compares them with the rendered documents. Only read requests are sent, and no Markdown files are written.
- Each article that would change gets a unified diff, headed by `--- a/<document key>`, or `--- /dev/null` for a new article. The first lines of the compared text show the title and the parent article. Manual content outside the `docgen` markers never appears as a change.
- A summary line closes the output, for example `sink youtrack (dry run): 9 to create, 0 to update, 0 unchanged`.
- Stale markings are not part of the preview yet.
- The output is plain text, without colour.

---

## 📚 Guides

| | Guide | What it covers |
|---|---|---|
| ⏰ | [Scheduled runs](docs/scheduled-runs.md) | Run the tool from cron or CI; examples in [`examples/`](examples) |
| 🔌 | [Write your own collector](docs/write-your-own-collector.md) | The `Collector` interface, the model, testing, wiring |
| 🧩 | [Overlay pattern](docs/overlay-pattern.md) | Keep site-specific collectors in a private repository |

---

## 🛠️ Development

Requirements: Go (see `go.mod` for the minimum version) and [golangci-lint](https://golangci-lint.run) v2.

| | Command | Purpose |
|---|---|---|
| 🔨 | `go build ./...` | Build everything |
| 🧪 | `go test -race ./...` | Run the tests |
| 🧹 | `golangci-lint run` | Lint and format checks |
| 🖼️ | `go test ./internal/<package> -update` | Rewrite that package's golden files from the current output |

Golden files live in each package's `testdata/` directory. Review their diff before committing.

### Real-YouTrack integration test

`internal/sink/youtrack/integration_test.go` publishes a small throwaway tree to a real YouTrack instance and checks create, idempotent re-run and update. It is skipped unless all of these variables are set, so default CI never runs it:

| Variable | Meaning |
|---|---|
| `YOUTRACK_IT_URL` | Base URL of the YouTrack instance |
| `YOUTRACK_IT_TOKEN` | Permanent token with write access to the sandbox project |
| `YOUTRACK_IT_PROJECT_ID` | Database ID of the sandbox project (for example `0-1`) |
| `YOUTRACK_IT_PROJECT_QUERY` | Search query that lists the sandbox project's articles (for example `project: SANDBOX`) |

```sh
go test ./internal/sink/youtrack -run TestIntegration -v
```

> [!WARNING]
> The sink marks every article matched by the query that it did not publish as stale. Use a dedicated sandbox project: the test aborts before writing if the query matches any article whose title does not start with `docgen-it-`.

docgen never deletes articles, so the test cannot either. Its articles are titled `docgen-it-<run id>` and `docgen-it-<run id>-child`; the test logs them at the end, and you remove them by hand.

---

## 📜 License

Released under the [MIT License](LICENSE).

---

## 🤝 Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md) for the full guide. For a code change:

1. Open an issue first if the change is larger than a small fix.
2. Branch off `main` and keep commits focused.
3. Run `go build ./...`, `go test -race ./...`, and `golangci-lint run` before opening a pull request.
4. Keep fixtures synthetic: no real hostnames, domains, IP addresses, or credentials.

---

<div align="center"><sub>Built to make infrastructure documentation write itself.</sub></div>
