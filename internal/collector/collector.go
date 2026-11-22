package collector

import (
	"context"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// Collector reads one data source and adds what it finds to a graph.
//
// Implementations own the entities they create (IDs start with Name()) and
// never import another collector or a sink. Cross-source linking happens after
// all collectors have run.
type Collector interface {
	// Name is the source name used as the first component of entity IDs.
	Name() string
	// Collect reads the source and adds its entities and relations to g.
	Collect(ctx context.Context, g *model.Graph) error
}

// CompletenessReporter is implemented by collectors that tolerate broken input
// by skipping it. After Collect, Incomplete returns the reasons the collection
// may lack objects that exist in the source; it is empty for a complete read.
// A publish run must not treat the missing objects as vanished.
type CompletenessReporter interface {
	Incomplete() []string
}
