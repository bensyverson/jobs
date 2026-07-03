package job

import (
	"bytes"
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

type orientHeaderDoc struct {
	Target     string        `yaml:"target"`
	Title      string        `yaml:"title"`
	Root       string        `yaml:"root"`
	Status     string        `yaml:"status"`
	BlockedBy  []string      `yaml:"blockedBy"`
	Blocks     []blockRefDoc `yaml:"blocks"`
	Criteria   tallyDoc      `yaml:"criteria"`
	OwnNotes   []string      `yaml:"own_notes"`
	WeighNotes []string      `yaml:"weigh_notes"`
}

type blockRefDoc struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
}

type tallyDoc struct {
	Passed int `yaml:"passed"`
	Total  int `yaml:"total"`
}

type orientNodeDoc struct {
	Title    string         `yaml:"title"`
	ID       string         `yaml:"id"`
	Status   string         `yaml:"status"`
	Target   bool           `yaml:"target,omitempty"`
	Closed   string         `yaml:"closed,omitempty"`
	Desc     string         `yaml:"desc,omitempty"`
	Labels   []string       `yaml:"labels,omitempty"`
	Blocks   []string       `yaml:"blocks,omitempty"`
	Criteria []criterionDoc `yaml:"criteria,omitempty"`
	// CompletionNote is the elided view's single most-recent-close
	// breadcrumb; at most one node in the document carries it.
	CompletionNote string           `yaml:"completion_note,omitempty"`
	Notes          []string         `yaml:"notes,omitempty"`
	Children       []*orientNodeDoc `yaml:"children,omitempty"`
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

func toHeaderDoc(h OrientHeader) orientHeaderDoc {
	doc := orientHeaderDoc{
		Target:     h.Target,
		Title:      h.Title,
		Root:       h.Root,
		Status:     h.Status,
		BlockedBy:  emptyIfNil(h.BlockedBy),
		Blocks:     []blockRefDoc{},
		Criteria:   tallyDoc{Passed: h.Criteria.Passed, Total: h.Criteria.Total},
		OwnNotes:   []string{},
		WeighNotes: emptyIfNil(h.WeighNotes),
	}
	for _, b := range h.Blocks {
		doc.Blocks = append(doc.Blocks, blockRefDoc{ID: b.ID, Title: b.Title})
	}
	for _, n := range h.OwnNotes {
		doc.OwnNotes = append(doc.OwnNotes, n.Text)
	}
	return doc
}

func toNodeDoc(n *OrientNode) *orientNodeDoc {
	d := &orientNodeDoc{
		Title:          n.Task.Title,
		ID:             n.Task.ShortID,
		Status:         n.Task.Status,
		Target:         n.Target,
		Desc:           n.Desc,
		Labels:         n.Labels,
		Blocks:         n.Blocks,
		CompletionNote: n.CompletionNote,
	}
	if n.Task.Status == "done" && n.Closed > 0 {
		d.Closed = time.Unix(n.Closed, 0).Format("2006-01-02")
	}
	for _, c := range n.Criteria {
		d.Criteria = append(d.Criteria, criterionDoc{Text: c.Label, State: string(c.State)})
	}
	for _, note := range n.Notes {
		d.Notes = append(d.Notes, note.Text)
	}
	for _, c := range n.Children {
		d.Children = append(d.Children, toNodeDoc(c))
	}
	return d
}

// emptyIfNil normalizes a nil slice to an empty one so header list fields
// render as `[]` rather than being silently equivalent to absent.
func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
