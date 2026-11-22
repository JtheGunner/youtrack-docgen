package model

import (
	"reflect"
	"testing"
)

func TestLink(t *testing.T) {
	aliased := host("terraform", "web", "web-tf")
	aliased.Aliases = []string{"web01.example.com"}

	tests := []struct {
		name      string
		entities  []Entity
		want      []Relation
		wantAdded int
	}{
		{
			name: "same address across sources",
			entities: []Entity{
				host("netbox", "vm1", "k8s-node-a.example.com", "192.0.2.20"),
				host("k8s", "node-a", "node-a", "192.0.2.20"),
			},
			want:      []Relation{{From: "k8s:host:node-a", To: "netbox:host:vm1", Kind: RelSameAs}},
			wantAdded: 1,
		},
		{
			name: "same hostname across sources, case-insensitive",
			entities: []Entity{
				host("netbox", "1", "Web01.example.com"),
				host("k8s", "web01", "web01.example.com"),
			},
			want:      []Relation{{From: "k8s:host:web01", To: "netbox:host:1", Kind: RelSameAs}},
			wantAdded: 1,
		},
		{
			name: "alias matches a name of another source",
			entities: []Entity{
				host("netbox", "1", "web01.example.com"),
				aliased,
			},
			want:      []Relation{{From: "netbox:host:1", To: "terraform:host:web", Kind: RelSameAs}},
			wantAdded: 1,
		},
		{
			name: "three sources describing one host",
			entities: []Entity{
				host("netbox", "1", "web01.example.com", "192.0.2.30"),
				host("k8s", "n1", "web01.example.com", "192.0.2.30"),
				host("terraform", "t1", "web01.example.com"),
			},
			want: []Relation{
				{From: "k8s:host:n1", To: "netbox:host:1", Kind: RelSameAs},
				{From: "k8s:host:n1", To: "terraform:host:t1", Kind: RelSameAs},
				{From: "netbox:host:1", To: "terraform:host:t1", Kind: RelSameAs},
			},
			wantAdded: 3,
		},
		{
			name: "same source is never linked",
			entities: []Entity{
				host("netbox", "1", "web01.example.com", "192.0.2.40"),
				host("netbox", "2", "web02.example.com", "192.0.2.40"),
			},
			want: []Relation{},
		},
		{
			name: "different addresses and names are not linked",
			entities: []Entity{
				host("netbox", "1", "web01.example.com", "192.0.2.50"),
				host("k8s", "n1", "node-b", "192.0.2.51"),
			},
			want: []Relation{},
		},
		{
			name: "short name links to the single matching fully qualified name",
			entities: []Entity{
				host("netbox", "1", "web01.example.com"),
				host("k8s", "n1", "web01"),
			},
			want:      []Relation{{From: "k8s:host:n1", To: "netbox:host:1", Kind: RelSameAs}},
			wantAdded: 1,
		},
		{
			name: "short name matching two fully qualified names links to neither",
			entities: []Entity{
				host("netbox", "1", "web01.example.com"),
				host("netbox", "2", "web01.example.org"),
				host("k8s", "n1", "web01"),
			},
			want: []Relation{},
		},
		{
			name: "two fully qualified names with the same first label are not linked",
			entities: []Entity{
				host("netbox", "1", "web01.example.com"),
				host("k8s", "n1", "web01.example.org"),
			},
			want: []Relation{},
		},
		{
			name: "two candidates in one source for the same address link to neither",
			entities: []Entity{
				host("netbox", "1", "a.example.com", "192.0.2.80"),
				host("netbox", "2", "b.example.com", "192.0.2.80"),
				host("k8s", "n1", "node", "192.0.2.80"),
			},
			want: []Relation{},
		},
		{
			name: "a host that is ambiguous from the other side is not linked either",
			entities: []Entity{
				host("netbox", "1", "web01.example.com"),
				host("k8s", "n1", "web01"),
				host("k8s", "n2", "web01.example.com"),
			},
			want: []Relation{},
		},
		{
			name: "only hosts are linked",
			entities: []Entity{
				{ID: "netbox:service:1", Kind: KindService, Source: "netbox", Name: "web", Addresses: host("x", "x", "x", "192.0.2.60").Addresses},
				{ID: "k8s:service:1", Kind: KindService, Source: "k8s", Name: "web", Addresses: host("x", "x", "x", "192.0.2.60").Addresses},
			},
			want: []Relation{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGraph()
			mustAdd(t, g, tt.entities...)

			if got := Link(g); got != tt.wantAdded {
				t.Errorf("Link() added %d relations, want %d", got, tt.wantAdded)
			}
			if got := g.Relations(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Relations() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLinkIsIdempotent(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g,
		host("netbox", "1", "web01.example.com", "192.0.2.70"),
		host("k8s", "n1", "node", "192.0.2.70"),
	)
	if got := Link(g); got != 1 {
		t.Fatalf("first Link() added %d, want 1", got)
	}
	if got := Link(g); got != 0 {
		t.Errorf("second Link() added %d, want 0", got)
	}
	if got := len(g.Relations()); got != 1 {
		t.Errorf("Relations() has %d entries, want 1", got)
	}
}
