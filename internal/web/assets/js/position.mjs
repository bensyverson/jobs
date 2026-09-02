/*
  Log positions — the dashboard's event cursor.

  Every event the server sends carries `position`, the string form of the
  log position (ts, rep, seq) that internal/eventlog/position.go defines.
  That triple is what every replica agrees on, so it is what `?at=`,
  `?since=` and the replay buffer address events by. The event's `id` is the
  server cache's row id: a DOM key, renumbered by any rebuild, and never a
  cursor.

  The encoding is a cursor, not a sort key. "1000-aaaaaa-9" and
  "1000-aaaaaa-10" compare the wrong way as strings — always parse first and
  use comparePositions.

  A position with an empty replica ("<ts>--<n>") is a legacy cursor: a row
  translated from a pre-log database, addressed by its cache row id because
  it has no replica or seq of its own. It is meaningful only within one
  cache. The empty replica sorts before every real one, which matches the
  server.
*/

// parsePosition decodes "<ts>-<rep>-<seq>" into a comparable triple, or
// returns null when the input is not a position.
export function parsePosition(s) {
  if (typeof s !== "string" || s === "") return null;
  const parts = s.split("-");
  if (parts.length !== 3) return null;
  if (!/^\d+$/.test(parts[0]) || !/^\d+$/.test(parts[2])) return null;
  if (parts[1] !== "" && !/^[0-9A-Za-z]+$/.test(parts[1])) return null;
  const ts = Number(parts[0]);
  const seq = Number(parts[2]);
  if (!(ts > 0) || !(seq > 0)) return null;
  return { ts, rep: parts[1], seq };
}

// isPosition reports whether s parses as a position.
export function isPosition(s) {
  return parsePosition(s) !== null;
}

// comparePositions orders two encoded positions, returning -1, 0 or 1.
// A null / absent / unparseable cursor sorts before every real position —
// that is the "genesis" end of the log, which is what an empty ?at means.
export function comparePositions(a, b) {
  const pa = parsePosition(a);
  const pb = parsePosition(b);
  if (pa === null && pb === null) return 0;
  if (pa === null) return -1;
  if (pb === null) return 1;
  if (pa.ts !== pb.ts) return pa.ts < pb.ts ? -1 : 1;
  if (pa.rep !== pb.rep) return pa.rep < pb.rep ? -1 : 1;
  if (pa.seq !== pb.seq) return pa.seq < pb.seq ? -1 : 1;
  return 0;
}

// samePosition is equality on the parsed triple, so an unparseable value
// never accidentally equals a real cursor.
export function samePosition(a, b) {
  if (!isPosition(a) || !isPosition(b)) return false;
  return comparePositions(a, b) === 0;
}

// sortByPosition orders a list of events ascending by their cursor. The
// server already returns them in this order; the buffer re-sorts because a
// live frame can arrive out of band.
export function sortByPosition(events) {
  return events
    .slice()
    .sort((a, b) => comparePositions(a.position, b.position));
}
