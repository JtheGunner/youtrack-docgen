package mapbuild

import (
	"errors"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

func TestAddSanitizesKeyAndRejectsDuplicates(t *testing.T) {
	b := New("netbox")
	e, err := b.Add(model.KindIPAddress, "2001:db8::1", "ip")
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "netbox:ipaddress:2001_db8__1" || e.Source != "netbox" {
		t.Errorf("entity = %+v", e)
	}
	if _, err := b.Add(model.KindIPAddress, "2001:db8::1", "ip"); !errors.Is(err, model.ErrDuplicateEntity) {
		t.Errorf("duplicate error = %v, want ErrDuplicateEntity", err)
	}
}

func TestCommitWritesEntitiesAndRelationsAndPrefixesErrors(t *testing.T) {
	b := New("k8s")
	a, _ := b.Add(model.KindHost, "a", "a")
	c, _ := b.Add(model.KindHost, "c", "c")
	b.Relate(a.ID, c.ID, model.RelDependsOn)

	g := model.NewGraph()
	if err := b.Commit(g); err != nil {
		t.Fatal(err)
	}
	if len(g.IDs()) != 2 || len(g.Relations()) != 1 {
		t.Errorf("graph has %d entities, %d relations", len(g.IDs()), len(g.Relations()))
	}
	err := b.Commit(g)
	if err == nil || err.Error()[:4] != "k8s:" {
		t.Errorf("second commit error = %v, want a k8s-prefixed duplicate error", err)
	}
}

func TestHelpers(t *testing.T) {
	if got := FirstNonEmpty("", "x", "y"); got != "x" {
		t.Errorf("FirstNonEmpty = %q", got)
	}
	e := &model.Entity{Attrs: map[string]string{}}
	SetAttr(e, "empty", "")
	SetAttr(e, "set", "v")
	if _, ok := e.Attrs["empty"]; ok || e.Attrs["set"] != "v" {
		t.Errorf("attrs = %v", e.Attrs)
	}
}
