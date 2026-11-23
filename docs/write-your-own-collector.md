# Write your own collector

A collector reads one data source and adds what it finds to the neutral model. Everything after that (linking, filtering, redaction, rendering, publishing) is shared, so a new source only has to produce entities and relations.

> [!NOTE]
> `model`, `collector` and `docgen` are public packages, so a collector can live in this repository or in a separate module. See [Overlay pattern](overlay-pattern.md) for the second case.

## The interface

```go
// collector/collector.go
type Collector interface {
	// Name is the source name used as the first component of entity IDs.
	Name() string
	// Collect reads the source and adds its entities and relations to g.
	Collect(ctx context.Context, g *model.Graph) error
}
```

Rules every collector follows:

- **Own your IDs.** Every entity ID starts with `Name()`. IDs have the form `source:kind:key`, are built with `model.NewID`, and must be stable between runs. Neither part may contain `:`.
- **Never import another collector or a sink.** Collectors only know the model.
- **Scope keys by cluster if you emit clusters.** A collector that models Kubernetes-like objects emits one `cluster` entity per cluster and puts the cluster name first in the key of everything below it, because names repeat across clusters. The built-in `kubernetes` collector uses `kubernetes:cluster:<cluster>`, `kubernetes:namespace:<cluster>/<namespace>` and `kubernetes:workload:<cluster>/<namespace>/<kind>/<name>`. Keys may contain `/` but not `:`. The renderer builds the `Kubernetes / <cluster> / <namespace>` articles only for entities of the source `kubernetes`; cluster entities of other sources are not rendered. The filter's cluster semantics also apply to the `kubernetes` source only: there a cluster is kept while one of its namespaces is kept, and a denied cluster takes its namespaces and their members along. A cluster of another source is filtered like any other entity.
- **Do not link across sources.** After all collectors ran, `model.Link` connects hosts of different sources that share an IP address or hostname with a `same_as` relation.
- **Add nothing on failure.** Collect into local values first and add them to the graph at the end, so a partial view of the source never reaches the output. The NetBox collector works this way.
- **Honour `ctx`** for every network call, and set a timeout on HTTP clients.
- **Read only.** A collector never modifies its source.

## The model

| Type | Purpose |
|---|---|
| `model.Entity` | One documented object: `ID`, `Kind`, `Name`, `Source`, `Labels`, `Attrs`, `Aliases`, `Addresses` |
| `model.Kind` | `site`, `network`, `ipaddress`, `host`, `interface`, `cluster`, `namespace`, `workload`, `service`, `ingress` |
| `model.Relation` | A directed edge: `From`, `To`, `Kind` |
| `model.RelationKind` | `runs_on`, `member_of`, `exposes`, `depends_on`, `routes_to`, `same_as` |

- `Graph.AddEntity` rejects an ID whose source or kind does not match the entity, an empty name, and duplicates.
- `Graph.AddRelation` rejects unknown kinds, self-relations, and relations whose ends are not in the graph yet. Add entities first, then relations.
- **Labels** are what allow/deny rules match on: `role`, `tenant`, `namespace`, one `tag.<slug>` = `true` per tag, and any custom key. Use the constants `model.LabelTagPrefix` and `model.LabelTenant` from `model/labels.go`; the keys `role` and `namespace` are plain strings.
- **Attrs** are free-form strings shown in the article. Keys that look like secrets (`password`, `token`, `api-key`, ...) and the prefixes `env.`, `secret.` and `secretdata.` are redacted by the filter. Put sensitive data under those names so redaction applies.
- **Rendered attributes** have fixed keys. Use the constants in `model/attrs.go`; an attribute under another key is carried along but never shown in a table, a diagram or a link.

  | Constant | Key | Entity | Shown as |
  |---|---|---|---|
  | `model.AttrFQDN` | `fqdn` | host | FQDN line in the host summary |
  | `model.AttrDescription` | `description` | host | Description line in the host summary |
  | `model.AttrURL` | `url` | host | Link to the source record in the Links section; must be an absolute `http(s)` URL |
  | `model.AttrPrefix` | `prefix` | network | Prefix column; hosts are matched to the network by it in the tables and the network diagram |
  | `model.AttrVLAN` | `vlan` | network | VLAN column; the numeric VLAN ID |
  | `model.AttrGateway` | `gateway` | network | Gateway column of the VLAN overview |
  | `model.AttrDNSName` | `dns_name` | ipaddress | Not shown; deny rules for hosts match on it |
  | `model.AttrExternalIPs` | `external_ips` | service | Not shown; links a service to the hosts that expose it |

  A network without a prefix that other networks are a `member_of` (a VLAN) is shown through those networks: each takes the VLAN's name unless it has a name of its own.
- **Aliases and Addresses** are repeated as plain text in the article, so YouTrack search finds them.

## Settings and factory

The command line builds a collector through a `collector.Factory` registered under the name of its block below `collectors:` in `docgen.yaml`. The factory receives the block as `collector.Settings`:

```go
// collector/settings.go
type Factory func(Settings) (Collector, error)

type Settings struct {
	URL     string         // the "url" key
	Token   Secret         // the "token" key, usually expanded from ${ENV}
	Options map[string]any // keys other than url, token, enabled, allow and deny, as decoded from YAML
}
```

- `Token` is a `collector.Secret`. Printing, logging or marshalling it shows `[REDACTED]`; call `Token.Value()` only to build an outgoing request.
- `Options` values are **not** masked. Put credentials in `token`, never in an option.
- The collector the factory returns must report the registered name from `Name()`.
- A collector may also implement `collector.CompletenessReporter`; see `collector/collector.go`.

## A minimal collector

This collector reads a JSON file with a list of hosts. It is a sketch to show the shape; the file format is made up. The runnable version is [`examples/overlay`](../examples/overlay).

```go
package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"

	"github.com/JtheGunner/youtrack-docgen/collector"
	"github.com/JtheGunner/youtrack-docgen/model"
)

// Source is the source name used in every entity ID of this collector.
const Source = "inventory"

type Collector struct{ path string }

// New is the collector.Factory: it reads the "path" option of the block.
func New(s collector.Settings) (collector.Collector, error) {
	path, ok := s.Options["path"].(string)
	if !ok || path == "" {
		return nil, errors.New(`inventory: option "path" must be a non-empty string`)
	}
	return &Collector{path: path}, nil
}

func (*Collector) Name() string { return Source }

type record struct {
	Hostname string   `json:"hostname"`
	Address  string   `json:"address"`
	Role     string   `json:"role"`
	Aliases  []string `json:"aliases"`
}

func (c *Collector) Collect(ctx context.Context, g *model.Graph) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return fmt.Errorf("inventory: %w", err)
	}
	var records []record
	if err := json.Unmarshal(raw, &records); err != nil {
		return fmt.Errorf("inventory: parse %s: %w", c.path, err)
	}

	hosts := make([]model.Entity, 0, len(records))
	for _, r := range records {
		id, err := model.NewID(Source, model.KindHost, r.Hostname)
		if err != nil {
			return fmt.Errorf("inventory: %w", err)
		}
		addr, err := netip.ParseAddr(r.Address)
		if err != nil {
			return fmt.Errorf("inventory: host %s: %w", r.Hostname, err)
		}
		hosts = append(hosts, model.Entity{
			ID:        id,
			Kind:      model.KindHost,
			Name:      r.Hostname,
			Source:    Source,
			Labels:    map[string]string{"role": r.Role},
			Aliases:   r.Aliases,
			Addresses: []netip.Addr{addr},
		})
	}

	// Add to the graph only after everything was read and validated.
	for _, h := range hosts {
		if err := g.AddEntity(h); err != nil {
			return fmt.Errorf("inventory: %w", err)
		}
	}
	return nil
}
```

## Test it

Follow the conventions of the existing collectors (see `internal/collector/netbox`):

- **Synthetic fixtures only.** Use `example.com` names and RFC 5737 or RFC 1918 addresses, stored under `testdata/`.
- **Table-driven tests** for the mapping from source records to entities. For an HTTP source, serve the fixtures from `httptest.NewServer` and pass in the HTTP client.
- **Failure paths:** an unreachable source, malformed data, and a duplicate entity must return an error and leave the graph unchanged.
- **Redaction:** seed a fake secret in the fixture (for example a `password` attribute or a bearer token in a description) and assert it does not appear in the rendered output. Any collector change needs this test.

```text
go test -race ./...
```

## Wire it in

The block under `collectors:` in `docgen.yaml` selects the collector by name. Its keys other than `url`, `token`, `enabled`, `allow` and `deny` arrive in `Settings.Options`:

```yaml
collectors:
  inventory:
    path: ./inventory.json   # read from Settings.Options["path"]
```

There are two routes to register the factory.

**In an overlay (a separate module).** Register it on an app and run that app from your own `main`:

```go
app := docgen.New()
if err := app.Register(inventory.Source, inventory.New); err != nil {
	log.Fatal(err)
}
app.Main()
```

`Register` rejects an invalid name, a nil factory, a duplicate, and the names of the built-in collectors. See [Overlay pattern](overlay-pattern.md).

**Inside this repository.** Built-in collectors are registered through the same mechanism. Add an entry to `BuiltinCollectors` in `internal/cli/collectors.go`, keyed by the collector's `Source` constant. Built-in collectors also get strict settings checks in `validate-config`; see `internal/config`.

For both routes, `validate-config` accepts any collector name, and `generate`, `diff` and `publish` stop with the list of registered names when a block names a collector that is not registered. Set `enabled: false` to skip a block.

NetBox is the reference collector: [`internal/collector/netbox`](../internal/collector/netbox).
