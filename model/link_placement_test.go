package model

import (
	"reflect"
	"testing"
)

func clusterEntity(id ID, kind Kind, name string, attrs map[string]string, aliases ...string) Entity {
	return Entity{ID: id, Kind: kind, Name: name, Source: "k8s", Attrs: attrs, Aliases: aliases}
}

func TestLinkPlacement(t *testing.T) {
	sameAs := func(from, to ID) Relation { return Relation{From: from, To: to, Kind: RelSameAs} }
	runsOn := func(from, to ID) Relation { return Relation{From: from, To: to, Kind: RelRunsOn} }
	exposes := func(from, to ID) Relation { return Relation{From: from, To: to, Kind: RelExposes} }

	workload := clusterEntity("k8s:workload:api", KindWorkload, "api", nil)
	pin := runsOn("k8s:workload:api", "k8s:host:node-a")

	tests := []struct {
		name     string
		entities []Entity
		rels     []Relation
		want     []Relation
	}{
		{
			name: "workload runs on the NetBox twin of its node",
			entities: []Entity{
				workload,
				host("k8s", "node-a", "node-a", "192.0.2.21"),
				host("netbox", "vm1", "vm1.example.com", "192.0.2.21"),
			},
			rels: []Relation{pin},
			want: []Relation{
				sameAs("k8s:host:node-a", "netbox:host:vm1"),
				pin,
				runsOn("k8s:workload:api", "netbox:host:vm1"),
			},
		},
		{
			name: "unmatchable node adds nothing",
			entities: []Entity{
				workload,
				host("k8s", "node-a", "node-a", "198.51.100.9"),
				host("netbox", "vm1", "vm1.example.com", "192.0.2.21"),
			},
			rels: []Relation{pin},
			want: []Relation{pin},
		},
		{
			name: "ambiguous node matching two hosts does not link the workload",
			entities: []Entity{
				workload,
				host("k8s", "node-a", "node-a", "192.0.2.21"),
				host("netbox", "vm1", "vm1.example.com", "192.0.2.21"),
				host("netbox", "vm2", "vm2.example.com", "192.0.2.21"),
			},
			rels: []Relation{pin},
			want: []Relation{pin},
		},
		{
			name: "host exposes a service on its external IP, not on its cluster IP",
			entities: []Entity{
				func() Entity {
					s := clusterEntity("k8s:service:edge", KindService, "edge", map[string]string{AttrExternalIPs: "192.0.2.30, 192.0.2.99"})
					s.Addresses = host("x", "x", "x", "10.96.0.5").Addresses
					return s
				}(),
				host("netbox", "vm1", "vm1.example.com", "192.0.2.30"),
				host("netbox", "vm2", "vm2.example.com", "10.96.0.5"),
			},
			want: []Relation{exposes("netbox:host:vm1", "k8s:service:edge")},
		},
		{
			name: "ambiguous external IP does not expose",
			entities: []Entity{
				clusterEntity("k8s:service:edge", KindService, "edge", map[string]string{AttrExternalIPs: "192.0.2.30"}),
				host("netbox", "vm1", "vm1.example.com", "192.0.2.30"),
				host("netbox", "vm2", "vm2.example.com", "192.0.2.30"),
			},
			want: []Relation{},
		},
		{
			name: "host exposes an ingress by hostname",
			entities: []Entity{
				clusterEntity("k8s:ingress:web", KindIngress, "web", nil, "Lb.example.com.", "shop.example.com"),
				host("netbox", "lb", "lb.example.com"),
				host("netbox", "other", "other.example.com"),
			},
			want: []Relation{exposes("netbox:host:lb", "k8s:ingress:web")},
		},
		{
			name: "same-source host never exposes",
			entities: []Entity{
				clusterEntity("k8s:ingress:web", KindIngress, "web", nil, "node-a"),
				host("k8s", "node-a", "node-a"),
			},
			want: []Relation{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGraph()
			mustAdd(t, g, tt.entities...)
			for _, r := range tt.rels {
				if err := g.AddRelation(r); err != nil {
					t.Fatal(err)
				}
			}
			Link(g)
			if got := g.Relations(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Relations() = %v, want %v", got, tt.want)
			}
			before := len(g.Relations())
			if added := Link(g); added != 0 || len(g.Relations()) != before {
				t.Errorf("second Link() added %d, want 0", added)
			}
		})
	}
}
