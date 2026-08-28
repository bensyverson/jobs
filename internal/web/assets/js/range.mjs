/*
  Client mirror of internal/web/handlers/range.go — the `?range=`
  window a bounded view looks back over.

  The server renders the first frame with a cutoff already applied;
  when the scrubber rebuilds a view from the in-memory event log it
  has to apply the same one, measured back from the cursor's moment
  rather than wall-clock now. Keep the keys, the default and the
  arithmetic here identical to range.go.

  Nothing here is page-specific: the Actors board and the Log share it.
*/

export const RANGE_7D = "7d";
export const RANGE_14D = "14d";
export const RANGE_30D = "30d";
export const RANGE_ALL = "all";

// DEFAULT_RANGE mirrors handlers.DefaultRangeKey.
export const DEFAULT_RANGE = RANGE_7D;

const DAY_SECONDS = 86400;

// RANGE_SECONDS is the window each key names. RANGE_ALL is absent —
// it has no length, which is what makes it unbounded.
const RANGE_SECONDS = new Map([
  [RANGE_7D, 7 * DAY_SECONDS],
  [RANGE_14D, 14 * DAY_SECONDS],
  [RANGE_30D, 30 * DAY_SECONDS],
]);

// parseRangeKey normalizes one raw `?range=` value. Unknown, empty
// and missing values collapse to the default: a range is a view
// preference, not an addressable resource.
export function parseRangeKey(raw) {
  const key = String(raw ?? "").trim().toLowerCase();
  switch (key) {
    case RANGE_7D:
    case RANGE_14D:
    case RANGE_30D:
    case RANGE_ALL:
      return key;
    default:
      return DEFAULT_RANGE;
  }
}

// rangeSeconds is the window length for a key; 0 for the unbounded
// "all".
export function rangeSeconds(key) {
  return RANGE_SECONDS.get(key) ?? 0;
}

// rangeCutoff is the unix second at (and after) which events are in
// the window, measured back from anchorSec. 0 means no lower bound.
export function rangeCutoff(key, anchorSec) {
  const span = rangeSeconds(key);
  return span === 0 ? 0 : anchorSec - span;
}

// rangeFromSearch reads `?range=` off a location search string and
// anchors it, returning { key, cutoff }.
export function rangeFromSearch(search, anchorSec) {
  const params = new URLSearchParams(search ?? "");
  const key = parseRangeKey(params.get("range"));
  return { key, cutoff: rangeCutoff(key, anchorSec) };
}
