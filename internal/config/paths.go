package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// PathSource is one entry of the kubernetes collector's "paths" setting: a
// file or directory of manifests and, optionally, the repository they come
// from.
type PathSource struct {
	Path string
	// RepoURL is the http(s) URL of the repository. Empty means no repository
	// link is shown for the objects read from Path.
	RepoURL string
}

var (
	errPathSources = errors.New("must be a non-empty list of paths or maps with \"path\" and an optional \"repo_url\"")
	errPathEntry   = errors.New("must be a non-empty path or a map with \"path\" and an optional \"repo_url\"")
)

// ParsePathSources reads the value of a "paths" setting. An entry is either a
// plain path or a map with the keys path (required) and repo_url (optional).
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
	if p, ok := item.(string); ok {
		if p == "" {
			return PathSource{}, errors.New("path must not be empty")
		}
		return PathSource{Path: p}, nil
	}
	m, ok := item.(map[string]any)
	if !ok {
		return PathSource{}, errPathEntry
	}
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if key != "path" && key != "repo_url" {
			return PathSource{}, fmt.Errorf("unknown key %q; only \"path\" and \"repo_url\" are allowed", key)
		}
	}
	path, ok := m["path"].(string)
	if !ok || path == "" {
		return PathSource{}, errors.New("path must be a non-empty string")
	}
	src := PathSource{Path: path}
	if raw, present := m["repo_url"]; present {
		u, ok := raw.(string)
		if !ok || !validURL(u) {
			return PathSource{}, errors.New("repo_url: " + urlHint)
		}
		src.RepoURL = u
	}
	return src, nil
}
