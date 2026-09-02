package eventlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the envelope schema version carried in every line.
const Version = 1

// Type names an event's kind. The typed constants live in internal/job, which
// owns the payload structs; this package only carries the string.
type Type string

// Envelope is one line of a replica's log. The JSON keys are fixed and the
// encoding is exactly one line: v, rep, seq, ts, actor, type, task, data.
//
// Rep and Seq identify the event globally, Seq being per-replica and gapless
// from 1 so a reader can tell a truncated file from a complete one. TS is a
// hybrid logical clock in milliseconds (see Clock). Task may be empty for an
// event that belongs to no task. Data is the per-type payload, opaque here.
type Envelope struct {
	V     int             `json:"v"`
	Rep   string          `json:"rep"`
	Seq   uint64          `json:"seq"`
	TS    int64           `json:"ts"`
	Actor string          `json:"actor"`
	Type  Type            `json:"type"`
	Task  string          `json:"task"`
	Data  json.RawMessage `json:"data"`
	// Legacy marks a line translated out of a cache that predates the store.
	// Its payload was written before any of it was replayable, so a reader
	// records the event and performs no state write: adoption's snapshot
	// carries the state those payloads used to imply
	// (project/2026-09-01-git-native-event-log.md, "Adoption of a legacy
	// database").
	Legacy bool `json:"legacy,omitempty"`
}

// Marshal encodes e as exactly one line: a single JSON object followed by one
// newline and no other trailing whitespace. A payload carrying newlines is
// compacted, so a log file's lines and its events always correspond.
func Marshal(e Envelope) ([]byte, error) {
	if e.Data != nil && !json.Valid(e.Data) {
		return nil, fmt.Errorf("eventlog: payload for %s is not valid JSON", e.Type)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Keep payload text as written; the log is read by humans in diffs.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return nil, fmt.Errorf("eventlog: encode: %w", err)
	}
	// Encode appends exactly one newline and compacts RawMessage payloads, so
	// the result is already one line; guard the invariant rather than trust it.
	if bytes.Count(buf.Bytes(), []byte("\n")) != 1 {
		return nil, errors.New("eventlog: encoded event spans more than one line")
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes one line. It rejects unknown fields, a wrong version, a
// malformed replica id, an empty type, and a missing or zero rep, seq or ts.
func Unmarshal(line []byte) (Envelope, error) {
	var e Envelope
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return Envelope{}, fmt.Errorf("eventlog: decode: %w", err)
	}
	if dec.More() {
		return Envelope{}, errors.New("eventlog: more than one JSON value on the line")
	}
	if err := e.validate(); err != nil {
		return Envelope{}, err
	}
	if bytes.Equal(e.Data, []byte("null")) {
		e.Data = nil
	}
	return e, nil
}

func (e Envelope) validate() error {
	switch {
	case e.V != Version:
		return fmt.Errorf("eventlog: unsupported envelope version %d (want %d)", e.V, Version)
	case e.Rep == "":
		return errors.New("eventlog: event has no rep")
	case !ValidReplicaID(e.Rep):
		return fmt.Errorf("eventlog: %q is not a replica id", e.Rep)
	case e.Seq == 0:
		return errors.New("eventlog: event has no seq")
	case e.TS == 0:
		return errors.New("eventlog: event has no ts")
	case e.TS < 0:
		return fmt.Errorf("eventlog: event ts %d is negative", e.TS)
	case e.Type == "":
		return errors.New("eventlog: event has no type")
	}
	return nil
}
