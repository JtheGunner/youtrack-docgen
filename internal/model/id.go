package model

import (
	"errors"
	"fmt"
	"strings"
)

// Kind is the type of an Entity.
type Kind string

// Entity kinds.
const (
	KindSite      Kind = "site"
	KindNetwork   Kind = "network"
	KindIPAddress Kind = "ipaddress"
	KindHost      Kind = "host"
	KindInterface Kind = "interface"
	KindCluster   Kind = "cluster"
	KindNamespace Kind = "namespace"
	KindWorkload  Kind = "workload"
	KindService   Kind = "service"
	KindIngress   Kind = "ingress"
)

const idSeparator = ":"

// ID is a stable entity identifier of the form "source:kind:key".
type ID string

// ErrInvalidID reports a malformed entity ID or ID component.
var ErrInvalidID = errors.New("invalid entity id")

// NewID builds an ID. Every component must be non-empty and the source, kind
// and key must not contain the separator ":".
func NewID(source string, kind Kind, key string) (ID, error) {
	for name, part := range map[string]string{"source": source, "kind": string(kind), "key": key} {
		if part == "" {
			return "", fmt.Errorf("%w: empty %s", ErrInvalidID, name)
		}
		if strings.Contains(part, idSeparator) {
			return "", fmt.Errorf("%w: %s %q contains %q", ErrInvalidID, name, part, idSeparator)
		}
	}
	return ID(source + idSeparator + string(kind) + idSeparator + key), nil
}

// ParseID splits an ID into its source, kind and key.
func ParseID(id ID) (source string, kind Kind, key string, err error) {
	parts := strings.Split(string(id), idSeparator)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("%w: %q must have 3 parts", ErrInvalidID, id)
	}
	for _, p := range parts {
		if p == "" {
			return "", "", "", fmt.Errorf("%w: %q has an empty part", ErrInvalidID, id)
		}
	}
	return parts[0], Kind(parts[1]), parts[2], nil
}
