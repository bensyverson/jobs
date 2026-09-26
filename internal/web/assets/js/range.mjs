/*
  Client mirror of the `?range=` window a bounded view looks back over.
  The key vocabulary, durations and default mirror
  internal/job/timerange.go; which keys a view offers mirrors
  internal/web/handlers/range.go (boundedViewRanges).

  The server renders the first frame with a cutoff already applied;
  when the scrubber rebuilds a view from the in-memory event log it
  has to apply the same one, measured back from the cursor's moment
  rather than wall-clock now. Keep the keys, the default, the per-view
  offer lists and the arithmetic here identical to the Go side.

  Nothing here is page-specific: the Actors board and the Log share it.
*/

export const RANGE_1H = "1h";
export const RANGE_1D = "1d";
export const RANGE_7D = "7d";
export const RANGE_14D = "14d";
export const RANGE_30D = "30d";
export const RANGE_ALL = "all";

// RANGE_KEYS mirrors job.RangeKeys(): every key, shortest first.
export const RANGE_KEYS = Object.freeze([
  RANGE_1H,
  RANGE_1D,
  RANGE_7D,
  RANGE_14D,
  RANGE_30D,
  RANGE_ALL,
]);

// BOUNDED_VIEW_RANGES mirrors handlers.boundedViewRanges: the keys the
// Actors board and the Log offer. Anything else on those views —
// including 1h and 1d — falls back to the default.
export const BOUNDED_VIEW_RANGES = Object.freeze([RANGE_7D, RANGE_14D, RANGE_30D, RANGE_ALL]);

// DEFAULT_RANGE mirrors job.DefaultRangeKey.
export const DEFAULT_RANGE = RANGE_7D;

const HOUR_SECONDS = 3600;
const DAY_SECONDS = 86400;

// RANGE_SECONDS is the window each key names. RANGE_ALL is absent —
// it has no length, which is what makes it unbounded.
const RANGE_SECONDS = new Map([
  [RANGE_1H, HOUR_SECONDS],
  [RANGE_1D, DAY_SECONDS],
  [RANGE_7D, 7 * DAY_SECONDS],
  [RANGE_14D, 14 * DAY_SECONDS],
  [RANGE_30D, 30 * DAY_SECONDS],
]);

// parseRangeKey normalizes one raw `?range=` value against the keys a
// view offers (every key when omitted). Unknown, unoffered, empty and
// missing values collapse to the default: a range is a view
// preference, not an addressable resource.
export function parseRangeKey(raw, offered = RANGE_KEYS) {
  const key = String(raw ?? "").trim().toLowerCase();
  return RANGE_KEYS.includes(key) && offered.includes(key) ? key : DEFAULT_RANGE;
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

// rangeFromSearch reads `?range=` off a location search string for a
// view offering `offered` and anchors it, returning { key, cutoff }.
// offered is required: a view that forgot it would accept keys its
// server-rendered first frame refused.
export function rangeFromSearch(search, anchorSec, offered) {
  if (!Array.isArray(offered)) {
    throw new TypeError("rangeFromSearch: offered keys are required");
  }
  const params = new URLSearchParams(search ?? "");
  const key = parseRangeKey(params.get("range"), offered);
  return { key, cutoff: rangeCutoff(key, anchorSec) };
}
