package games

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// AllKey is the history key for the whole scanned market.
const AllKey = "*"

// slot is a moving average for one hour of the week (UTC).
type slot struct {
	Viewers  float64 `json:"v"`
	EntryBar float64 `json:"e"`
	N        int     `json:"n"`
}

// After maxN samples the average becomes exponential, so roughly the last
// five weeks count and old seasons fade out.
const maxN = 30

func (s *slot) add(viewers, entry float64) {
	if s.N < maxN {
		s.N++
	}
	a := 1 / float64(s.N)
	s.Viewers += (viewers - s.Viewers) * a
	s.EntryBar += (entry - s.EntryBar) * a
}

// History is the only state TrendPilot writes to disk: hour-of-week viewer
// averages per game, plus when trending YouTube gaming videos were
// published. No keys, no user data.
type History struct {
	mu        sync.Mutex
	Games     map[string]*[168]slot `json:"games"`
	Names     map[string]string     `json:"names"`
	YTPublish [168]float64          `json:"yt_publish"`
	YTSeen    map[string]time.Time  `json:"yt_seen"`
	path      string
}

func NewHistory(path string) *History {
	h := &History{Games: map[string]*[168]slot{}, Names: map[string]string{}, YTSeen: map[string]time.Time{}, path: path}
	if path == "" {
		return h
	}
	if b, err := os.ReadFile(path); err == nil {
		var loaded History
		if json.Unmarshal(b, &loaded) == nil && loaded.Games != nil {
			h.Games, h.Names, h.YTPublish = loaded.Games, loaded.Names, loaded.YTPublish
			if loaded.YTSeen != nil {
				h.YTSeen = loaded.YTSeen
			}
			if h.Names == nil {
				h.Names = map[string]string{}
			}
		}
	}
	return h
}

func hourOfWeek(t time.Time) int {
	t = t.UTC()
	return int(t.Weekday())*24 + t.Hour()
}

func (h *History) record(key, name string, at time.Time, viewers, entry int) {
	g := h.Games[key]
	if g == nil {
		g = &[168]slot{}
		h.Games[key] = g
	}
	if name != "" {
		h.Names[key] = name
	}
	g[hourOfWeek(at)].add(float64(viewers), float64(entry))
}

func (h *History) Add(s Snapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.record(AllKey, "All games", s.At, s.Viewers, 0)
	for _, g := range s.Games {
		h.record(g.Key, g.Name, s.At, g.Viewers, g.EntryBar)
	}
	h.prune(s.At)
}

// AddYouTube records each trending gaming video's publish hour once,
// weighted by how fast it is gathering views.
func (h *History) AddYouTube(id string, published time.Time, viewsPerHour float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, seen := h.YTSeen[id]; seen {
		return
	}
	h.YTSeen[id] = published
	h.YTPublish[hourOfWeek(published)] += math.Log1p(viewsPerHour)
}

const maxGames = 400

func (h *History) prune(now time.Time) {
	for id, t := range h.YTSeen {
		if now.Sub(t) > 30*24*time.Hour {
			delete(h.YTSeen, id)
		}
	}
	if len(h.Games) <= maxGames {
		return
	}
	type kv struct {
		k string
		n int
	}
	var all []kv
	for k, g := range h.Games {
		n := 0
		for _, s := range g {
			n += s.N
		}
		if k != AllKey {
			all = append(all, kv{k, n})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].n < all[j].n })
	for _, e := range all[:len(h.Games)-maxGames] {
		delete(h.Games, e.k)
		delete(h.Names, e.k)
	}
}

// Save writes atomically so a crash mid-write never corrupts the file.
func (h *History) Save() error {
	if h.path == "" {
		return nil
	}
	h.mu.Lock()
	b, err := json.Marshal(h)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o755); err != nil {
		return err
	}
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, h.path)
}

type Slot struct {
	Weekday  time.Weekday `json:"weekday"` // in the creator's time zone
	Hour     int          `json:"hour"`
	Viewers  float64      `json:"viewers"`
	EntryBar float64      `json:"entry_bar"`
	Score    float64      `json:"score"` // viewers per entry-bar viewer
}

type BestTimes struct {
	Name      string `json:"name"`
	Coverage  int    `json:"coverage"`   // hours of the week with data, of 168
	PeakWeek  []Slot `json:"peak_week"`  // biggest audience this week
	GoLive    []Slot `json:"go_live"`    // best audience-to-competition, any day
	NextDay   []Slot `json:"next_day"`   // best go-live hours in the next 24 h
	YTPublish []int  `json:"yt_publish"` // local hours (0-23) trending gaming videos were published, best first
}

// Best ranks hours for one game (or AllKey). loc is the creator's time zone.
func (h *History) Best(key string, loc *time.Location, now time.Time) (BestTimes, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.Games[key]
	if !ok {
		return BestTimes{}, false
	}
	bt := BestTimes{Name: h.Names[key]}
	weekStart := now.UTC().Truncate(time.Hour).Add(-time.Duration(hourOfWeek(now)) * time.Hour)
	var all []Slot
	for i, s := range g {
		if s.N == 0 {
			continue
		}
		bt.Coverage++
		local := weekStart.Add(time.Duration(i) * time.Hour).In(loc)
		all = append(all, Slot{Weekday: local.Weekday(), Hour: local.Hour(), Viewers: math.Round(s.Viewers), EntryBar: math.Round(s.EntryBar), Score: round(s.Viewers / (s.EntryBar + 1))})
	}
	bt.PeakWeek = top(all, 5, func(s Slot) float64 { return s.Viewers })
	if key != AllKey {
		bt.GoLive = top(all, 5, func(s Slot) float64 { return s.Score })
		var next []Slot
		for i := 0; i < 24; i++ {
			t := now.Add(time.Duration(i) * time.Hour)
			if s := g[hourOfWeek(t)]; s.N > 0 {
				l := t.In(loc)
				next = append(next, Slot{Weekday: l.Weekday(), Hour: l.Hour(), Viewers: math.Round(s.Viewers), EntryBar: math.Round(s.EntryBar), Score: round(s.Viewers / (s.EntryBar + 1))})
			}
		}
		bt.NextDay = top(next, 3, func(s Slot) float64 { return s.Score })
	}
	var byHour [24]float64
	for i, w := range h.YTPublish {
		byHour[weekStart.Add(time.Duration(i)*time.Hour).In(loc).Hour()] += w
	}
	for hr := range byHour {
		if byHour[hr] > 0 {
			bt.YTPublish = append(bt.YTPublish, hr)
		}
	}
	sort.SliceStable(bt.YTPublish, func(i, j int) bool { return byHour[bt.YTPublish[i]] > byHour[bt.YTPublish[j]] })
	bt.YTPublish = bt.YTPublish[:min(3, len(bt.YTPublish))]
	return bt, true
}

// Keys lists tracked games for name lookups.
func (h *History) Keys() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]string, len(h.Names))
	for k, v := range h.Names {
		out[k] = v
	}
	return out
}

func top(xs []Slot, n int, by func(Slot) float64) []Slot {
	s := append([]Slot(nil), xs...)
	sort.SliceStable(s, func(i, j int) bool { return by(s[i]) > by(s[j]) })
	return s[:min(n, len(s))]
}

func round(f float64) float64 { return math.Round(f*100) / 100 }
