package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// member is one key of a JSON object, its value kept as the bytes it was
// read from.
type member struct {
	Key   string
	Value json.RawMessage
}

// object is a JSON object that keeps its keys in order. encoding/json's maps
// sort keys on output, and a person's settings file should come back in the
// order they wrote it, with every value it did not touch byte for byte.
type object []member

var errNotObject = errors.New("not a JSON object")

// parseObject reads raw as a JSON object.
func parseObject(raw []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errNotObject
	}
	obj := object{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected %v where a key belongs", tok)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		obj = append(obj, member{Key: key, Value: v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after the object")
	}
	return obj, nil
}

// get returns the value under key, or nil.
func (o object) get(key string) json.RawMessage {
	for _, m := range o {
		if m.Key == key {
			return m.Value
		}
	}
	return nil
}

// set replaces the value under key in place, or appends the key.
func (o object) set(key string, v json.RawMessage) object {
	for i := range o {
		if o[i].Key == key {
			o[i].Value = v
			return o
		}
	}
	return append(o, member{Key: key, Value: v})
}

// marshal renders the object compactly, keys in order.
func (o object) marshal() json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(encodeNoEscape(m.Key))
		buf.WriteByte(':')
		buf.Write(m.Value)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// encodeNoEscape marshals v without encoding/json's HTML escaping, which
// would turn a command's `&` or `<` into & and <.
func encodeNoEscape(v any) json.RawMessage {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// Only strings and plain structs of strings reach here.
		panic(err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// parseArray reads raw as a JSON array, each element kept as raw bytes.
func parseArray(raw []byte) ([]json.RawMessage, error) {
	var a []json.RawMessage
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	return a, nil
}

// marshalArray renders elements compactly, in order.
func marshalArray(a []json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, e := range a {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(e)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

// indent renders a document the way Claude Code writes its settings: two
// spaces, a trailing newline.
func indent(doc json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, doc, "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
