package config

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
)

// PathSource is one entry of the kubernetes collector's "paths" setting: a
// file or directory of manifests, the cluster they describe and, optionally,
// the repository they come from.
type PathSource struct {
	Path string
	// Cluster names the cluster the objects read from Path belong to. It is
	// non-empty and consists of lowercase letters, digits, '.', '_' and '-',
	// starting and ending with a letter or digit.
	Cluster string
	// RepoURL is the http(s) URL of the repository. Empty means no repository
	// link is shown for the objects read from Path.
	RepoURL string
}

var (
	errPathSources = errors.New("must be a non-empty list of maps with \"path\", \"cluster\" and an optional \"repo_url\"")
	errPathEntry   = errors.New("must be a map with \"path\", \"cluster\" and an optional \"repo_url\"")
	errCluster     = errors.New("cluster must be a name of lowercase letters, digits, '.', '_' or '-' that starts and ends with a letter or digit")
	clusterName    = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)
)

// ParsePathSources reads the value of a "paths" setting. Every entry is a map
// with the keys path and cluster (both required) and repo_url (optional).
// Errors never contain the offending values: they may come from the
// environment.
func ParsePathSources(raw any) ([]PathSource, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, errPathSources
	}
	out := make([]PathSource, 0, len(list))
	for i, item := range list {
		src, err := parsePathSource(item)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", i+1, err)
		}
		out = append(out, src)
	}
	return out, nil
}

func parsePathSource(item any) (PathSource, error) {
	m, ok := item.(map[string]any)
	if !ok {
		return PathSource{}, errPathEntry
	}
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if key != "path" && key != "cluster" && key != "repo_url" {
			return PathSource{}, fmt.Errorf("unknown key %q; only \"path\", \"cluster\" and \"repo_url\" are allowed", key)
		}
	}
	path, ok := m["path"].(string)
	if !ok || path == "" {
		return PathSource{}, errors.New("path must be a non-empty string")
	}
	rawCluster, present := m["cluster"]
	if !present {
		return PathSource{}, errors.New("cluster is required")
	}
	cluster, ok := rawCluster.(string)
	if !ok || !clusterName.MatchString(cluster) {
		return PathSource{}, errCluster
	}
	src := PathSource{Path: path, Cluster: cluster}
	if raw, present := m["repo_url"]; present {
		u, ok := raw.(string)
		if !ok || !validURL(u) {
			return PathSource{}, errors.New("repo_url: " + urlHint)
		}
		src.RepoURL = u
	}
	return src, nil
}
