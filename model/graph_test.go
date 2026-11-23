package model

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"
)

func host(source, key, name string, addrs ...string) Entity {
	e := Entity{
		ID:     ID(source + ":host:" + key),
		Kind:   KindHost,
		Source: source,
		Name:   name,
	}
	for _, a := range addrs {
		e.Addresses = append(e.Addresses, netip.MustParseAddr(a))
	}
	return e
}

func mustAdd(t *testing.T, g *Graph, entities ...Entity) {
	t.Helper()
	for _, e := range entities {
		if err := g.AddEntity(e); err != nil {
			t.Fatalf("AddEntity(%s): %v", e.ID, err)
		}
	}
}

func ids(entities []Entity) []ID {
	out := make([]ID, 0, len(entities))
	for _, e := range entities {
		out = append(out, e.ID)
	}
	return out
}

func TestGraphAddEntity(t *testing.T) {
	tests := []struct {
		name    string
		entity  Entity
		wantErr error
	}{
		{name: "valid", entity: host("netbox", "1", "web01.example.com")},
		{name: "malformed id", entity: Entity{ID: "nope", Kind: KindHost, Source: "netbox", Name: "x"}, wantErr: ErrInvalidID},
		{name: "kind does not match id", entity: Entity{ID: "netbox:site:1", Kind: KindHost, Source: "netbox", Name: "x"}, wantErr: ErrInvalidEntity},
		{name: "source does not match id", entity: Entity{ID: "netbox:host:1", Kind: KindHost, Source: "k8s", Name: "x"}, wantErr: ErrInvalidEntity},
		{name: "empty name", entity: Entity{ID: "netbox:host:1", Kind: KindHost, Source: "netbox"}, wantErr: ErrInvalidEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewGraph().AddEntity(tt.entity)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AddEntity() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestGraphAddEntityDuplicate(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g, host("netbox", "1", "web01.example.com"))
	err := g.AddEntity(host("netbox", "1", "other.example.com"))
	if !errors.Is(err, ErrDuplicateEntity) {
		t.Fatalf("AddEntity() error = %v, want ErrDuplicateEntity", err)
	}
}

func TestGraphEntityIsACopy(t *testing.T) {
	g := NewGraph()
	in := host("netbox", "1", "web01.example.com", "192.0.2.10")
	in.Labels = map[string]string{"role": "web"}
	mustAdd(t, g, in)

	in.Labels["role"] = "changed"
	in.Addresses[0] = netip.MustParseAddr("192.0.2.99")

	got, ok := g.Entity("netbox:host:1")
	if !ok {
		t.Fatal("Entity() not found")
	}
	if got.Labels["role"] != "web" || got.Addresses[0] != netip.MustParseAddr("192.0.2.10") {
		t.Errorf("stored entity changed through caller input: %+v", got)
	}

	got.Labels["role"] = "mutated"
	again, _ := g.Entity("netbox:host:1")
	if again.Labels["role"] != "web" {
		t.Errorf("stored entity changed through returned value: %+v", again)
	}
}

func TestGraphEntityMissing(t *testing.T) {
	if _, ok := NewGraph().Entity("netbox:host:1"); ok {
		t.Error("Entity() found a missing entity")
	}
}

func TestGraphByKindIsSorted(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g,
		host("netbox", "2", "b.example.com"),
		host("netbox", "1", "a.example.com"),
		Entity{ID: "netbox:site:1", Kind: KindSite, Source: "netbox", Name: "dc1"},
	)
	want := []ID{"netbox:host:1", "netbox:host:2"}
	if got := ids(g.ByKind(KindHost)); !reflect.DeepEqual(got, want) {
		t.Errorf("ByKind(host) = %v, want %v", got, want)
	}
	if got := g.ByKind(KindCluster); len(got) != 0 {
		t.Errorf("ByKind(cluster) = %v, want empty", got)
	}
}

func TestGraphAddRelation(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g,
		host("netbox", "1", "vm01.example.com"),
		host("netbox", "2", "hv01.example.com"),
	)
	tests := []struct {
		name    string
		rel     Relation
		wantErr error
	}{
		{name: "valid", rel: Relation{From: "netbox:host:1", To: "netbox:host:2", Kind: RelRunsOn}},
		{name: "duplicate is ignored", rel: Relation{From: "netbox:host:1", To: "netbox:host:2", Kind: RelRunsOn}},
		{name: "unknown from", rel: Relation{From: "netbox:host:9", To: "netbox:host:2", Kind: RelRunsOn}, wantErr: ErrUnknownEntity},
		{name: "unknown to", rel: Relation{From: "netbox:host:1", To: "netbox:host:9", Kind: RelRunsOn}, wantErr: ErrUnknownEntity},
		{name: "unknown kind", rel: Relation{From: "netbox:host:1", To: "netbox:host:2", Kind: "likes"}, wantErr: ErrInvalidRelation},
		{name: "self relation", rel: Relation{From: "netbox:host:1", To: "netbox:host:1", Kind: RelDependsOn}, wantErr: ErrInvalidRelation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := g.AddRelation(tt.rel); !errors.Is(err, tt.wantErr) {
				t.Fatalf("AddRelation() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
	if got := len(g.Relations()); got != 1 {
		t.Errorf("Relations() has %d entries, want 1", got)
	}
}

func TestGraphRelationsFromTo(t *testing.T) {
	g := NewGraph()
	mustAdd(t, g,
		host("netbox", "1", "vm01.example.com"),
		host("netbox", "2", "hv01.example.com"),
		host("netbox", "3", "db01.example.com"),
	)
	for _, r := range []Relation{
		{From: "netbox:host:1", To: "netbox:host:3", Kind: RelDependsOn},
		{From: "netbox:host:1", To: "netbox:host:2", Kind: RelRunsOn},
	} {
		if err := g.AddRelation(r); err != nil {
			t.Fatal(err)
		}
	}

	wantFrom := []Relation{
		{From: "netbox:host:1", To: "netbox:host:2", Kind: RelRunsOn},
		{From: "netbox:host:1", To: "netbox:host:3", Kind: RelDependsOn},
	}
	if got := g.RelationsFrom("netbox:host:1"); !reflect.DeepEqual(got, wantFrom) {
		t.Errorf("RelationsFrom() = %v, want %v", got, wantFrom)
	}
	wantTo := []Relation{{From: "netbox:host:1", To: "netbox:host:3", Kind: RelDependsOn}}
	if got := g.RelationsTo("netbox:host:3"); !reflect.DeepEqual(got, wantTo) {
		t.Errorf("RelationsTo() = %v, want %v", got, wantTo)
	}
}
