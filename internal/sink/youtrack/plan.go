package youtrack

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

const diffContext = 3

// Reader is the read-only part of the YouTrack client. A Planner only gets
// this interface, so a dry run cannot write by construction.
type Reader interface {
	SearchArticles(ctx context.Context, query string) ([]client.Article, error)
}

// ChangeKind is what a change does to its article.
type ChangeKind string

// Kinds of change.
const (
	// ChangeCreate creates the article.
	ChangeCreate ChangeKind = "create"
	// ChangeUpdate changes title, parent or generated sections.
	ChangeUpdate ChangeKind = "update"
	// ChangeStale marks the article of a vanished object as stale.
	ChangeStale ChangeKind = "stale"
	// ChangeRestore removes the stale marking: the object is back.
	ChangeRestore ChangeKind = "restore"
)

// Change is one write Publish would make to one article.
type Change struct {
	// Key is the stable document key of the article.
	Key  string
	Kind ChangeKind
	// Diff is the unified diff of the article; it is never empty.
	Diff string
	// Writes lists the write calls of the change, as "<call>:<key>" in call
	// order: create, update, tag-add, tag-remove.
	Writes []string
}

// Plan is what Publish would do, without having done it. It comes from the
// same operation list Publish executes.
type Plan struct {
	Changes   []Change
	unchanged int
	// Warnings are the warnings Publish would report, see Result.Warnings.
	Warnings []string
}

func (p Plan) count(kind ChangeKind) int {
	n := 0
	for _, c := range p.Changes {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

// Created returns the number of articles that would be created.
func (p Plan) Created() int { return p.count(ChangeCreate) }

// Updated returns the number of existing articles that would change.
func (p Plan) Updated() int { return p.count(ChangeUpdate) }

// Stale returns the number of articles that would be marked as stale.
func (p Plan) Stale() int { return p.count(ChangeStale) }

// Restored returns the number of articles whose stale marking would be removed.
func (p Plan) Restored() int { return p.count(ChangeRestore) }

// Unchanged returns the number of articles that are already up to date.
func (p Plan) Unchanged() int { return p.unchanged }

// Summary is a one-line count of the plan.
func (p Plan) Summary() string {
	return fmt.Sprintf("%d to create, %d to update, %d unchanged, %d to mark stale, %d to restore",
		p.Created(), p.Updated(), p.Unchanged(), p.Stale(), p.Restored())
}

// Diff is the unified diff of every changed article, in publish order.
func (p Plan) Diff() string {
	var b strings.Builder
	for _, c := range p.Changes {
		b.WriteString(c.Diff)
	}
	return b.String()
}

// Writes lists every write call Publish would make, in call order.
func (p Plan) Writes() []string {
	var out []string
	for _, c := range p.Changes {
		out = append(out, c.Writes...)
	}
	return out
}

// Planner computes what Publish would change. It only reads from YouTrack.
type Planner struct {
	api Reader
	cfg Config
}

// NewPlanner creates a Planner for the project described by cfg.
func NewPlanner(api Reader, cfg Config) (*Planner, error) {
	if api == nil {
		return nil, errors.New("youtrack sink: reader must not be nil")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Planner{api: api, cfg: cfg}, nil
}

// Plan compares the document tree below root with the articles in YouTrack,
// using the operation list Publish uses. When Publish would refuse the run
// because it marks too many articles as stale, the plan is returned together
// with ErrMassStale, so the dry run still shows what the run would do.
func (pl *Planner) Plan(ctx context.Context, root render.Document) (Plan, error) {
	existing, lost, err := loadIndex(ctx, pl.api, pl.cfg)
	if err != nil {
		return Plan{}, err
	}
	list, guardErr := buildOps(existing, lost, root, pl.cfg)
	if list == nil {
		return Plan{}, guardErr
	}
	plan := Plan{Warnings: lost.all()}
	for _, o := range list.ops {
		switch o.kind {
		case opUnchanged:
			plan.unchanged++
		default:
			plan.Changes = append(plan.Changes, Change{Key: o.key, Kind: o.kind.changeKind(), Diff: o.diff, Writes: o.writes()})
		}
	}
	return plan, guardErr
}

func (k opKind) changeKind() ChangeKind {
	switch k {
	case opCreate:
		return ChangeCreate
	case opMarkStale:
		return ChangeStale
	case opClearStale:
		return ChangeRestore
	}
	return ChangeUpdate
}
