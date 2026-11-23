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

Infrastructure documentation goes stale because nobody enjoys maintaining it. `youtrack-docgen` reads the systems that already know the truth, such as NetBox and Kubernetes manifests. It writes one searchable article per host into your YouTrack Knowledge Base. Kubernetes objects get articles per cluster and namespace; in the KB they sit below a `Kubernetes` article: `Kubernetes / <cluster> / <namespace>`. The Markdown output of `generate` lists the clusters directly in `index.md`.

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

By default a diagram is written as a plain list of its relations, one per line (`web01 depends_on db01`). That reads the same in every target. Set `diagrams: mermaid` in `docgen.yaml` to write fenced `mermaid` code blocks instead; use it when your target draws Mermaid.

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
diagrams: text                 # optional: "text" (default, a list of relations) or "mermaid" (a mermaid code block)
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
    paths:
      - path: ./kubernetes-config
        cluster: prod                                   # required: the cluster these manifests describe
      - path: ./gitops
        cluster: prod
        repo_url: https://git.example.com/ops/gitops    # optional: the repository the manifests come from
redact:                        # optional, adds to the built-in secret patterns
  deny_patterns: ['CORP-[A-Z]+-\d+']   # regexes removed from free text
  secret_keys: ['internal-.*']         # attribute/annotation keys whose value is dropped
sinks:
  youtrack:
    root_article: Infrastructure   # optional: title of the root article (default: "Infrastructure documentation")
  markdown:
    enabled: false             # modules can be switched off individually
    dir: ./out
```

- **Secrets come from the environment.** `${VAR}` must be set and non-empty. `${VAR:-default}` falls back to a default, and `$$` writes a literal `$`. All missing variables are reported at once, by name and config path, never with a value.
- **Tokens only travel over HTTPS.** A `url` with `http://` next to a token is an error, because the token would cross the network unencrypted. `localhost`, `127.0.0.0/8` and `::1` are always allowed, for example for a local test instance. Set `allow_insecure_http: true` at the top level to accept plain HTTP on other hosts.
- **The YouTrack client retries transient failures.** Reads and idempotent writes are retried up to 5 times with exponential backoff (at most 30 seconds per wait) on HTTP 429, 502, 503, 504 and network errors; `Retry-After` is honored as seconds or as an HTTP date. Creating an article or tag is retried on 429 only, so a retry can never create a duplicate. Error messages never contain the token, even if the server echoes it.
- **Unknown keys are errors.** Typos in the top level, in `youtrack`, and in the settings of the built-in modules (`netbox`, `kubernetes`, `youtrack`, `markdown`) are caught. Modules with another name, such as one from a private overlay, keep their own settings unchecked.
- **Allow/deny rules** are lists. A rule can combine `kinds`, `tags`, `roles`, `tenants`, `namespaces` and `labels` (all must match); values are case-insensitive globs. An empty rule is an error.
- **Rules and settings without effect are errors.** `validate-config` rejects:
  - a `kinds` value that matches no kind of the collector; the message lists the valid ones (`host`, `interface`, `ipaddress`, `network`, `site` for `netbox`; `cluster`, `host`, `ingress`, `namespace`, `service`, `workload` for `kubernetes`). Kinds are model kinds in lowercase: `Deployment` is wrong, `workload` is right;
  - rule fields the collector never fills: `namespaces` on `netbox`; `tags`, `roles` and `tenants` on `kubernetes`; and `labels` keys the collector never sets (`netbox`: `role`, `tenant`, `tag.*`; `kubernetes`: `namespace`, `label.*`);
  - settings the module does not read: `url` and `token` on `kubernetes`, `url` and `token` on the sinks, and `allow`/`deny` on a sink.
- **Allow rules keep what belongs to the allowed entity.** With `allow: [{ tags: [documented] }]`, a matching host also keeps its interfaces, their IP addresses, the networks those addresses are in, and the sites the host is in. Networks and sites are only references: they are not expanded further (a network's own site or VLAN is not taken along), and a deny rule always wins, so a denied network or site stays out. Entities that no allow rule matches and no allowed host references are removed.
- **The run reports what the filter removed.** `generate`, `diff` and `publish` print one line per collector to stderr, for example `filter netbox: kept 12, removed 30 by allow, 2 by deny`, and a warning when the allow rules of a collector leave no entity at all. The output contains counts only, never the name of a removed entity.
- **What a deny rule also removes:** a denied entity appears in no form in the output, and nothing in the output or in a warning names it.
  - **Host:** its interfaces and the IP addresses that belong only to it, its same-machine twins from other sources, its name and aliases, its short name (the first label of a fully qualified name), its addresses in every notation, and the reverse-DNS names (`in-addr.arpa`, `ip6.arpa`) of those addresses are removed from every other entity. An entity that is itself named after one of them is removed.
  - **IP address entity:** the address is removed from every host, interface and text, together with its reverse-DNS name. Its DNS name and aliases are removed from every other entity too.
  - **Cluster:** `kinds: [cluster]` in a deny rule removes the cluster with all its namespaces, workloads, services and ingresses. It matches every cluster: no rule can name one cluster, so to leave out a single cluster, remove its entries from `paths`. A cluster is also kept whenever one of its namespaces is kept, even if an allow list names only namespaces, so a kept namespace never loses its parent. A deny on a namespace name (`namespaces: [quarantine]`) covers that name in every cluster.
  - **Namespace:** its workloads, services and ingresses are removed with their relations. Their names, their cluster-internal DNS names (`name.namespace`, `name.namespace.svc`, `name.namespace.svc.cluster.local`), ingress hosts and addresses are removed from the text of every other entity, except values that an entity which stays owns itself (its own name, alias or address). Names under 4 characters are only removed in the qualified forms.
  - **Known limitation:** a short name is only removed from free text if it is at least 4 characters long and stands at word boundaries (`lab01` goes, `lab011` and `lab01-eth0` stay). Shorter, generic short names such as `db` or `web` are not removed. A side effect: an object whose own name contains a removed short name (for example a namespace `lab01`) is removed too.
- **A disabled module still needs its variables.** Use `${VAR:-}` for a token you do not set while the module is off.

Check a config without running anything:

```text
youtrack-docgen validate-config --config docgen.yaml
```

---

## 🔒 Redaction

Every entity passes the redactor before it is rendered, whatever the sink.

- **Free text** (descriptions, labels, attributes, aliases): private key blocks, bearer tokens and `Authorization: Basic` headers, JWTs, AWS keys, GitHub, GitLab and Slack tokens, YouTrack permanent tokens, credentials in URLs (`scheme://user:password@host/path` becomes `[REDACTED]`), and assignments such as `password=...`, `pwd: ...`, `passphrase: ...`, `token=...` or `api_key=...` are replaced with `[REDACTED]`. Your own `redact.deny_patterns` come on top.
  - **URL credentials fail closed:** a password may contain `@` and `/`, so the end of the credentials cannot be told from the host. A URL is therefore replaced as a whole, up to the next whitespace, when the text between `://` and the first `/` contains a `:` and the URL contains an `@`. The host disappears with it, and so does a harmless URL such as `https://host.example.com:8443/path/a@b`. A URL without a `:` in that part (`ssh://git@host/repo`) carries no password and stays readable. The separators may be escaped: `%2F`, `&#47;`, `&#x2F;`, `&sol;`, JSON `\u002f` and extra backslashes count as `/`, and the same goes for `:` and `@`. Only a literal `/` ends the part before the credentials, because an escaped one can be part of the user name. Forms outside these lists are not recognised. A scheme-relative `//user:password@host` has no scheme to find and is not redacted. A password with unencoded whitespace is only covered up to the whitespace; RFC 3986 requires such characters to be percent-encoded.
- **Keys:** attribute, label and annotation values are dropped when the key looks secret (password, secret, token, auth, `pwd`, `passphrase`, ...) or matches `redact.secret_keys`. Environment values, Secret data and last-applied manifests are always dropped.
- **Names and ID keys:** the name and the ID key of an entity are its article title, table cells and diagram labels, and every reference uses the key. If the redactor would change either one, the whole entity is removed, like a denied host, and so are its relations. Its article is never published; one that already exists is handled as for any removed object, also when a source was read incompletely. Removing is safer than masking, because a masked name would still leak through the key and the references.

---

## ▶️ Generate

`generate` runs the whole pipeline and writes the documentation as Markdown files, so you can review the result before anything touches YouTrack:

```text
youtrack-docgen generate --config docgen.yaml
```

- It uses every enabled collector, applies the allow/deny rules and redaction, renders the documents, and writes them with the `markdown` sink into `sinks.markdown.dir`.
- `index.md` is the entry page. Every other document gets its own file named after its ID, for example `netbox-host-srv01-example-com.md`. Files the tool does not generate are left alone.
- The `netbox` and `kubernetes` collectors are supported; set `enabled: false` on any other collector. Kubernetes objects are linked to NetBox hosts as described under [Linking Kubernetes and NetBox](#linking-kubernetes-and-netbox). An enabled `youtrack` sink is skipped with a note; use [`publish`](#-publish) to write to YouTrack.
- NetBox does not keep names unique. When several objects map to the same ID (a device name on two sites, a device and a VM with one name, anycast addresses, a VLAN ID in two groups, a prefix twice), each of them gets a suffix with its NetBox ID, such as `~device12`. Objects without a collision keep their plain ID. Interfaces of one host that share a name get the suffix `~nic<ID>`. Objects that cannot be mapped are skipped with a warning, and so are the IP addresses assigned to a skipped interface, so an address never appears without its host.
- From NetBox, a host article shows the FQDN and a link to the NetBox record, and the VLAN overview shows VLAN ID, prefix, gateway and host count. NetBox has no gateway field: the collector reads the custom field `gateway` of a prefix, or else the first IP address in the prefix that carries the tag `gateway`.

### Kubernetes inputs

The `kubernetes` collector reads plain YAML manifests from the files and directories in `paths`. It never talks to a cluster.

- **Supported:** `Namespace`, `Node`, `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, `CronJob`, `Service`, `Ingress`, and `List` wrappers around them. Other kinds (CRDs, `Kustomization`, `Secret`, ...) are ignored without looking at their `spec`.
- **Directories** are walked in lexical order. Hidden directories and non-YAML files are skipped. A symlinked path is resolved; symlinked directories below it are not followed.
- **Labels:** `metadata.labels` is copied to the entity with a `label.` prefix, so `labels` allow/deny rules match them as `label.<key>` (for example `labels: { label.team: payments }`). The prefix keeps a manifest from spoofing the reserved dimensions `namespace`, `role`, `tenant` and `tag.*`. The `namespace` label is always the object's namespace. Annotations, Secret data, environment values and container arguments are never read. Node labels are not read.
- **Cluster:** every entry of `paths` is a map with `path` and `cluster`; a plain string is rejected. The cluster name is lowercase letters, digits, `.`, `_` and `-`, and starts and ends with a letter or digit (`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`). Several entries may name the same cluster. The manifests do not carry a cluster name, so this is how the tool tells two clusters apart: the same namespace name in two clusters is two namespaces.
- **Articles:** the KB shows `Kubernetes / <cluster> / <namespace>`. The cluster article lists its namespaces. The namespace article is titled with the namespace name, repeats the cluster name as plain text, and lists the workloads, services and ingresses of that namespace as plain text, so YouTrack search finds them. Workloads, services and ingresses get no articles of their own; nodes are host articles.
- **IDs:** entity IDs are `kubernetes:cluster:<cluster>`, `kubernetes:namespace:<cluster>/<namespace>`, `kubernetes:workload:<cluster>/<namespace>/<kind>/<name>`, and `kubernetes:service:<cluster>/<namespace>/<name>` or `kubernetes:ingress:<cluster>/<namespace>/<name>`. `generate` derives the Markdown file names from them (`kubernetes-namespace-prod-shop.md`), and `publish` writes them into the document markers.
- **Repository link:** an entry of `paths` may carry an optional `repo_url` (an `http(s)` URL). The URL is attached to the objects read from that path, and the **Links** section of a namespace or host article shows it as `Repository for kubernetes: <url>`. Without `repo_url` no repository link appears. Credentials, query and fragment are stripped from the URL before it is shown.
- **Not rendered:** Helm templates and Kustomize overlays are not rendered. Point `paths` at rendered output (for example from `helm template` or `kustomize build`). Files that are not valid YAML, such as templates containing `{{ }}`, and documents with an unexpected shape, such as JSON6902 patches, are skipped with a warning naming the file; the run continues.
- **Duplicates:** if the same object (kind, namespace, name) appears more than once within one cluster, for example a Kustomize base and an overlay patch, the first one in path order and lexical file order wins. Later ones are skipped with a warning that names the cluster. Nodes are matched by name across all entries and clusters: a node name that appears in two clusters is one host.
- **Warnings** go to standard error. A configured path that yields no objects at all produces a warning.
- `spec.replicas` may be an integer or a quoted integer.

### Linking Kubernetes and NetBox

After collection, hosts of different sources are matched and Kubernetes objects are attached to NetBox hosts. A relation is only set when the match is unique; an ambiguous match sets nothing and prints a warning on standard error.

- **Same machine (`same_as`):** two hosts of different sources are the same machine when they share an IP address or a hostname (name or alias, case-insensitive). A bare short name such as `web01` also matches the first label of `web01.example.com`; two fully qualified names with the same first label do not match. The relation is set only when each host has exactly one candidate in the other source.
- **Ambiguity fails closed for deny rules:** an ambiguous match is never published as a relation, but the deny rules still count it. If one candidate is denied, the other host (for example the Kubernetes node) may be the same machine and is withheld too. Its other candidates are not denied by this. The warning only names hosts that are published; a warning that would mention a denied host is not printed.
- **VRFs:** NetBox addresses keep their VRF. The same address in two different VRFs is two addresses. A Kubernetes node carries no VRF, so an address that exists in several VRFs on several NetBox hosts is ambiguous for it.
- **Workloads (`runs_on`):** a workload pinned by `nodeName` or by a `kubernetes.io/hostname` node selector runs on the NetBox host its node is the same machine as. This needs the node's addresses or name to match, which usually means a `Node` manifest.
- **Services (`exposes`):** a NetBox host that owns one of the service's `externalIPs` or load balancer IPs (`status.loadBalancer.ingress[].ip`) exposes the service. Cluster IPs and node ports are not used.
- **Ingresses (`exposes`):** a NetBox host whose name or alias equals an ingress hostname (or its short name) exposes the ingress.

> [!NOTE]
> A workload that is not pinned to a node is not placed on any host. Without `Node` manifests the cluster says nothing about where it runs, and the tool does not guess. Host articles say "Nothing is known to run here" in that case; it does not mean that nothing runs there.

### Derived dependencies

After the filter has run, `depends_on` relations are derived from the filtered graph only, so a rejected entity never shows up in a dependency. Exactly these edges are derived, and they are not chained:

- **Ingress -> Service:** an ingress that routes to a service depends on it.
- **Service -> Workload:** a service whose selector matches a workload depends on it.
- **Workload -> Host:** a workload that runs on a host depends on it.
- **Host -> Host:** a host that runs on another host depends on it. The NetBox collector sets this for a virtual machine whose `device` field names its hypervisor.
- **VM -> Cluster:** a NetBox virtual machine depends on the cluster entity of its source that its `cluster` attribute names. The NetBox collector does not emit cluster entities yet, so this edge only appears once a source provides them. Until then a VM shows its cluster only as the `cluster` attribute.

Host articles list these under **Depends on** and **Needed by**, and the diagrams draw them. An empty section reads "Not known", not "none": no source can state that nothing depends on a host.

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
- A newly created host article ends with a **Manual notes** section outside the markers, as a visible place for your own text. It is written once, when the article is created; later runs never change it, and existing articles do not gain it.
- The root article is titled `sinks.youtrack.root_article`, or "Infrastructure documentation" when that is not set. The setting also titles the root document of `generate`.
- A generated section that the tool no longer produces is removed from its article, and so is a second copy of a section.
- Do not remove the `docgen:doc` line. An article without it that carries the title of a generated document is left untouched, no second article is created, and a warning names the line to restore. The same protects a manual article that happens to have the title of a document. The title is the only link left: if the title was changed too, a new article is created, and the old one is reported when it still holds generated sections.
- If you delete one marker of a section by hand, the other one stays as plain text and the section is written again at the end of the article. Your text is never treated as part of a section: a begin marker is only paired with an end marker before the next begin marker of the same section. The run warns about the article; remove the leftover marker line to silence it.
- A marker only counts on a line of its own. Do not put marker lines into your own text, for example in a code block: a marked section the tool does not produce is removed.
- Host names with spaces or a double hyphen get an encoded key that starts with `esc:`.
- An article whose object is gone from the sources gets the `docgen-stale` tag and a banner, and its generated sections are removed, so the names and addresses of a vanished or denied host do not stay in the Knowledge Base. Text you wrote outside the markers stays. When the object comes back, the sections return and tag and banner are removed.
- Stale handling never renames or deletes an article. The title of an article (the object's name) and its document marker (which carries the object's ID) therefore stay in YouTrack after the object disappears or is denied; only the generated sections are removed. A name that was published before a deny rule hid it counts as published: rename or delete such an article in YouTrack yourself.
- Only articles of sources that took part in the run can become stale. If you switch a collector off, its articles are left alone.
- A source whose collection was incomplete is treated like a switched-off one for that run: its articles are not marked as stale, and a warning says so. For `kubernetes` this means a skipped file or document, a Namespace without a name, or a configured path without any object. Objects that a deny rule removes are the exception: their articles are always cleaned and marked as stale, so denied content does not stay published, and these cleanups do not count against `youtrack.max_stale_ratio`. Articles of an incomplete source that this run did not collect (for example because they sit in a skipped file) are checked against the deny rules by their document ID (source, kind and, for Kubernetes, cluster and namespace); articles of a collector that is switched off are never checked. The members of a Kubernetes namespace follow the verdict of their namespace, and a cluster that a deny rule removes takes its namespaces along, so the article of an uncollected namespace of a denied cluster, or of a denied namespace in any cluster, is cleaned. The check does not know about hosts that a denied host drags along (their hostnames and addresses in other articles), so an article that is only removed that way is not cleaned. For the same reason an incomplete `kubernetes` run does not clean a cluster article it did not regenerate when that article still lists a namespace that is now removed only because a `redact.deny_patterns` pattern added since the last publication matches its name, or only because it is named after a denied host or address; the next complete run regenerates the cluster article without that name. If that cannot be decided (a rule uses `tags`, `roles`, `tenants` or `labels`, or the article is an interface, IP address, network or site that only an allowed host's rule could keep, or it is a cluster article while a deny rule uses `namespaces` or `labels` or matches the `namespace` kind, or while allow rules exist and none of them admits every namespace (`kinds: [namespace]` without further conditions), because a cluster article names its namespaces), or a denied article has a generated block without end marker that cannot be emptied safely, `publish` stops with an error before it writes anything; `diff` only warns. `generate` stays tolerant and only prints the warnings.
- **Articles with a Kubernetes ID without a cluster** (for example `kubernetes:namespace:<namespace>` from a pre-release build): such an ID names no cluster and no namespace, so a deny rule that uses `namespaces` or `labels` or matches the `cluster` kind cannot decide it. When a run reads the `kubernetes` source incompletely and such a rule is configured, such an article that was not produced by the run makes `publish` stop with the error above, which names the article. A complete run marks those articles as stale and removes their generated sections; an article that is already stale and clean no longer blocks.
- A run that would mark more than `youtrack.max_stale_ratio` of the live articles as stale (default `0.5`) stops before the first write, with the counts in the error. This is what an empty or broken collection looks like. Articles that already carry the stale tag and banner do not count, so the limit does not loosen as stale articles pile up. When no live article is left, any newly stale article stops the run. Run again with `--allow-mass-stale` if the objects really are gone.
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
| 🔌 | [Write your own collector](docs/write-your-own-collector.md) | The `Collector` interface, settings and factory, the model, testing, wiring |
| 🧩 | [Overlay pattern](docs/overlay-pattern.md) | Build a binary with private collectors from the public `model`, `collector` and `docgen` packages; see [`examples/overlay`](examples/overlay) |

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

`internal/sink/youtrack/integration_test.go` publishes a small throwaway tree to a real YouTrack instance and checks create, idempotent re-run, update, stale marking and restore. It is skipped unless all of these variables are set, so default CI never runs it:

| Variable | Meaning |
|---|---|
| `YOUTRACK_IT_URL` | Base URL of the YouTrack instance |
| `YOUTRACK_IT_TOKEN` | Permanent token with write access to the sandbox project |
| `YOUTRACK_IT_PROJECT_ID` | Database ID of the sandbox project (for example `0-1`) |

```sh
go test ./internal/sink/youtrack -run TestIntegration -v
```

> [!WARNING]
> The sink marks every article of the project that it did not publish as stale. Use a dedicated sandbox project: the test aborts before writing if the project holds any article whose title does not start with `docgen-it-`.

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
