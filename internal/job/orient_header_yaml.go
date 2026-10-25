package job

import "gopkg.in/yaml.v3"

// orientHeaderDoc is the header's YAML projection, in reading order: the
// target in full, its dependencies, then its siblings. Field order is the
// emitted key order. List fields always render, as `[]` when empty, so "no
// blockers" reads as an answer rather than a missing field.
type orientHeaderDoc struct {
	Target    string         `yaml:"target"`
	Title     string         `yaml:"title"`
	Root      string         `yaml:"root"`
	Status    string         `yaml:"status"`
	Desc      string         `yaml:"desc,omitempty"`
	Criteria  []criterionDoc `yaml:"criteria"`
	Notes     []string       `yaml:"notes"`
	BlockedBy []depDoc       `yaml:"blockedBy"`
	Blocks    []depDoc       `yaml:"blocks"`
	Siblings  []siblingDoc   `yaml:"siblings"`
}

type depDoc struct {
	ID         string `yaml:"id"`
	Title      string `yaml:"title"`
	Status     string `yaml:"status"`
	LatestNote string `yaml:"latest_note,omitempty"`
}

type siblingDoc struct {
	ID     string `yaml:"id"`
	Title  string `yaml:"title"`
	Status string `yaml:"status"`
	Notes  int    `yaml:"notes"`
}

// MarshalYAML renders a sibling on a single line: a reader scans the list to
// choose which `job show` to run, and one row per sibling keeps it short.
func (s siblingDoc) MarshalYAML() (any, error) {
	type plain siblingDoc
	return flowNode(plain(s))
}

// flowNode encodes v as a flow-style (single-line) YAML node.
func flowNode(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	n.Style = yaml.FlowStyle
	return &n, nil
}

func toHeaderDoc(h OrientHeader) orientHeaderDoc {
	doc := orientHeaderDoc{
		Target:    h.Target,
		Title:     h.Title,
		Root:      h.Root,
		Status:    h.Status,
		Desc:      h.Desc,
		Criteria:  []criterionDoc{},
		Notes:     []string{},
		BlockedBy: toDepDocs(h.BlockedBy),
		Blocks:    toDepDocs(h.Blocks),
		Siblings:  []siblingDoc{},
	}
	for _, c := range h.Criteria {
		doc.Criteria = append(doc.Criteria, criterionDoc{Text: c.Label, State: string(c.State)})
	}
	for _, n := range h.Notes {
		doc.Notes = append(doc.Notes, n.Text)
	}
	for _, s := range h.Siblings {
		doc.Siblings = append(doc.Siblings, siblingDoc{ID: s.ID, Title: s.Title, Status: s.Status, Notes: s.Notes})
	}
	return doc
}

func toDepDocs(deps []OrientDep) []depDoc {
	out := []depDoc{}
	for _, d := range deps {
		out = append(out, depDoc{ID: d.ID, Title: d.Title, Status: d.Status, LatestNote: d.LatestNote})
	}
	return out
}
