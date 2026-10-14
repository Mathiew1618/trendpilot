// Package monitor tracks how a creator's posts perform and tells the creator
// when to act (reply to new comments, cross-post a breakout). It never
// engages on the creator's behalf.
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"trendpilot/internal/youtube"
)

type Sample struct {
	At       time.Time `json:"at"`
	Views    int64     `json:"views"`
	Likes    int64     `json:"likes"`
	Comments int64     `json:"comments"`
}

type Alert struct {
	VideoID string `json:"video_id"`
	Title   string `json:"title"`
	Kind    string `json:"kind"` // "comments" or "breakout"
	Message string `json:"message"`
}

type Status struct {
	VideoID      string   `json:"video_id"`
	Title        string   `json:"title"`
	Owner        string   `json:"owner,omitempty"` // who asked to watch it (e.g. a Discord channel)
	Latest       Sample   `json:"latest"`
	ViewsPerHour float64  `json:"views_per_hour"`
	Engagement   float64  `json:"engagement"` // (likes+comments)/views
	Samples      []Sample `json:"-"`
}

type StatsSource interface {
	Stats(ctx context.Context, ids []string) ([]youtube.Video, error)
}

type Notifier func(owner string, a Alert)

const maxSamples = 288 // one day at 5-minute polls; RAM only

type Monitor struct {
	src    StatsSource
	notify Notifier
	now    func() time.Time

	// Breakout fires when the latest views/hour is this many times the
	// median of earlier intervals.
	BreakoutFactor float64

	mu     sync.Mutex
	videos map[key]*Status // one entry per (owner, video) so each owner gets its own alerts
}

type key struct{ owner, id string }

func New(src StatsSource, notify Notifier) *Monitor {
	return &Monitor{src: src, notify: notify, now: time.Now, BreakoutFactor: 3, videos: map[key]*Status{}}
}

// MaxPerOwner caps how many videos one owner (a Discord channel, or the
// local API) can track, which bounds YouTube quota use.
const MaxPerOwner = 25

func (m *Monitor) Watch(id, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.videos[key{owner, id}]; ok {
		return nil
	}
	n := 0
	for _, s := range m.videos {
		if s.Owner == owner {
			n++
		}
	}
	if n >= MaxPerOwner {
		return fmt.Errorf("already watching %d videos here; /unwatch one first", MaxPerOwner)
	}
	m.videos[key{owner, id}] = &Status{VideoID: id, Owner: owner}
	return nil
}

// Unwatch removes id if owner is the one watching it. An empty owner (the
// local API) may remove anything.
func (m *Monitor) Unwatch(id, owner string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := false
	for k := range m.videos {
		if k.id == id && (owner == "" || k.owner == owner) {
			delete(m.videos, k)
			removed = true
		}
	}
	return removed
}

func (m *Monitor) List() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.videos))
	for _, s := range m.videos {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner != out[j].Owner {
			return out[i].Owner < out[j].Owner
		}
		return out[i].VideoID < out[j].VideoID
	})
	return out
}

// Run polls every interval until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := m.Poll(ctx); err != nil {
			slog.Warn("monitor poll failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Monitor) Poll(ctx context.Context) error {
	m.mu.Lock()
	seen := map[string]bool{}
	var ids []string
	for k := range m.videos {
		if !seen[k.id] {
			seen[k.id] = true
			ids = append(ids, k.id)
		}
	}
	m.mu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	vs, err := m.src.Stats(ctx, ids)
	if err != nil {
		return err
	}
	now := m.now()
	type pending struct {
		owner string
		a     Alert
	}
	var alerts []pending

	byID := make(map[string]youtube.Video, len(vs))
	for _, v := range vs {
		byID[v.ID] = v
	}

	m.mu.Lock()
	for k, st := range m.videos {
		v, ok := byID[k.id]
		if !ok {
			continue // deleted or private, or watched mid-poll
		}
		st.Title = v.Title
		cur := Sample{At: now, Views: v.Views, Likes: v.Likes, Comments: v.Comments}
		if n := len(st.Samples); n > 0 {
			prev := st.Samples[n-1]
			if d := cur.Comments - prev.Comments; d > 0 {
				alerts = append(alerts, pending{st.Owner, Alert{v.ID, v.Title, "comments",
					fmt.Sprintf("%d new comment(s) on %q. Replying now keeps the thread active.", d, v.Title)}})
			}
			rate := perHour(prev, cur)
			if base := median(rates(st.Samples)); base > 0 && rate >= m.BreakoutFactor*base {
				alerts = append(alerts, pending{st.Owner, Alert{v.ID, v.Title, "breakout",
					fmt.Sprintf("%q is breaking out: %.0f views/h vs a usual %.0f. Good moment to pin a comment or share it elsewhere.", v.Title, rate, base)}})
			}
			st.ViewsPerHour = rate
		}
		st.Samples = append(st.Samples, cur)
		if len(st.Samples) > maxSamples {
			st.Samples = st.Samples[len(st.Samples)-maxSamples:]
		}
		st.Latest = cur
		if cur.Views > 0 {
			st.Engagement = float64(cur.Likes+cur.Comments) / float64(cur.Views)
		}
	}
	m.mu.Unlock()

	for _, p := range alerts {
		m.notify(p.owner, p.a)
	}
	return nil
}

func perHour(a, b Sample) float64 {
	h := b.At.Sub(a.At).Hours()
	if h <= 0 {
		return 0
	}
	return float64(b.Views-a.Views) / h
}

func rates(ss []Sample) []float64 {
	var out []float64
	for i := 1; i < len(ss); i++ {
		out = append(out, perHour(ss[i-1], ss[i]))
	}
	return out
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[len(s)/2]
}
