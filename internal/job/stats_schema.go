package job

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// SchemaKind names a JSON Schema `job schema` can print.
type SchemaKind string

const (
	// SchemaPlan is the `job import` plan grammar; the default.
	SchemaPlan SchemaKind = "plan"
	// SchemaStats is the Report `job stats --format=json` emits.
	SchemaStats SchemaKind = "stats"
)

var schemaKinds = []SchemaKind{SchemaPlan, SchemaStats}

// ParseSchemaKind normalizes `job schema`'s argument (trimmed,
// case-insensitive). Empty is SchemaPlan, so bare `job schema` keeps
// printing the grammar agents reach for most.
func ParseSchemaKind(raw string) (k SchemaKind, ok bool) {
	k = SchemaKind(strings.ToLower(strings.TrimSpace(raw)))
	if k == "" {
		return SchemaPlan, true
	}
	if slices.Contains(schemaKinds, k) {
		return k, true
	}
	return k, false
}

// SchemaKindList is the accepted kinds, for help and errors.
func SchemaKindList() string {
	names := make([]string, len(schemaKinds))
	for i, k := range schemaKinds {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}

// WriteSchema prints the JSON Schema kind names, with a trailing newline.
func WriteSchema(w io.Writer, kind SchemaKind) error {
	switch kind {
	case SchemaPlan:
		return RunSchema(w)
	case SchemaStats:
		_, err := fmt.Fprintln(w, statsSchemaJSON)
		return err
	default:
		return fmt.Errorf("unknown schema %q (want one of %s)", kind, SchemaKindList())
	}
}

// statsSchemaJSON describes Report as a JSON Schema (Draft 2020-12).
// Hand-authored like the plan grammar's, so each field carries its
// counting rule in words; TestStatsSchema_MatchesTheReportStruct keeps
// its shape in step with the struct.
var statsSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "job stats report",
  "description": "The output of ` + "`job stats --format=json`" + `: headline figures and a burn-up series for one window. Every count is in leaves — tasks with no children, issue leaves included; parents and issue roots never count. Each sample is the store's state as of its instant, replayed from the event log. Adding a field does not change ` + "`schema`" + `; renaming, removing or changing the meaning of one does.",
  "type": "object",
  "required": ["schema", "window", "leaves", "plans", "pace", "done_by_actor", "series", "activity", "imports"],
  "additionalProperties": false,
  "properties": {
    "schema": {
      "const": ` + fmt.Sprint(ReportSchema) + `,
      "description": "Version of this shape. Refuse a version you do not know."
    },
    "window": {
      "type": "object",
      "description": "What the report covers, after defaults resolved.",
      "required": ["since", "until", "bucket", "timezone"],
      "additionalProperties": false,
      "properties": {
        "scope": { "type": "string", "description": "Short id of the subtree the report covers. Absent for the whole forest." },
        "since": { "$ref": "#/$defs/timestamp", "description": "Window start; the first event in scope when none was asked for." },
        "until": { "$ref": "#/$defs/timestamp", "description": "Window end; the moment the report was built when none was asked for." },
        "bucket": { "type": "string", "enum": ["minute", "5m", "hour", "6h", "12h", "day", "week"], "description": "Width of one sample, on the local calendar: days start at local midnight, weeks on Monday." },
        "timezone": { "type": "string", "description": "IANA zone the buckets align to." }
      }
    },
    "leaves": {
      "type": "object",
      "description": "Leaf counts. created, done and canceled are transitions inside the window; open and blocked are the state at until.",
      "required": ["created", "done", "canceled", "open", "blocked"],
      "additionalProperties": false,
      "properties": {
        "created": { "$ref": "#/$defs/count" },
        "done": { "$ref": "#/$defs/count" },
        "canceled": { "$ref": "#/$defs/count" },
        "open": { "$ref": "#/$defs/count" },
        "blocked": { "$ref": "#/$defs/count", "description": "Open leaves with an unresolved blocker." }
      }
    },
    "plans": {
      "type": "object",
      "description": "Plans are non-issue roots. Import figures come from imported events, which stores began recording on 2026-09-26; earlier imports are not backfilled, so these undercount rather than guess.",
      "required": ["imported", "closed", "open", "median_import_to_close_seconds", "first_import_at"],
      "additionalProperties": false,
      "properties": {
        "imported": { "$ref": "#/$defs/count", "description": "Imported events in the window." },
        "closed": { "$ref": "#/$defs/count", "description": "Plans closed in the window." },
        "open": { "$ref": "#/$defs/count", "description": "Plans open at until." },
        "median_import_to_close_seconds": { "$ref": "#/$defs/seconds", "description": "Median over imported plans closed in the window; null when there are none." },
        "first_import_at": { "type": ["string", "null"], "format": "date-time", "description": "The earliest imported event in scope anywhere in the store up to until; null when the store has none. Tells 'no imports ever recorded' from 'none in this window'." }
      }
    },
    "pace": {
      "type": "object",
      "description": "Throughput and cycle time for leaves closed in the window. A median is null when no leaf qualifies.",
      "required": ["done_per_week", "median_created_to_done_seconds", "median_claimed_to_done_seconds"],
      "additionalProperties": false,
      "properties": {
        "done_per_week": { "type": "number", "minimum": 0 },
        "median_created_to_done_seconds": { "$ref": "#/$defs/seconds" },
        "median_claimed_to_done_seconds": { "$ref": "#/$defs/seconds" }
      }
    },
    "done_by_actor": {
      "type": "array",
      "description": "Leaves closed in the window by the identity that closed them, most first. Nearly every close is run by an agent, some under a human's identity, so this measures identity hygiene as much as who did the work.",
      "items": {
        "type": "object",
        "required": ["actor", "done"],
        "additionalProperties": false,
        "properties": {
          "actor": { "type": "string" },
          "done": { "$ref": "#/$defs/count" }
        }
      }
    },
    "series": {
      "type": "array",
      "description": "One sample per bucket, oldest first; the last sample's end is window.until.",
      "items": { "$ref": "#/$defs/sample" }
    },
    "trace": {
      "type": "array",
      "description": "Present only when a caller asks for it (the dashboard does; job stats does not): the burn-up at drawing resolution, a few hundred samples from the state as of window.since, on a round step, to window.until. Same meaning as series, from the same replay.",
      "items": { "$ref": "#/$defs/sample" }
    },
    "activity": {
      "type": "array",
      "description": "Events per bucket [start, end), aligned with series.",
      "items": {
        "type": "object",
        "required": ["start", "end", "created", "claimed", "done", "blocked"],
        "additionalProperties": false,
        "properties": {
          "start": { "$ref": "#/$defs/timestamp" },
          "end": { "$ref": "#/$defs/timestamp" },
          "created": { "$ref": "#/$defs/count" },
          "claimed": { "$ref": "#/$defs/count" },
          "done": { "$ref": "#/$defs/count" },
          "blocked": { "$ref": "#/$defs/count" }
        }
      }
    },
    "imports": {
      "type": "array",
      "description": "Each imported event in the window, oldest first.",
      "items": {
        "type": "object",
        "required": ["at", "task_id", "title", "source"],
        "additionalProperties": false,
        "properties": {
          "at": { "$ref": "#/$defs/timestamp" },
          "task_id": { "type": "string", "description": "Short id of the top-level task the import created." },
          "title": { "type": "string" },
          "source": { "type": "string", "description": "Base name of the imported file." }
        }
      }
    }
  },
  "$defs": {
    "timestamp": { "type": "string", "format": "date-time" },
    "sample": {
      "type": "object",
      "description": "The store's state as of end, in leaves.",
      "required": ["end", "scope", "done", "open", "blocked", "canceled", "plans_done"],
      "additionalProperties": false,
      "properties": {
        "end": { "$ref": "#/$defs/timestamp", "description": "The instant this sample is the state as of." },
        "scope": { "$ref": "#/$defs/count", "description": "Leaves that exist and are not canceled: the burn-up's upper line." },
        "done": { "$ref": "#/$defs/count", "description": "Leaves done: the lower line. It dips when a leaf is reopened." },
        "open": { "$ref": "#/$defs/count", "description": "scope minus done." },
        "blocked": { "$ref": "#/$defs/count", "description": "The part of open with an unresolved blocker." },
        "canceled": { "$ref": "#/$defs/count", "description": "Leaves canceled as of end; they have left scope." },
        "plans_done": { "$ref": "#/$defs/count", "description": "Plans in the done state as of end." }
      }
    },
    "count": { "type": "integer", "minimum": 0 },
    "seconds": { "type": ["integer", "null"], "minimum": 0 }
  }
}`
