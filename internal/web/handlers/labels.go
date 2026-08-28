package handlers

import (
	"net/http"
	"net/url"
)

// LabelRedirect implements /labels/{name} as a redirect to the
// query-based filter that already does the work: /plan?label=<name>.
// The label-filter machinery (strip pills, toggle URLs, ?show=
// composition) is query-based by design — "queries only for search,
// sort, filters" — so a path-shaped label route doesn't get its own
// copy of that logic; it just resolves to the canonical URL. A 302
// (not 301) because the mapping is a routing convenience, not a
// permanent rename of the label's identity.
func LabelRedirect(deps Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			NotFound(deps).ServeHTTP(w, r)
			return
		}
		target := "/plan?" + url.Values{"label": {name}}.Encode()
		http.Redirect(w, r, target, http.StatusFound)
	})
}
