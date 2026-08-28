package server

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/bensyverson/jobs/internal/web/assets"
	"github.com/bensyverson/jobs/internal/web/broadcast"
	"github.com/bensyverson/jobs/internal/web/handlers"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// NewMux builds the dashboard's route table. The URL map mirrors the
// "URL map" section of project/2026-04-21-web-dashboard-vision.md. The
// ctx governs the broadcaster's poll loop — canceling it stops the
// background DB tail that powers /events.
func NewMux(ctx context.Context, cfg Config) http.Handler {
	manifest, err := assets.BuildManifest()
	if err != nil {
		// The asset tree is embedded; a failure here is a build-time
		// defect, not a runtime condition a caller can recover from.
		panic(fmt.Errorf("web: build asset manifest: %w", err))
	}
	engine, err := templates.New(manifest)
	if err != nil {
		panic(fmt.Errorf("web: build template engine: %w", err))
	}

	var bc *broadcast.Broadcaster
	if cfg.DB != nil {
		bc = broadcast.New(cfg.DB, 0)
		go func() {
			if err := bc.Start(ctx); err != nil {
				log.Printf("broadcaster: %v", err)
			}
		}()
	}

	deps := handlers.Deps{DB: cfg.DB, Templates: engine, Broadcaster: bc}

	mux := http.NewServeMux()
	mux.Handle("GET /{$}", handlers.Home(deps))
	mux.Handle("POST /home/graph", handlers.HomeGraph(deps))
	mux.Handle("GET /plan", handlers.Plan(deps))
	mux.Handle("GET /plan/{id}", handlers.Plan(deps))
	mux.Handle("GET /issues", handlers.Issues(deps))
	mux.Handle("GET /issues/{id}", handlers.Issues(deps))
	mux.Handle("GET /actors", handlers.Actors(deps))
	mux.Handle("GET /actors/{name}", handlers.ActorSingle(deps))
	mux.Handle("GET /tasks/{id}", handlers.Task(deps))
	mux.Handle("GET /tasks/{id}/peek", handlers.Peek(deps))
	mux.Handle("GET /log", handlers.Log(deps))
	mux.Handle("GET /events", handlers.Events(deps))
	mux.Handle("GET /labels/{name}", handlers.LabelRedirect(deps))
	mux.Handle("GET /search", handlers.Search(deps))

	mux.Handle("GET /static/", http.StripPrefix("/static/", manifest.Handler()))

	// Catch-all 404 for unmatched paths. `GET /{$}` (Home) is more
	// specific than `GET /`, so the root still hits Home.
	mux.Handle("GET /", handlers.NotFound(deps))

	return mux
}
