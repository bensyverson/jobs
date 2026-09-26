package handlers

import (
	"bytes"
	"html/template"
	"net/http"

	"github.com/bensyverson/jobs/internal/web/templates"
)

// The preview catalog serves each dashboard component in its
// representative states through the real page shell, stylesheet and
// scripts, so it cannot become a second renderer: /preview lists the
// components, /preview/{component} stacks every state under its note,
// and /preview/{component}/{state} is one state whole. A state's
// payload is the production view-model, built by the same function the
// live view calls. It needs no database — see server.NewPreviewMux.

// previewComponent is one catalog entry.
type previewComponent struct {
	Slug  string
	Title string
	// Source is the template that renders the component.
	Source string
	// Block is the template block Source defines.
	Block  string
	States []previewState
}

// previewState is one representative state and its payload.
type previewState struct {
	Slug string
	Name string
	// Note says what a reviewer looks at here, or what once went wrong.
	Note    string
	Payload any
}

// previewCatalog is every component with a catalog entry, in the order
// /preview lists them. Each entry lives beside its component.
func previewCatalog() []previewComponent {
	return []previewComponent{chartPanelPreview()}
}

// PreviewListing describes one component for `--list --json`.
type PreviewListing struct {
	Component string                `json:"component"`
	Title     string                `json:"title"`
	Source    string                `json:"source"`
	URL       string                `json:"url"`
	States    []PreviewStateListing `json:"states"`
}

// PreviewStateListing describes one state and where it is served.
type PreviewStateListing struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Note string `json:"note"`
	URL  string `json:"url"`
}

// PreviewIndex lists the catalog, for an agent walking it
// (`job preview --list --json`).
func PreviewIndex() []PreviewListing {
	var out []PreviewListing
	for _, c := range previewCatalog() {
		out = append(out, c.listing())
	}
	return out
}

func (c previewComponent) listing() PreviewListing {
	l := PreviewListing{Component: c.Slug, Title: c.Title, Source: c.Source, URL: "/preview/" + c.Slug}
	for _, s := range c.States {
		l.States = append(l.States, PreviewStateListing{
			Slug: s.Slug, Name: s.Name, Note: s.Note, URL: l.URL + "/" + s.Slug,
		})
	}
	return l
}

// PreviewMode is which of the catalog's three pages is rendering.
type PreviewMode string

const (
	PreviewModeIndex     PreviewMode = "index"
	PreviewModeComponent PreviewMode = "component"
	PreviewModeState     PreviewMode = "state"
)

// PreviewPageData is the preview page's payload.
type PreviewPageData struct {
	templates.Chrome
	Mode       PreviewMode
	Components []PreviewListing
	Component  PreviewListing
	Rendered   []PreviewRendered
}

// PreviewRendered is one state with its component's markup.
type PreviewRendered struct {
	PreviewStateListing
	HTML template.HTML
}

// Preview serves the catalog. Routes bind {component} and {state};
// either may be empty.
func Preview(deps Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := PreviewPageData{Mode: PreviewModeIndex, Components: PreviewIndex()}
		slug, stateSlug := r.PathValue("component"), r.PathValue("state")
		if slug == "" {
			renderPage(deps, w, "preview", data)
			return
		}
		comp, ok := findPreviewComponent(slug)
		if !ok {
			RenderError(deps, w, http.StatusNotFound, "Component not found", "No component "+slug+" in the preview catalog.")
			return
		}
		data.Component = comp.listing()
		data.Mode = PreviewModeComponent
		if stateSlug != "" {
			data.Mode = PreviewModeState
		}
		for i, s := range comp.States {
			if stateSlug != "" && s.Slug != stateSlug {
				continue
			}
			var buf bytes.Buffer
			if err := deps.Templates.RenderFragment(&buf, "preview", comp.Block, s.Payload); err != nil {
				InternalError(deps, w, "render preview "+slug+"/"+s.Slug, err)
				return
			}
			data.Rendered = append(data.Rendered, PreviewRendered{
				PreviewStateListing: data.Component.States[i],
				// The component's own template escaped it; this only
				// carries the result into the page.
				HTML: template.HTML(buf.String()),
			})
		}
		if len(data.Rendered) == 0 {
			RenderError(deps, w, http.StatusNotFound, "State not found", "No state "+stateSlug+" for "+slug+".")
			return
		}
		renderPage(deps, w, "preview", data)
	})
}

func findPreviewComponent(slug string) (previewComponent, bool) {
	for _, c := range previewCatalog() {
		if c.Slug == slug {
			return c, true
		}
	}
	return previewComponent{}, false
}
