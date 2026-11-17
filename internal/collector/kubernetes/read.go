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
	"strings"

	"gopkg.in/yaml.v3"
)

// object is the subset of a Kubernetes manifest the collector understands.
// Secret data, environment values, annotations and container arguments are
// deliberately not decoded.
type object struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	// Items is set for kind "List".
	Items []yaml.Node `yaml:"items"`
	Spec  spec        `yaml:"spec"`
}

type spec struct {
	Replicas *int        `yaml:"replicas"`
	Template podTemplate `yaml:"template"`
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
		Containers     []container `yaml:"containers"`
		InitContainers []container `yaml:"initContainers"`
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

// parsed couples an object with the decoded service selector.
type parsed struct {
	object
	serviceSelector map[string]string
}

func readObjects(ctx context.Context, paths []string) ([]parsed, error) {
	var out []parsed
	for _, root := range paths {
		files, err := manifestFiles(root)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			objs, err := readFile(f)
			if err != nil {
				return nil, err
			}
			out = append(out, objs...)
		}
	}
	return out, nil
}

// manifestFiles returns the YAML files under root in lexical order, skipping
// hidden directories such as .git.
func manifestFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: %w", err)
	}
	if !info.IsDir() {
		return []string{root}, nil
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
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

func readFile(path string) ([]parsed, error) {
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
			return nil, fmt.Errorf("kubernetes: %s: document %d: %w", path, i, err)
		}
		objs, err := decodeNode(&node)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: %s: document %d: %w", path, i, err)
		}
		out = append(out, objs...)
	}
}

// decodeNode decodes one document and expands kind "List". Documents without
// apiVersion and kind are not Kubernetes objects and are skipped.
func decodeNode(node *yaml.Node) ([]parsed, error) {
	var o object
	if err := node.Decode(&o); err != nil {
		return nil, err
	}
	if o.APIVersion == "" || o.Kind == "" {
		return nil, nil
	}
	if o.Kind == "List" {
		var out []parsed
		for i := range o.Items {
			objs, err := decodeNode(&o.Items[i])
			if err != nil {
				return nil, fmt.Errorf("list item %d: %w", i+1, err)
			}
			out = append(out, objs...)
		}
		return out, nil
	}
	p := parsed{object: o}
	if o.Kind == "Service" {
		var s serviceSelector
		if err := node.Decode(&s); err != nil {
			return nil, err
		}
		p.serviceSelector = s.Spec.Selector
	}
	return []parsed{p}, nil
}
