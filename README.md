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

The project is in early development. The repository currently provides the neutral model, the configuration loader with `validate-config`, and the build, lint, and test scaffolding. Collectors, renderers, and the YouTrack sink are being added step by step.

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
    allow: { tags: [documented] }
    deny:  { roles: [lab] }
  kubernetes:
    paths: [./kubernetes-config]
sinks:
  youtrack:
    root_article: Infrastructure
  markdown:
    enabled: false             # modules can be switched off individually
    dir: ./out
```

- **Secrets come from the environment.** `${VAR}` must be set and non-empty. `${VAR:-default}` falls back to a default, and `$$` writes a literal `$`. All missing variables are reported at once, by name and config path, never with a value.
- **Unknown keys are errors.** Typos in the top level, in `youtrack`, and in the settings of the built-in modules (`netbox`, `kubernetes`, `youtrack`, `markdown`) are caught. Modules with another name, such as one from a private overlay, keep their own settings unchecked.
- **A disabled module still needs its variables.** Use `${VAR:-}` for a token you do not set while the module is off.

Check a config without running anything:

```text
youtrack-docgen validate-config --config docgen.yaml
```

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
