package model

import (
	"maps"
	"net/netip"
	"slices"
)

// Entity is one documented object, independent of the source it came from.
type Entity struct {
	ID     ID
	Kind   Kind
	Name   string
	Source string
	// Labels are source tags and roles; filters match on them.
	Labels map[string]string
	// Attrs are free-form, kind-specific attributes.
	Attrs map[string]string
	// Aliases are additional hostnames that resolve to this entity.
	Aliases []string
	// Addresses are the IP addresses this entity owns.
	Addresses []netip.Addr
}

// clone returns a deep copy so callers and the graph never share mutable state.
func (e Entity) clone() Entity {
	e.Labels = maps.Clone(e.Labels)
	e.Attrs = maps.Clone(e.Attrs)
	e.Aliases = slices.Clone(e.Aliases)
	e.Addresses = slices.Clone(e.Addresses)
	return e
}
