<div align="center">

# 🗂️ youtrack-docgen

**Generate infrastructure documentation and publish it to the YouTrack Knowledge Base.**

<code>NetBox · Kubernetes manifests</code> &nbsp;→&nbsp; <code>neutral model</code> &nbsp;→&nbsp; <code>YouTrack KB articles</code>

[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-single%20binary-3776ab?style=flat-square&logo=go&logoColor=white)](#-development)
[![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux-0ea5e9?style=flat-square)](#-development)
[![Status](https://img.shields.io/badge/status-early%20development-f59e0b?style=flat-square)](#-status)
[![CI](https://img.shields.io/github/actions/workflow/status/JtheGunner/youtrackt-docgen/ci.yml?branch=main&style=flat-square&label=CI)](https://github.com/JtheGunner/youtrackt-docgen/actions/workflows/ci.yml)

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

---

## 🚧 Status

The project is in early development. The pipeline runs end to end from NetBox to a Markdown directory with `generate`. The Kubernetes collector and the YouTrack sink are being added step by step.

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
- Only the `netbox` collector is supported so far. Set `enabled: false` on other collectors. An enabled `youtrack` sink is skipped with a note.

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

---

## 📜 License

Released under the [MIT License](LICENSE).

---

## 🤝 Contributing

Issues and pull requests are welcome. For a code change:

1. Open an issue first if the change is larger than a small fix.
2. Branch off `main` and keep commits focused.
3. Run `go build ./...`, `go test -race ./...`, and `golangci-lint run` before opening a pull request.
4. Keep fixtures synthetic: no real hostnames, domains, IP addresses, or credentials.

---

<div align="center"><sub>Built to make infrastructure documentation write itself.</sub></div>
