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

## 📦 Install

Pin a released version so runs are reproducible. Replace `v0.1.0` with the release you want; see [Releases](https://github.com/JtheGunner/youtrack-docgen/releases) and the [changelog](CHANGELOG.md).

**With Go:**

```sh
go install github.com/JtheGunner/youtrack-docgen/cmd/youtrack-docgen@v0.1.0
```

**From a release binary** (macOS and Linux, `amd64` and `arm64`). Download the archive named `youtrack-docgen_<version>_<os>_<arch>.tar.gz` and `checksums.txt` from the release page, then verify and unpack:

```sh
sha256sum --ignore-missing -c checksums.txt   # macOS: shasum -a 256 --ignore-missing -c checksums.txt
tar -xzf youtrack-docgen_v0.1.0_linux_amd64.tar.gz
```

Check what you installed:

```sh
youtrack-docgen --version
```

Avoid `@main` in automation: it changes under you.

---

## ⚙️ Configuration

The tool reads `docgen.yaml`. Every collector and sink has its own block, so each module is configured on its own.

```yaml
allow_insecure_http: false    # optional: accept http:// URLs on non-loopback hosts (see below)
youtrack:                      # connection, used by the youtrack sink
  url: https://youtrack.example.com
  token: ${YOUTRACK_TOKEN}
  project: INFRA
  max_stale_ratio: 0.5         # optional: largest share of articles one run may mark as stale
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
- **Tokens only travel over HTTPS.** A `url` with `http://` next to a token is an error, because the token would cross the network unencrypted. `localhost`, `127.0.0.0/8` and `::1` are always allowed, for example for a local test instance. Set `allow_insecure_http: true` at the top level to accept plain HTTP on other hosts.
- **The YouTrack client retries transient failures.** Reads and idempotent writes are retried up to 5 times with exponential backoff (at most 30 seconds per wait) on HTTP 429, 502, 503, 504 and network errors; `Retry-After` is honored as seconds or as an HTTP date. Creating an article or tag is retried on 429 only, so a retry can never create a duplicate. Error messages never contain the token, even if the server echoes it.
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
- From NetBox, a host article shows the FQDN and a link to the NetBox record, and the VLAN overview shows VLAN ID, prefix, gateway and host count. NetBox has no gateway field: the collector reads the custom field `gateway` of a prefix, or else the first IP address in the prefix that carries the tag `gateway`.

### Kubernetes inputs

The `kubernetes` collector reads plain YAML manifests from the files and directories in `paths`. It never talks to a cluster.

- **Supported:** `Namespace`, `Node`, `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, `CronJob`, `Service`, `Ingress`, and `List` wrappers around them. Other kinds (CRDs, `Kustomization`, `Secret`, ...) are ignored without looking at their `spec`.
- **Directories** are walked in lexical order. Hidden directories and non-YAML files are skipped. A symlinked path is resolved; symlinked directories below it are not followed.
- **Labels:** `metadata.labels` is copied to the entity with a `label.` prefix, so `labels` allow/deny rules match them as `label.<key>` (for example `labels: { label.team: payments }`). The prefix keeps a manifest from spoofing the reserved dimensions `namespace`, `role`, `tenant` and `tag.*`. The `namespace` label is always the object's namespace. Annotations, Secret data, environment values and container arguments are never read. Node labels are not read.
- **Not rendered:** Helm templates and Kustomize overlays are not rendered. Point `paths` at rendered output (for example from `helm template` or `kustomize build`). Files that are not valid YAML, such as templates containing `{{ }}`, and documents with an unexpected shape, such as JSON6902 patches, are skipped with a warning naming the file; the run continues.
- **Duplicates:** if the same object (kind, namespace, name) appears more than once, for example a Kustomize base and an overlay patch, the first one in path order and lexical file order wins. Later ones are skipped with a warning.
- **Warnings** go to standard error. A configured path that yields no objects at all produces a warning.
- `spec.replicas` may be an integer or a quoted integer.

---

## 🚀 Publish

`publish` runs the same pipeline and writes the documents to the YouTrack Knowledge Base:

```text
youtrack-docgen publish --config docgen.yaml
```

- It needs the `youtrack` sink to be enabled and `youtrack.url`, `youtrack.token` and `youtrack.project` to be set. `youtrack.project` is the project key, for example `INFRA`; an unknown key stops the run before anything is written.
- Only articles of that project are read, changed or marked. Articles of other projects are never touched.
- Articles are created or updated, never deleted. Only the blocks between the `docgen` markers are replaced; text you add outside them stays.
- The markers are HTML comments: `<!-- docgen:doc <key> -->` names the document, and `<!-- docgen:begin <section> hash=… -->` to `<!-- docgen:end <section> -->` wraps one generated section. They are still recognised after an editor changed line endings or spacing.
- A generated section that the tool no longer produces is removed from its article, and so is a second copy of a section.
- Do not remove the `docgen:doc` line. An article without it that carries the title of a generated document is left untouched, no second article is created, and a warning names the line to restore. The same protects a manual article that happens to have the title of a document. The title is the only link left: if the title was changed too, a new article is created, and the old one is reported when it still holds generated sections.
- If you delete one marker of a section by hand, the other one stays as plain text and the section is written again at the end of the article. Your text is never treated as part of a section: a begin marker is only paired with an end marker before the next begin marker of the same section. The run warns about the article; remove the leftover marker line to silence it.
- A marker only counts on a line of its own. Do not put marker lines into your own text, for example in a code block: a marked section the tool does not produce is removed.
- Host names with spaces or a double hyphen get an encoded key that starts with `esc:`.
- An article whose object is gone from the sources gets the `docgen-stale` tag and a banner, and its generated sections are removed, so the names and addresses of a vanished or denied host do not stay in the Knowledge Base. Text you wrote outside the markers stays. When the object comes back, the sections return and tag and banner are removed.
- Only articles of sources that took part in the run can become stale. If you switch a collector off, its articles are left alone.
- A source whose collection was incomplete is treated like a switched-off one for that run: its articles are not marked as stale, and a warning says so. For `kubernetes` this means a skipped file or document, a Namespace without a name, or a configured path without any object. `generate` stays tolerant and only prints the warnings.
- A run that would mark more than `youtrack.max_stale_ratio` of the managed articles as stale (default `0.5`) stops before the first write, with the counts in the error. This is what an empty or broken collection looks like. Run again with `--allow-mass-stale` if the objects really are gone.
- A run on unchanged data writes nothing. The "Generated at" line of an article keeps the time of its last real change.
- If a collector or the filter fails, nothing is sent to YouTrack.
- The Markdown sink is not used by this command. The Markdown sink never removes files: files of objects that no longer exist stay in the output directory until you delete them.
- A summary line closes the output, for example `sink youtrack: 9 created, 0 updated, 0 unchanged, 0 marked stale, 0 restored`.

> [!WARNING]
> Every article in the project that `docgen` created earlier, belongs to an active source and is missing from the current run is marked as stale. Keep one project for one `docgen.yaml`, run `diff` first, and try a new config against a sandbox project.

---

## 🔍 Dry run and diff

`diff` shows what `publish` would create, update, mark as stale or restore, and writes nothing. `publish --dry-run` does the same.

```text
youtrack-docgen diff --config docgen.yaml
youtrack-docgen publish --dry-run --config docgen.yaml
```

- It needs the `youtrack` sink to be enabled and `youtrack.url`, `youtrack.token` and `youtrack.project` to be set.
- It runs the same pipeline as `generate`, reads the articles of the project once, and compares them with the rendered documents. Only read requests are sent, and no Markdown files are written.
- Each article that would change gets a unified diff, headed by `--- a/<document key>`, or `--- /dev/null` for a new article. The first lines of the compared text show the title and the parent article. Manual content outside the `docgen` markers never appears as a change.
- A change to the stale marking shows as a diff of the article: the banner is added or removed, the generated sections disappear or return, and a `Tags: docgen-stale` line appears or goes.
- `diff` and `publish` take their decisions from the same list of operations, so the preview names exactly the writes `publish` makes.
- If `publish` would stop at the stale limit, `diff` prints the preview and then fails with the same error. Pass `--allow-mass-stale` to preview the run without the limit.
- A summary line closes the output, for example `sink youtrack (dry run): 9 to create, 0 to update, 0 unchanged, 0 to mark stale, 0 to restore`.
- The output is plain text, without colour.
- `diff` exits with status 0 whether or not articles would change. Pass `--exit-code` to exit with status 2 when they would, so a CI job can detect drift between the sources and the Knowledge Base. Errors always exit with status 1.

---

## 🚦 Exit codes and aborting

| Status | Meaning |
|---|---|
| 0 | Success. For `diff --exit-code`: nothing would change |
| 1 | Any error: invalid config, a failing collector, filter or sink, or an abort by signal |
| 2 | Only `diff --exit-code`: articles would change |

`SIGINT` (Ctrl-C) and `SIGTERM` cancel the running command through its context. Collectors and sinks stop, and the command exits with status 1. The Markdown sink writes every file through a temporary file and a rename, so an aborted run never leaves a half-written file; a symlink in the output directory is replaced, never followed.

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

## 📝 Changelog

Release notes are kept in [CHANGELOG.md](CHANGELOG.md).

---

## 🤝 Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md) for the full guide. For a code change:

1. Open an issue first if the change is larger than a small fix.
2. Branch off `main` and keep commits focused.
3. Run `go build ./...`, `go test -race ./...`, and `golangci-lint run` before opening a pull request.
4. Keep fixtures synthetic: no real hostnames, domains, IP addresses, or credentials.

---

<div align="center"><sub>Built to make infrastructure documentation write itself.</sub></div>
