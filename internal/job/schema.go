package job

import (
	"fmt"
	"io"
)

// schemaJSON describes the `job import` grammar as a JSON Schema (Draft 2020-12).
// Hand-authored so the documented semantics (required title, string-arrays, the
// "reserved; not yet persisted" note on labels) don't drift with Go-struct quirks.
const schemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "job import grammar",
  "description": "Root document for ` + "`" + `job import` + "`" + `. Parses the first fenced YAML block in a Markdown file whose top-level key is ` + "`" + `tasks` + "`" + `.",
  "type": "object",
  "required": ["tasks"],
  "properties": {
    "tasks": {
      "type": "array",
      "description": "Tasks form a tree. All work happens on leaves; if a parent has work of its own, model that work as a leaf child rather than putting it on the parent.",
      "items": {
        "type": "object",
        "required": ["title"],
        "properties": {
          "title": {
            "type": "string",
            "description": "Required. Human-readable task title."
          },
          "desc": {
            "type": "string",
            "description": "Optional free-text description. Paragraphs separate with a blank line. Assume the reader is an agent with fresh context, so include the 'why' and supporting context. Don't hard-wrap lines; assume descriptions will be reflowed."
          },
          "labels": {
            "type": "array",
            "items": { "type": "string" },
            "description": "Free-form labels. Persisted; queryable via ` + "`" + `list --label` + "`" + `."
          },
          "ref": {
            "type": "string",
            "description": "Optional author-chosen handle/tag, unique across the entire import. Used by blockedBy entries elsewhere in this document. Refs are not persisted on task rows."
          },
          "blockedBy": {
            "type": "array",
            "items": { "type": "string" },
            "description": "Optional list. Each entry must resolve in order: (1) a ref defined elsewhere in this import; (2) a verbatim task title elsewhere in this import (must be unambiguous); (3) a pre-existing short ID in the database."
          },
          "foundIn": {
            "type": "string",
            "description": "Optional. Records the task that surfaced this one — provenance only, creating no blocking relationship in either direction. One value, not a list: work is found in one place. Resolves exactly as a ` + "`" + `blockedBy` + "`" + ` entry does: (1) a ref defined elsewhere in this import; (2) a verbatim task title elsewhere in this import (must be unambiguous); (3) a pre-existing short ID in the database. A task cannot be found in itself."
          },
          "kind": {
            "type": "string",
            "enum": ["task", "issue"],
            "description": "Optional tree kind, valid on a root of this import only — a top-level entry imported without --parent. Defaults to 'task'. 'issue' marks an issue-tree: discovered work that next/orient/claim --next skip. Setting it on a child (or on any row when --parent makes every imported task a child) is an error."
          },
          "criteria": {
            "type": "array",
            "items": { "type": "string" },
            "description": "Optional list of acceptance-criteria. Each criterion should be a short but descriptive sentence. All criteria are created in the 'pending' state; transitions land later via 'job done --criterion label=passed' or 'job edit --set-criterion label=passed'."
          },
          "children": {
            "type": "array",
            "items": { "$ref": "#/properties/tasks/items" },
            "description": "Optional sub-tasks, recursive. Same grammar as the root task entries."
          }
        },
        "additionalProperties": false
      }
    }
  }
}`

func RunSchema(w io.Writer) error {
	if _, err := fmt.Fprintln(w, schemaJSON); err != nil {
		return err
	}
	return nil
}
