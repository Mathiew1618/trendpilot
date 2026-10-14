package games

import (
	"sort"
	"strings"
	"time"

	"trendpilot/internal/trends"
)

type GenreStat struct {
	Name    string   `json:"name"`
	Viewers int      `json:"viewers"` // viewers in games tagged with this genre
	Games   []string `json:"games"`   // biggest first, up to 3
	Growth  float64  `json:"growth"`  // viewer-weighted
}

// Genres groups a snapshot by genre. A game with several genres counts
// toward each of them.
func Genres(s Snapshot) []GenreStat {
	by := map[string]*GenreStat{}
	weighted := map[string]float64{}
	for _, g := range s.Games {
		for _, name := range g.Genres {
			gs := by[name]
			if gs == nil {
				gs = &GenreStat{Name: name}
				by[name] = gs
			}
			gs.Viewers += g.Viewers
			weighted[name] += g.Growth * float64(g.Viewers)
			if len(gs.Games) < 3 {
				gs.Games = append(gs.Games, g.Name)
			}
		}
	}
	out := make([]GenreStat, 0, len(by))
	for name, gs := range by {
		if gs.Viewers > 0 {
			gs.Growth = round(weighted[name] / float64(gs.Viewers))
		}
		out = append(out, *gs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Viewers > out[j].Viewers })
	return out
}

type Overview struct {
	At        time.Time   `json:"at"`
	Viewers   int         `json:"viewers"`
	HourAgo   int         `json:"hour_ago,omitempty"`
	DayAgo    int         `json:"day_ago,omitempty"`
	Rising    []GameStat  `json:"rising"`
	Falling   []GameStat  `json:"falling"`
	TopGenres []GenreStat `json:"top_genres"`
}

func (s *Service) Overview() (Overview, bool) {
	now, ok := s.Latest()
	if !ok {
		return Overview{}, false
	}
	o := Overview{At: now.At, Viewers: now.Viewers}
	if h, ok := s.Ago(time.Hour); ok {
		o.HourAgo = h.Viewers
	}
	if d, ok := s.Ago(24 * time.Hour); ok {
		o.DayAgo = d.Viewers
	}
	gs := append([]GameStat(nil), now.Games...)
	sort.Slice(gs, func(i, j int) bool { return gs[i].Growth > gs[j].Growth })
	for _, g := range gs {
		if g.Growth > 0.05 && len(o.Rising) < 3 {
			o.Rising = append(o.Rising, g)
		}
	}
	for i := len(gs) - 1; i >= 0; i-- {
		if gs[i].Growth < -0.05 && len(o.Falling) < 3 {
			o.Falling = append(o.Falling, gs[i])
		}
	}
	o.TopGenres = Genres(now)
	o.TopGenres = o.TopGenres[:min(5, len(o.TopGenres))]
	return o, true
}

// Opportunities ranks the latest scan for a small or mid-size creator,
// optionally limited to one genre.
func (s *Service) Opportunities(genre string) []GameStat {
	now, _ := s.Latest()
	var out []GameStat
	for _, g := range now.Games {
		if g.Genres != nil && g.Genres[0] == nonGaming {
			continue
		}
		if genre != "" && !hasGenre(g, genre) {
			continue
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Opportunity > out[j].Opportunity })
	return out
}

func hasGenre(g GameStat, genre string) bool {
	want := strings.ToLower(genre)
	for _, x := range g.Genres {
		if strings.Contains(strings.ToLower(x), want) {
			return true
		}
	}
	return false
}

// Find resolves a user-typed game name to a history key: exact match first,
// then the shortest tracked name containing it.
func (s *Service) Find(query string) (key, name string, ok bool) {
	q := trends.Normalize(query)
	if q == "" {
		return AllKey, "All games", true
	}
	names := s.Hist.Keys()
	if n, hit := names[q]; hit {
		return q, n, true
	}
	for k, n := range names {
		if k != AllKey && strings.Contains(k, q) && (key == "" || len(k) < len(key)) {
			key, name = k, n
		}
	}
	return key, name, key != ""
}
