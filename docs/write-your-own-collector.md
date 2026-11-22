# Write your own collector

A collector reads one data source and adds what it finds to the neutral model. Everything after that (linking, filtering, redaction, rendering, publishing) is shared, so a new source only has to produce entities and relations.

> [!IMPORTANT]
> The `collector` and `model` packages live under `internal/`. Go only lets code inside this module import them. See [Overlay pattern](overlay-pattern.md) for what that means for collectors that live outside this repository.

## The interface

```go
// internal/collector/collector.go
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
- **Labels** are what allow/deny rules match on: `role`, `tenant`, `namespace`, one `tag.<slug>` = `true` per tag, and any custom key. Use the constants in `internal/model/labels.go`.
- **Attrs** are free-form strings shown in the article. Keys that look like secrets (`password`, `token`, `api-key`, ...) and the prefixes `env.`, `secret.` and `secretdata.` are redacted by the filter. Put sensitive data under those names so redaction applies.
- **Rendered attributes** have fixed keys. Use the constants in `internal/model/attrs.go`; an attribute under another key is carried along but never shown in a table, a diagram or a link.

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

## A minimal collector

This collector reads a JSON file with a list of hosts. It is a sketch to show the shape; the file format is made up.

```go
package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// Source is the source name used in every entity ID of this collector.
const Source = "inventory"

type Collector struct{ path string }

func New(path string) *Collector { return &Collector{path: path} }

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
go test -race ./internal/collector/inventory/...
```

## Wire it in

Today the command line builds its collectors in `buildCollectors` in `internal/cli/generate.go`, with one `case` per collector name. A collector inside this repository adds its case there. Its settings come from the matching block under `collectors:` in `docgen.yaml`:

```yaml
collectors:
  inventory:
    path: ./inventory.json   # collector-specific setting, read from Module.Settings
```

`validate-config` accepts any collector name. `generate` stops with an error for a collector it has no `case` for. Set `enabled: false` to skip one.

Collectors for NetBox are the reference: [`internal/collector/netbox`](../internal/collector/netbox).
