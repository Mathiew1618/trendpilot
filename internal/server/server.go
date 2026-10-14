// Package server is a local JSON API over the app, bound to loopback by
// default, for scripts and future front ends.
package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"trendpilot/internal/app"
	"trendpilot/internal/games"
	"trendpilot/internal/rewrite"
	"trendpilot/internal/usage"
	"trendpilot/internal/youtube"
)

func Handler(a *app.App) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })

	mux.HandleFunc("GET /api/trends", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ts, err := a.Trends(r.Context(), strings.ToUpper(q.Get("region")), q.Get("category"))
		reply(w, ts, err)
	})

	mux.HandleFunc("POST /api/suggest", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			rewrite.Draft
			Region string `json:"region"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		s, err := a.Suggest(r.Context(), req.Draft, strings.ToUpper(req.Region))
		reply(w, s, err)
	})

	mux.HandleFunc("GET /api/watch", func(w http.ResponseWriter, r *http.Request) {
		reply(w, a.Monitor.List(), nil)
	})
	mux.HandleFunc("POST /api/watch", func(w http.ResponseWriter, r *http.Request) {
		id, ok := youtube.VideoID(r.URL.Query().Get("video"))
		if !ok {
			http.Error(w, "video must be a YouTube link or ID", http.StatusBadRequest)
			return
		}
		if err := a.Monitor.Watch(id, ""); err != nil {
			http.Error(w, err.Error(), http.StatusTooManyRequests)
			return
		}
		reply(w, map[string]string{"watching": id}, nil)
	})
	mux.HandleFunc("DELETE /api/watch", func(w http.ResponseWriter, r *http.Request) {
		id, _ := youtube.VideoID(r.URL.Query().Get("video"))
		reply(w, map[string]bool{"removed": a.Monitor.Unwatch(id, "")}, nil)
	})
	mux.HandleFunc("GET /api/usage", func(w http.ResponseWriter, r *http.Request) {
		reply(w, usage.Snapshot(), nil)
	})

	// Game market (needs Twitch credentials).
	gamesOnly := func(h func(w http.ResponseWriter, r *http.Request, g *games.Service)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if a.Games == nil {
				http.Error(w, "Twitch is not configured", http.StatusServiceUnavailable)
				return
			}
			h(w, r, a.Games)
		}
	}
	mux.HandleFunc("GET /api/games", gamesOnly(func(w http.ResponseWriter, r *http.Request, g *games.Service) {
		snap, _ := g.Latest()
		reply(w, map[string]any{"snapshot": snap, "opportunities": g.Opportunities(r.URL.Query().Get("genre"))}, nil)
	}))
	mux.HandleFunc("GET /api/genres", gamesOnly(func(w http.ResponseWriter, r *http.Request, g *games.Service) {
		snap, _ := g.Latest()
		reply(w, games.Genres(snap), nil)
	}))
	mux.HandleFunc("GET /api/viewers", gamesOnly(func(w http.ResponseWriter, r *http.Request, g *games.Service) {
		o, _ := g.Overview()
		reply(w, o, nil)
	}))
	mux.HandleFunc("GET /api/besttime", gamesOnly(func(w http.ResponseWriter, r *http.Request, g *games.Service) {
		key, _, ok := g.Find(r.URL.Query().Get("game"))
		if !ok {
			http.Error(w, "game not seen yet", http.StatusNotFound)
			return
		}
		bt, _ := g.Hist.Best(key, g.Loc, time.Now())
		reply(w, bt, nil)
	}))
	return mux
}

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(v)
}
