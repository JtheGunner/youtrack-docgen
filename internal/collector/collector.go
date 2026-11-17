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
