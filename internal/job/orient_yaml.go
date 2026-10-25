package job

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"gopkg.in/yaml.v3"
)

// OrientRenderer renders an assembled OrientView to a writer. The YAML
// renderer is the default; the planned markdown renderer (front-matter header
// + markdown-UL tree) drops in behind this same seam.
type OrientRenderer interface {
	RenderOrient(w io.Writer, view *OrientView) error
}

// YAMLOrientRenderer renders an OrientView as structured YAML.
type YAMLOrientRenderer struct{}

// RenderOrient implements OrientRenderer.
func (YAMLOrientRenderer) RenderOrient(w io.Writer, view *OrientView) error {
	return RenderOrientYAML(w, view)
}

// orientDoc / *Doc are YAML projection structs. Field declaration order is the
// emitted key order, so it encodes the per-node contract: identity (id,
// status) → spec (desc, labels, blocks, criteria) → history (notes) last.
type orientDoc struct {
	Orient orientHeaderDoc  `yaml:"orient"`
	Tasks  []*orientNodeDoc `yaml:"tasks"`
}

type orientNodeDoc struct {
	Title  string `yaml:"title"`
	ID     string `yaml:"id"`
	Status string `yaml:"status"`
	Target bool   `yaml:"target,omitempty"`
	Closed string `yaml:"closed,omitempty"`
	Desc   string `yaml:"desc,omitempty"`
	// DoneLeaves and CanceledLeaves stand in for a collapsed container's
	// leaf rows.
	DoneLeaves     int            `yaml:"done_leaves,omitempty"`
	CanceledLeaves int            `yaml:"canceled_leaves,omitempty"`
	Labels         []string       `yaml:"labels,omitempty"`
	BlockedBy      []string       `yaml:"blockedBy,omitempty"`
	Blocks         []string       `yaml:"blocks,omitempty"`
	Criteria       []criterionDoc `yaml:"criteria,omitempty"`
	// CompletionNote is the elided view's single most-recent-close
	// breadcrumb; at most one node in the document carries it.
	CompletionNote string   `yaml:"completion_note,omitempty"`
	Notes          []string `yaml:"notes,omitempty"`
	// EarlierNotes names how many notes the elided view dropped and the
	// command that shows them, e.g. "+41 earlier: job show uyhAEz".
	EarlierNotes string           `yaml:"earlier_notes,omitempty"`
	Children     []*orientNodeDoc `yaml:"children,omitempty"`

	// line, when set, replaces the whole node with a one-line row.
	line *orientLineDoc
}

// orientLineDoc is a closed leaf's one-line row. The close date sits under
// the key naming how it closed, so the row needs no status field.
type orientLineDoc struct {
	Title    string `yaml:"title"`
	ID       string `yaml:"id"`
	Done     string `yaml:"done,omitempty"`
	Canceled string `yaml:"canceled,omitempty"`
}

// MarshalYAML renders a line-shaped node as a single flow-style row, and
// every other node as its block mapping.
func (d orientNodeDoc) MarshalYAML() (any, error) {
	if d.line != nil {
		return flowNode(*d.line)
	}
	type plain orientNodeDoc
	return plain(d), nil
}

type criterionDoc struct {
	Text  string `yaml:"text"`
	State string `yaml:"state"`
}

// RenderOrientYAML writes the OrientView as YAML: a top-level `orient:` header
// followed by the `tasks:` tree. Two-space indentation matches the project's
// own YAML docs.
func RenderOrientYAML(w io.Writer, view *OrientView) error {
	doc := orientDoc{
		Orient: toHeaderDoc(view.Header),
		Tasks:  []*orientNodeDoc{toNodeDoc(view.Tree)},
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func toNodeDoc(n *OrientNode) *orientNodeDoc {
	if n.Shape == OrientShapeLine {
		line := &orientLineDoc{Title: n.Task.Title, ID: n.Task.ShortID}
		date := closedDate(n.Closed)
		if n.Status == "canceled" {
			line.Canceled = date
		} else {
			line.Done = date
		}
		return &orientNodeDoc{line: line}
	}
	d := &orientNodeDoc{
		Title:          n.Task.Title,
		ID:             n.Task.ShortID,
		Status:         n.Status,
		BlockedBy:      n.BlockedBy,
		Target:         n.Target,
		Desc:           n.Desc,
		DoneLeaves:     n.DoneLeaves,
		CanceledLeaves: n.CanceledLeaves,
		Labels:         n.Labels,
		Blocks:         n.Blocks,
		CompletionNote: n.CompletionNote,
	}
	if n.Task.Status == "done" {
		d.Closed = closedDate(n.Closed)
	}
	for _, c := range n.Criteria {
		d.Criteria = append(d.Criteria, criterionDoc{Text: c.Label, State: string(c.State)})
	}
	for _, note := range n.Notes {
		d.Notes = append(d.Notes, note.Text)
	}
	if n.EarlierNotes > 0 {
		d.EarlierNotes = fmt.Sprintf("+%d earlier: job show %s", n.EarlierNotes, n.Task.ShortID)
	}
	for _, c := range n.Children {
		d.Children = append(d.Children, toNodeDoc(c))
	}
	return d
}

// closedDate formats a close timestamp as a date; "" when there is none.
func closedDate(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02")
}

// orientNoTasksDoc is the YAML projection of OrientNoTasksView: the same
// top-level `orient:` + `tasks:` shape as orientDoc, but with a null target
// and a message in place of the synthesized header.
type orientNoTasksDoc struct {
	Orient orientNoTasksHeaderDoc `yaml:"orient"`
	Tasks  []*orientNodeDoc       `yaml:"tasks"`
}

type orientNoTasksHeaderDoc struct {
	Target  *string `yaml:"target"`
	Message string  `yaml:"message"`
}

// RenderOrientNoTasksYAML writes an OrientNoTasksView as YAML, mirroring
// RenderOrientYAML's shape: `target: null` and a `message:` string stand in
// for the usual synthesized header, and `tasks:` renders whatever trees the
// view carries (possibly none, on a genuinely empty repo).
func RenderOrientNoTasksYAML(w io.Writer, view *OrientNoTasksView) error {
	doc := orientNoTasksDoc{
		Orient: orientNoTasksHeaderDoc{Target: nil, Message: view.Message},
		Tasks:  []*orientNodeDoc{},
	}
	for _, tree := range view.Trees {
		doc.Tasks = append(doc.Tasks, toNodeDoc(tree))
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
