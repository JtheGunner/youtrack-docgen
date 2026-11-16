package sink

import (
	"context"

	"github.com/JtheGunner/youtrackt-docgen/internal/render"
)

// Sink persists a rendered document tree.
//
// Implementations only depend on the render package; they never import a
// collector. Publishing the same tree twice must leave the same result.
type Sink interface {
	// Name is the sink name used in the config and in log output.
	Name() string
	// Publish persists root and all of its descendants.
	Publish(ctx context.Context, root render.Document) error
}
