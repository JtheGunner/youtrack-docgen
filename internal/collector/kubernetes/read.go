package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
)

// header is decoded first for every document: just enough to decide whether
// the document is a supported Kubernetes object. The spec is not touched, so a
// CRD or a patch with a different spec shape cannot make decoding fail.
type header struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Labels    map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	// Items is set for kind "List".
	Items []yaml.Node `yaml:"items"`
}

// object is the subset of a supported Kubernetes manifest the collector
// understands. Secret data, environment values, annotations and container
// arguments are deliberately not decoded.
type object struct {
	header `yaml:",inline"`
	Spec   spec   `yaml:"spec"`
	Status status `yaml:"status"`
}

// replicaCount accepts the integer 3 as well as the string "3" that templating
// tools tend to produce. Any other value decodes to the empty string.
type replicaCount string

func (r *replicaCount) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return nil
	}
	if _, err := strconv.Atoi(n.Value); err == nil {
		*r = replicaCount(n.Value)
	}
	return nil
}

// status is the part of a Node status that names the machine's addresses.
type status struct {
	Addresses []struct {
		Type    string `yaml:"type"`
		Address string `yaml:"address"`
	} `yaml:"addresses"`
	// LoadBalancer is set on a Service by the cloud or the load balancer
	// controller; manifests exported from a cluster carry it.
	LoadBalancer struct {
		Ingress []struct {
			IP string `yaml:"ip"`
		} `yaml:"ingress"`
	} `yaml:"loadBalancer"`
}

type spec struct {
	Replicas replicaCount `yaml:"replicas"`
	Template podTemplate  `yaml:"template"`
	// CronJob nests the pod template one level deeper.
	JobTemplate struct {
		Spec struct {
			Template podTemplate `yaml:"template"`
		} `yaml:"spec"`
	} `yaml:"jobTemplate"`
	Schedule string `yaml:"schedule"`

	// Service fields.
	Type        string        `yaml:"type"`
	ClusterIP   string        `yaml:"clusterIP"`
	ClusterIPs  []string      `yaml:"clusterIPs"`
	ExternalIPs []string      `yaml:"externalIPs"`
	Ports       []servicePort `yaml:"ports"`

	// Ingress fields.
	IngressClassName string `yaml:"ingressClassName"`
	DefaultBackend   *struct {
		Service *backendService `yaml:"service"`
	} `yaml:"defaultBackend"`
	Rules []ingressRule `yaml:"rules"`
	TLS   []struct {
		Hosts []string `yaml:"hosts"`
	} `yaml:"tls"`
}

type podTemplate struct {
	Metadata struct {
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		NodeName       string            `yaml:"nodeName"`
		NodeSelector   map[string]string `yaml:"nodeSelector"`
		Containers     []container       `yaml:"containers"`
		InitContainers []container       `yaml:"initContainers"`
	} `yaml:"spec"`
}

type container struct {
	Image string `yaml:"image"`
}

type servicePort struct {
	Port       int    `yaml:"port"`
	Protocol   string `yaml:"protocol"`
	TargetPort any    `yaml:"targetPort"`
}

type ingressRule struct {
	Host string `yaml:"host"`
	HTTP *struct {
		Paths []struct {
			Backend struct {
				Service *backendService `yaml:"service"`
			} `yaml:"backend"`
		} `yaml:"paths"`
	} `yaml:"http"`
}

type backendService struct {
	Name string `yaml:"name"`
}

// serviceSelector decodes the flat spec.selector map of a Service, which has a
// different shape than the selector of a workload.
type serviceSelector struct {
	Spec struct {
		Selector map[string]string `yaml:"selector"`
	} `yaml:"spec"`
}

// parsed couples an object with the decoded service selector and remembers
// where it came from, for warnings.
type parsed struct {
	object
	serviceSelector map[string]string
	origin          string
	// repoURL is the repository of the configured path the object was read from.
	repoURL string
	// cluster is the cluster of the configured path the object was read from.
	cluster string
}

// supportedKind reports whether the spec of kind is decoded. Every other kind
// is ignored without looking at its spec.
func supportedKind(kind string) bool {
	switch kind {
	case "Namespace", "Node", "Service", "Ingress":
		return true
	}
	return workloadKinds[kind]
}

// reporter receives the warnings of a read. A skip that can hide an object
// that exists in the sources is also recorded as a reason the read is
// incomplete; a skipped duplicate is not, because the object is still read.
type reporter struct {
	warn       func(string)
	incomplete []string
}

// skipped warns about msg and records it as a reason the read is incomplete.
func (r *reporter) skipped(msg string) {
	r.warn(msg)
	r.incomplete = append(r.incomplete, msg)
}

// readObjects reads every path. Files and documents that cannot be decoded and
// duplicate objects are skipped with a warning; only I/O failures abort. Every
// skip that may hide an object is also recorded in r.incomplete. When
// the same object (cluster, kind, namespace, name; nodes by name alone)
// appears more than once, the first one in path order and lexical file order
// wins. It also returns, in path order, the clusters at least one of whose
// paths held an object.
func readObjects(ctx context.Context, paths []config.PathSource, r *reporter) ([]parsed, []string, error) {
	var out []parsed
	var clusters []string
	seen := map[string]string{}
	for _, src := range paths {
		root := src.Path
		files, err := manifestFiles(root)
		if err != nil {
			return nil, nil, err
		}
		found := 0
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			objs, err := readFile(f, r)
			if err != nil {
				return nil, nil, err
			}
			found += len(objs)
			for _, o := range objs {
				if o.Kind == "Namespace" && o.Metadata.Name == "" {
					r.skipped(fmt.Sprintf("%s: skipping Namespace without metadata.name", o.origin))
					continue
				}
				key := identity(o)
				scoped, label := key, key
				if o.Kind != "Node" {
					scoped = src.Cluster + " " + key
					label = key + " in cluster " + src.Cluster
				}
				if first, dup := seen[scoped]; dup {
					r.warn(fmt.Sprintf("%s: duplicate %s ignored, first defined in %s", o.origin, label, first))
					continue
				}
				o.repoURL = src.RepoURL
				o.cluster = src.Cluster
				seen[scoped] = o.origin
				out = append(out, o)
			}
		}
		if found == 0 {
			r.skipped(fmt.Sprintf("%s: no Kubernetes objects found", root))
		} else if !slices.Contains(clusters, src.Cluster) {
			clusters = append(clusters, src.Cluster)
		}
	}
	return out, clusters, nil
}

// identity is the duplicate key of an object within one cluster. Namespaces
// and nodes are cluster-scoped, everything else lives in a namespace. Nodes are
// machines and keep one identity across clusters.
func identity(o parsed) string {
	if o.Kind == "Namespace" || o.Kind == "Node" {
		return o.Kind + " " + o.Metadata.Name
	}
	ns := o.Metadata.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	return o.Kind + " " + ns + "/" + o.Metadata.Name
}

// manifestFiles returns the YAML files under root in lexical order, skipping
// hidden directories such as .git. A root that is a symlink is resolved first;
// symlinked directories below the root are not followed.
func manifestFiles(root string) ([]string, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: %w", err)
	}
	if !info.IsDir() {
		return []string{resolved}, nil
	}
	var files []string
	err = filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != resolved && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(path)); ext == ".yaml" || ext == ".yml" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("kubernetes: %w", err)
	}
	return files, nil
}

// readFile decodes all documents of one file. A file with a YAML syntax error
// (a Helm template, for example) is skipped as a whole, because the decoder
// cannot resynchronise after it; a document with an unexpected shape (a
// JSON6902 patch, a wrong field type) is skipped on its own.
func readFile(path string, r *reporter) ([]parsed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var out []parsed
	for i := 1; ; i++ {
		var node yaml.Node
		err := dec.Decode(&node)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			r.skipped(fmt.Sprintf("%s: skipping file, not valid YAML (document %d): %v", path, i, err))
			return nil, nil
		}
		origin := fmt.Sprintf("%s (document %d)", path, i)
		objs, err := decodeNode(&node, origin)
		if err != nil {
			r.skipped(fmt.Sprintf("%s: skipping document: %v", origin, err))
			continue
		}
		out = append(out, objs...)
	}
}

// decodeNode decodes one document and expands kind "List". Documents without
// apiVersion and kind, and kinds the collector does not map, are skipped.
func decodeNode(node *yaml.Node, origin string) ([]parsed, error) {
	var h header
	if err := node.Decode(&h); err != nil {
		return nil, err
	}
	if h.APIVersion == "" || h.Kind == "" {
		return nil, nil
	}
	if h.Kind == "List" {
		var out []parsed
		for i := range h.Items {
			objs, err := decodeNode(&h.Items[i], fmt.Sprintf("%s item %d", origin, i+1))
			if err != nil {
				return nil, fmt.Errorf("list item %d: %w", i+1, err)
			}
			out = append(out, objs...)
		}
		return out, nil
	}
	if !supportedKind(h.Kind) {
		return nil, nil
	}
	p := parsed{origin: origin}
	if err := node.Decode(&p.object); err != nil {
		return nil, err
	}
	if h.Kind == "Service" {
		var s serviceSelector
		if err := node.Decode(&s); err != nil {
			return nil, err
		}
		p.serviceSelector = s.Spec.Selector
	}
	return []parsed{p}, nil
}
