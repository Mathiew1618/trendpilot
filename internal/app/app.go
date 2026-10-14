// Package app wires the agents together. The HTTP API, the CLI and the
// Discord bot are thin front ends over it.
package app

import (
	"context"
	"log/slog"
	"time"

	"trendpilot/internal/cache"
	"trendpilot/internal/games"
	"trendpilot/internal/monitor"
	"trendpilot/internal/rewrite"
	"trendpilot/internal/trends"
)

type App struct {
	Region   string // default region, e.g. "US"
	tracker  *trends.Tracker
	snaps    *cache.TTL[string, []trends.Trend]
	Rewriter *rewrite.Rewriter
	Monitor  *monitor.Monitor
	Games    *games.Service // nil when Twitch credentials are not configured
}

// TrendTTL bounds YouTube quota use: one chart call per region/category per
// TTL however many people ask.
const TrendTTL = 10 * time.Minute

func New(region string, src trends.Source, rw *rewrite.Rewriter, mon *monitor.Monitor) *App {
	return &App{
		Region:   region,
		tracker:  trends.NewTracker(src),
		snaps:    cache.New[string, []trends.Trend](TrendTTL),
		Rewriter: rw,
		Monitor:  mon,
	}
}

func (a *App) Trends(ctx context.Context, region, category string) ([]trends.Trend, error) {
	if region == "" {
		region = a.Region
	}
	key := region + "|" + category
	if ts, ok := a.snaps.Get(key); ok {
		return ts, nil
	}
	ts, err := a.tracker.Snapshot(ctx, region, category)
	if err != nil {
		return nil, err
	}
	a.snaps.Set(key, ts)
	return ts, nil
}

func (a *App) Suggest(ctx context.Context, d rewrite.Draft, region string) (rewrite.Suggestion, error) {
	if d.Platform == "" {
		d.Platform = "youtube"
	}
	ts, err := a.Trends(ctx, region, "")
	if err != nil {
		return rewrite.Suggestion{}, err
	}
	return a.Rewriter.Suggest(ctx, d, ts)
}

// Refresh keeps the default region's snapshot warm so growth is measured
// on a steady cadence, not only when someone asks.
func (a *App) Refresh(ctx context.Context) {
	t := time.NewTicker(TrendTTL)
	defer t.Stop()
	for {
		a.snaps.Delete(a.Region + "|")
		if _, err := a.Trends(ctx, a.Region, ""); err != nil && ctx.Err() == nil {
			slog.Warn("trend refresh failed", "region", a.Region, "err", err)
		}
		a.snaps.Sweep()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
