package config

import (
	"os"
	"regexp"
	"testing"
)

var yamlFence = regexp.MustCompile("(?s)```yaml\n(.*?)```")

// TestReadmeExampleConfigLoads keeps the documented configuration valid: every
// yaml block of the README must parse, and every block with a kubernetes
// collector must carry its paths in the map form with a cluster. At least one
// block must have a kubernetes collector.
func TestReadmeExampleConfigLoads(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	blocks := yamlFence.FindAllSubmatch(readme, -1)
	if len(blocks) == 0 {
		t.Fatal("README has no yaml example")
	}
	env := envMap(map[string]string{"YOUTRACK_TOKEN": "fake-youtrack-token", "NETBOX_TOKEN": "fake-netbox-token"})
	withKubernetes := 0
	for _, block := range blocks {
		cfg, err := Parse(block[1], env)
		if err != nil {
			t.Fatalf("README example does not load: %v\n%s", err, block[1])
		}
		kubernetes, ok := cfg.Collectors["kubernetes"]
		if !ok {
			continue
		}
		withKubernetes++
		sources, err := ParsePathSources(kubernetes.Settings["paths"])
		if err != nil || len(sources) == 0 {
			t.Fatalf("README example kubernetes paths: %v (%d entries)", err, len(sources))
		}
	}
	if withKubernetes == 0 {
		t.Fatal("no README yaml example has a kubernetes collector")
	}
}
