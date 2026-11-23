package model

// Label keys shared by collectors (which write them) and the filter (which
// matches rules against them), so both sides agree on one convention.
const (
	// LabelTagPrefix prefixes one label per tag: "tag.<slug>" = "true".
	LabelTagPrefix = "tag."
	// LabelTenant holds the slug (or name) of the owning tenant.
	LabelTenant = "tenant"
)
