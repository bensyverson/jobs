// Package eventlog is the git-native event store's wire format and file layout:
// the JSONL envelope, the global ordering key, the hybrid logical clock, replica
// ids, the store lock, and the append/read primitives over .jobs/log/*.jsonl.
//
// It is deliberately free of any dependency on SQLite or on internal/job. The
// cache, the typed payloads and apply/rebuild live above it; this package knows
// only that an event is a line of JSON with an opaque data blob.
//
// See project/2026-09-01-git-native-event-log.md.
package eventlog

// base62Chars is the alphabet short ids and replica ids are drawn from. It is a
// copy of the constant of the same name in internal/job/database.go — this
// package must not import internal/job — and the two must stay identical, or
// ids minted here will not match the ids validated there.
const base62Chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
