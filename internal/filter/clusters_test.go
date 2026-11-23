package filter

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// kubeEntity builds an entity the way the Kubernetes collector writes it: the
// key is cluster-prefixed, the name is the last key segment, members carry the
// plain namespace as label and every cluster-scoped entity names its cluster.
func kubeEntity(t *testing.T, kind model.Kind, key string, attrs map[string]string) model.Entity {
	t.Helper()
	e := entity(t, "k8s", kind, key, nil, attrs)
	e.Name = key[strings.LastIndex(key, "/")+1:]
	if kind == model.KindCluster {
		return e
	}
	parts := strings.Split(key, "/")
	e.Labels = map[string]string{LabelNamespace: parts[1]}
	if e.Attrs == nil {
		e.Attrs = map[string]string{}
	}
	e.Attrs["cluster"] = parts[0]
	return e
}

func TestNamespaceFromKey(t *testing.T) {
	tests := []struct {
		kind      model.Kind
		key       string
		want      string
		wantFound bool
	}{
		{model.KindNamespace, "prod/shop", "shop", true},
		{model.KindWorkload, "prod/shop/deployment/web", "shop", true},
		{model.KindService, "prod/shop/web", "shop", true},
		{model.KindIngress, "prod/shop/web", "shop", true},
		{model.KindCluster, "prod", "", false},
		{model.KindHost, "node01", "", false},
		// Keys of the old format or with a wrong segment count name no namespace
		// for certain, so a namespace rule stays undecided instead of guessing.
		{model.KindNamespace, "shop", "", false},
		{model.KindWorkload, "shop/deployment/web", "", false},
		{model.KindService, "shop/web", "", false},
		{model.KindService, "prod/shop/extra/web", "", false},
		{model.KindIngress, "prod//web", "", false},
		{model.KindNamespace, "/shop", "", false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %s", tt.kind, tt.key), func(t *testing.T) {
			got, found := namespaceFromKey(tt.kind, tt.key)
			if got != tt.want || found != tt.wantFound {
				t.Errorf("namespaceFromKey() = %q, %v, want %q, %v", got, found, tt.want, tt.wantFound)
			}
		})
	}
}

// A namespace rule names the plain namespace, so it decides the members of
// that namespace in every cluster. The cluster entity follows kind rules only.
func TestDecideIDAcrossClusters(t *testing.T) {
	denyX := Rules{Deny: []Match{{Namespaces: []string{"x"}}}}
	denyNamespaceX := Rules{Deny: []Match{{Kinds: []string{"namespace"}, Namespaces: []string{"x"}}}}
	allowX := Rules{Allow: []Match{{Namespaces: []string{"x"}}}}
	tests := []struct {
		name  string
		rules Rules
		id    string
		want  Verdict
	}{
		{"workload in cluster a denied", denyX, "kubernetes:workload:a/x/deployment/web", Denied},
		{"workload in cluster b denied", denyX, "kubernetes:workload:b/x/deployment/web", Denied},
		{"service in cluster a denied", denyX, "kubernetes:service:a/x/web", Denied},
		{"service in cluster b denied", denyX, "kubernetes:service:b/x/web", Denied},
		{"ingress in cluster a denied", denyX, "kubernetes:ingress:a/x/web", Denied},
		{"ingress in cluster b denied", denyX, "kubernetes:ingress:b/x/web", Denied},
		{"namespace in cluster b denied", denyX, "kubernetes:namespace:b/x", Denied},
		{"member cascade in cluster a", denyNamespaceX, "kubernetes:service:a/x/web", Denied},
		{"member cascade in cluster b", denyNamespaceX, "kubernetes:workload:b/x/deployment/web", Denied},
		{"other namespace permitted", denyX, "kubernetes:service:a/y/web", Permitted},
		{"cluster named like the namespace is no namespace", denyX, "kubernetes:workload:x/y/deployment/web", Permitted},
		{"allow admits members in every cluster", allowX, "kubernetes:workload:b/x/deployment/web", Permitted},
		{"allow excludes other namespaces", allowX, "kubernetes:service:a/y/web", Denied},
		{"cluster kept when its namespaces are denied", denyX, "kubernetes:cluster:a", Permitted},
		{"cluster kept by a namespace-kind deny", denyNamespaceX, "kubernetes:cluster:a", Permitted},
		{"cluster denied by a kind rule", Rules{Deny: []Match{{Kinds: []string{"cluster"}}}}, "kubernetes:cluster:a", Denied},
		{"cluster admitted by a kind allow rule", Rules{Allow: []Match{{Kinds: []string{"cluster"}}}}, "kubernetes:cluster:a", Permitted},
		// A cluster is kept when one of its namespaces survives, which an ID
		// cannot tell: a failed allow match leaves it undecided.
		{"namespace allow rule leaves the cluster undecided", allowX, "kubernetes:cluster:x", Undecidable},
		{"cluster deny wins over its reference", Rules{Allow: []Match{{Namespaces: []string{"x"}}}, Deny: []Match{{Kinds: []string{"cluster"}}}}, "kubernetes:cluster:a", Denied},
		{"label rule leaves the cluster undecided", Rules{Deny: []Match{{Labels: map[string]string{"label.team": "a"}}}}, "kubernetes:cluster:a", Undecidable},
		{"old-format member key is undecided", denyX, "kubernetes:service:x/web", Undecidable},
		{"redacted key in a cluster is denied", Rules{}, "kubernetes:service:a/x/password=FAKE-KEY-SECRET-9", Denied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := New(Config{Collectors: map[string]Rules{"kubernetes": tt.rules}})
			if err != nil {
				t.Fatal(err)
			}
			if got := f.DecideID(model.ID(tt.id)); got != tt.want {
				t.Errorf("DecideID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

// A decided DecideID verdict of a cluster ID matches what Apply does with the
// collected cluster entity.
func TestApplyTreatsTheClusterLikeDecideID(t *testing.T) {
	for _, rules := range []Rules{
		{Deny: []Match{{Namespaces: []string{"x"}}}},
		{Allow: []Match{{Namespaces: []string{"x"}}}},
		{Allow: []Match{{Kinds: []string{"cluster"}}}},
		{Deny: []Match{{Kinds: []string{"cluster"}}}},
	} {
		g := model.NewGraph()
		cluster := mustAdd(t, g, entity(t, kubernetesSource, model.KindCluster, "a", nil, nil))
		f, err := New(Config{Collectors: map[string]Rules{kubernetesSource: rules}})
		if err != nil {
			t.Fatal(err)
		}
		out, err := f.Apply(g)
		if err != nil {
			t.Fatal(err)
		}
		_, kept := out.Entity(cluster.ID)
		if verdict := f.DecideID(cluster.ID); verdict != Undecidable && (verdict == Permitted) != kept {
			t.Errorf("rules %+v: Apply kept = %v, DecideID = %v", rules, kept, verdict)
		}
	}
}

// Members of a namespace that a rule on its name denies are removed in every
// cluster, also without a member_of relation (through their key). The DNS
// names of those members are scrubbed from what stays.
func TestApplyRemovesMembersOfDeniedNamespaceInEveryCluster(t *testing.T) {
	g := model.NewGraph()
	mustAdd(t, g, kubeEntity(t, model.KindCluster, "a", nil))
	mustAdd(t, g, kubeEntity(t, model.KindCluster, "b", nil))
	// Members carry no namespace label and no member_of relation here, so only
	// the key-based cascade can remove them.
	unlabeled := func(e model.Entity) model.Entity {
		e.Labels = nil
		return e
	}
	var denied []model.Entity
	for _, c := range []string{"a", "b"} {
		denied = append(denied,
			mustAdd(t, g, kubeEntity(t, model.KindNamespace, c+"/lab", nil)),
			mustAdd(t, g, unlabeled(kubeEntity(t, model.KindWorkload, c+"/lab/deployment/api", nil))),
			mustAdd(t, g, unlabeled(kubeEntity(t, model.KindService, c+"/lab/billing", nil))),
			mustAdd(t, g, unlabeled(kubeEntity(t, model.KindIngress, c+"/lab/edge", nil))),
		)
	}
	kept := mustAdd(t, g, kubeEntity(t, model.KindWorkload, "b/app/deployment/web", map[string]string{
		"notes": "calls billing.lab.svc and billing.lab.svc.cluster.local",
	}))

	cfg := Config{Collectors: map[string]Rules{"k8s": {Deny: []Match{{Kinds: []string{"namespace"}, Namespaces: []string{"lab"}}}}}}
	out := mustApply(t, cfg, g)
	for _, e := range denied {
		if _, ok := out.Entity(e.ID); ok {
			t.Errorf("%s must be removed", e.ID)
		}
	}
	for _, id := range []model.ID{"k8s:cluster:a", "k8s:cluster:b"} {
		if _, ok := out.Entity(id); !ok {
			t.Errorf("cluster %s must stay when only its namespaces are denied", id)
		}
	}
	web, ok := out.Entity(kept.ID)
	if !ok {
		t.Fatal("the workload of the other namespace must stay")
	}
	if strings.Contains(web.Attrs["notes"], "billing.lab") {
		t.Errorf("notes = %q still names a removed member", web.Attrs["notes"])
	}
}

// A namespace removed by a rule that only matches it in one cluster (here by
// its manifest label) takes its own members along, not the members of the
// namespace with the same name in another cluster.
func TestApplyCascadeStaysInsideTheCluster(t *testing.T) {
	g := model.NewGraph()
	nsA := kubeEntity(t, model.KindNamespace, "a/lab", nil)
	nsA.Labels["label.stage"] = "test"
	mustAdd(t, g, nsA)
	mustAdd(t, g, kubeEntity(t, model.KindNamespace, "b/lab", nil))
	memberA := mustAdd(t, g, kubeEntity(t, model.KindService, "a/lab/api", nil))
	memberB := mustAdd(t, g, kubeEntity(t, model.KindService, "b/lab/api", nil))

	cfg := Config{Collectors: map[string]Rules{"k8s": {Deny: []Match{{Kinds: []string{"namespace"}, Labels: map[string]string{"label.stage": "test"}}}}}}
	out := mustApply(t, cfg, g)
	if _, ok := out.Entity(memberA.ID); ok {
		t.Error("member of the denied namespace must be removed")
	}
	if _, ok := out.Entity(memberB.ID); !ok {
		t.Error("member of the namespace in the other cluster must stay")
	}
}
