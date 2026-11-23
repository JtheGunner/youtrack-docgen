package render

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/JtheGunner/youtrack-docgen/model"
)

const (
	// maxDiagramNeighbors caps the nodes drawn around the subject of one
	// per-entity diagram so it stays readable.
	maxDiagramNeighbors = 12
	// maxNetworkDiagramHosts caps the hosts drawn per network (or in total
	// when no network is known) in the network overview.
	maxNetworkDiagramHosts = 15
	// maxNetworkDiagramNetworks caps the networks drawn in the overview.
	maxNetworkDiagramNetworks = 20
	// maxLabelRunes truncates node labels.
	maxLabelRunes = 60
)

// Diagram is a diagram in a text-based language such as "mermaid". It is
// sink-neutral: a sink that cannot draw it shows the source as a fenced code
// block, which is also what Markdown emits.
type Diagram struct {
	Language string
	Source   string
	// Lines is the same diagram as a plain list: one line per relation and
	// one per node without a relation. It is what the text format shows.
	Lines []string
}

// DiagramFormat selects how diagrams appear in the documents.
type DiagramFormat string

const (
	// DiagramText shows a diagram as a plain list of its relations. It reads
	// the same in every target and is the default.
	DiagramText DiagramFormat = "text"
	// DiagramMermaid shows a diagram as a fenced "mermaid" code block, for
	// targets that draw Mermaid.
	DiagramMermaid DiagramFormat = "mermaid"
)

// Valid reports whether f is a known format. The empty format is valid and
// means DiagramText.
func (f DiagramFormat) Valid() bool {
	return f == "" || f == DiagramText || f == DiagramMermaid
}

// diagramBlock returns d in the requested format.
func (r *renderer) diagramBlock(d Diagram) Block {
	if r.diagrams == DiagramMermaid {
		return d
	}
	return List(d.Lines)
}

func (d Diagram) markdown() string {
	return "```" + d.Language + "\n" + d.Source + "```\n"
}

// mermaidLabelEscaper turns characters that carry meaning in Mermaid labels,
// Markdown or HTML into Mermaid entity codes. Backticks are encoded so a label
// can never close the surrounding code fence.
var mermaidLabelEscaper = strings.NewReplacer(
	"#", "#35;", "\"", "#quot;", "&", "#amp;", "<", "#lt;", ">", "#gt;",
	"`", "#96;", "\\", "#92;", "|", "#124;", "[", "#91;", "]", "#93;",
	"(", "#40;", ")", "#41;", "{", "#123;", "}", "#125;", ";", "#59;",
	"%", "#37;",
)

// mermaidLabel makes untrusted text safe inside a double-quoted Mermaid label.
func mermaidLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if runes := []rune(s); len(runes) > maxLabelRunes {
		s = string(runes[:maxLabelRunes]) + "..."
	}
	return mermaidLabelEscaper.Replace(s)
}

// plainLabel makes untrusted text a single-line, length-capped label for the
// text format. Escaping for Markdown happens when the list is rendered.
func plainLabel(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if runes := []rune(s); len(runes) > maxLabelRunes {
		s = string(runes[:maxLabelRunes]) + "..."
	}
	return s
}

// flowchart accumulates a Mermaid flowchart and, alongside it, the plain list
// of the same diagram. Node IDs are synthetic (n0, n1, ...) so entity names
// never reach the diagram syntax, only quoted labels.
type flowchart struct {
	lines []string
	ids   map[string]string
	// labels maps a node ID to its plain label; order keeps declaration order.
	labels map[string]string
	order  []string
	// text holds the relation lines; joined holds the nodes that have one.
	text   []string
	joined map[string]bool
}

func newFlowchart() *flowchart {
	return &flowchart{
		lines: []string{"flowchart LR"}, ids: map[string]string{},
		labels: map[string]string{}, joined: map[string]bool{},
	}
}

// node declares a node once per key and returns its synthetic ID.
func (f *flowchart) node(key, label string, kind model.Kind) string {
	if id, ok := f.ids[key]; ok {
		return id
	}
	id := fmt.Sprintf("n%d", len(f.ids))
	f.ids[key] = id
	f.labels[id] = plainLabel(label)
	f.order = append(f.order, id)
	text := `"` + mermaidLabel(label) + `"`
	switch kind {
	case model.KindNetwork:
		f.lines = append(f.lines, fmt.Sprintf("    %s{{%s}}", id, text))
	case model.KindService, model.KindWorkload, model.KindIngress:
		f.lines = append(f.lines, fmt.Sprintf("    %s(%s)", id, text))
	default:
		f.lines = append(f.lines, fmt.Sprintf("    %s[%s]", id, text))
	}
	return id
}

// edge draws a directed edge. The label must be a fixed, trusted string.
func (f *flowchart) edge(from, to, label string) {
	f.joined[from], f.joined[to] = true, true
	if label == "" {
		f.text = append(f.text, f.labels[from]+" - "+f.labels[to])
	} else {
		f.text = append(f.text, f.labels[from]+" "+label+" "+f.labels[to])
	}
	if label == "" {
		f.lines = append(f.lines, fmt.Sprintf("    %s --- %s", from, to))
		return
	}
	f.lines = append(f.lines, fmt.Sprintf("    %s -->|%s| %s", from, label, to))
}

func (f *flowchart) diagram() Diagram {
	lines := slices.Clone(f.text)
	for _, id := range f.order {
		if !f.joined[id] {
			lines = append(lines, f.labels[id])
		}
	}
	return Diagram{Language: "mermaid", Source: strings.Join(f.lines, "\n") + "\n", Lines: lines}
}

// neighbor is one entity adjacent to the subject of a per-entity diagram.
type neighbor struct {
	entity   model.Entity
	label    string
	outgoing bool // true when the relation points from the subject to the entity
}

// entityDiagram draws the direct relations of e and the networks its
// addresses belong to. It returns false when there is nothing to draw. It
// reads only the graph the renderer was given, which is already filtered.
func (r *renderer) entityDiagram(e model.Entity) (Diagram, bool) {
	var all []neighbor
	for _, rel := range r.g.RelationsFrom(e.ID) {
		if other, ok := r.g.Entity(rel.To); ok {
			all = append(all, neighbor{entity: other, label: string(rel.Kind), outgoing: true})
		}
	}
	for _, rel := range r.g.RelationsTo(e.ID) {
		if other, ok := r.g.Entity(rel.From); ok {
			all = append(all, neighbor{entity: other, label: string(rel.Kind)})
		}
	}
	for _, n := range r.nets {
		if n.prefix.IsValid() && slices.ContainsFunc(e.Addresses, n.prefix.Contains) {
			all = append(all, neighbor{entity: n.entity, label: "in_network", outgoing: true})
		}
	}
	if len(all) == 0 {
		return Diagram{}, false
	}
	slices.SortFunc(all, func(a, b neighbor) int {
		if c := strings.Compare(string(a.entity.ID), string(b.entity.ID)); c != 0 {
			return c
		}
		if c := strings.Compare(a.label, b.label); c != 0 {
			return c
		}
		return boolCompare(a.outgoing, b.outgoing)
	})

	f := newFlowchart()
	subject := f.node(string(e.ID), e.Name, e.Kind)
	seen := map[model.ID]bool{}
	for _, nb := range all {
		if !seen[nb.entity.ID] {
			if len(seen) == maxDiagramNeighbors {
				continue
			}
			seen[nb.entity.ID] = true
		}
		other := f.node(string(nb.entity.ID), nb.entity.Name, nb.entity.Kind)
		if nb.outgoing {
			f.edge(subject, other, nb.label)
		} else {
			f.edge(other, subject, nb.label)
		}
	}
	if distinct := countDistinct(all); distinct > len(seen) {
		more := f.node("more", fmt.Sprintf("+%d more", distinct-len(seen)), "")
		f.edge(subject, more, "")
	}
	return f.diagram(), true
}

func countDistinct(all []neighbor) int {
	ids := map[model.ID]bool{}
	for _, nb := range all {
		ids[nb.entity.ID] = true
	}
	return len(ids)
}

func boolCompare(a, b bool) int {
	switch {
	case a == b:
		return 0
	case b:
		return -1
	}
	return 1
}

// networkDiagram draws each network with the hosts whose addresses it
// contains. Without any network prefix it falls back to drawing every host
// with its IP addresses and returns a note saying so.
func (r *renderer) networkDiagram() (d Diagram, note string, ok bool) {
	hosts := r.g.ByKind(model.KindHost)
	f := newFlowchart()

	drawn, extraNets := 0, 0
	for _, n := range r.nets {
		if !n.prefix.IsValid() {
			continue
		}
		if drawn == maxNetworkDiagramNetworks {
			extraNets++
			continue
		}
		drawn++
		netID := f.node(string(n.entity.ID), n.label(), model.KindNetwork)
		shown, total := 0, 0
		for _, h := range hosts {
			if !slices.ContainsFunc(h.Addresses, n.prefix.Contains) {
				continue
			}
			total++
			if shown == maxNetworkDiagramHosts {
				continue
			}
			shown++
			f.edge(netID, f.node(string(h.ID), h.Name, h.Kind), "")
		}
		if total > shown {
			f.edge(netID, f.node("more:"+string(n.entity.ID), fmt.Sprintf("+%d more hosts", total-shown), ""), "")
		}
	}
	if drawn > 0 {
		if extraNets > 0 {
			f.node("more-networks", fmt.Sprintf("+%d more networks", extraNets), "")
		}
		return f.diagram(), "", true
	}

	f = newFlowchart()
	shown := 0
	for _, h := range hosts {
		if len(h.Addresses) == 0 {
			continue
		}
		if shown == maxNetworkDiagramHosts {
			break
		}
		shown++
		hostID := f.node(string(h.ID), h.Name, h.Kind)
		for _, a := range addrStrings(h.Addresses) {
			f.edge(hostID, f.node("ip:"+string(h.ID)+":"+a, a, ""), "")
		}
	}
	if shown == 0 {
		return Diagram{}, "", false
	}
	return f.diagram(), "No network prefixes are known, so the diagram shows the IP addresses of each host.", true
}
