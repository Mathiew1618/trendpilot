package games

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"trendpilot/internal/trends"
)

type Shift struct {
	Key    string        `json:"key"`  // "genre:horror", "game:hades2"; used for cooldowns and mutes
	Kind   string        `json:"kind"` // "genre" or "game"
	Name   string        `json:"name"`
	From   int           `json:"from"`
	To     int           `json:"to"`
	Change float64       `json:"change"` // +2.5 = +250%; +Inf for a new entrant
	Window time.Duration `json:"window"`
}

func (s Shift) String() string {
	mins := int(s.Window.Round(time.Minute).Minutes())
	switch {
	case s.From == 0:
		return fmt.Sprintf("**%s** just entered the top Twitch categories with %s viewers.", s.Name, short(s.To))
	case s.Change > 0:
		return fmt.Sprintf("**%s** spiked **+%.0f%%** in the last %d min (%s → %s viewers).", s.Name, s.Change*100, mins, short(s.From), short(s.To))
	default:
		return fmt.Sprintf("**%s** dropped **%.0f%%** in the last %d min (%s → %s viewers).", s.Name, s.Change*100, mins, short(s.From), short(s.To))
	}
}

// Thresholds keep alerts rare enough to be worth reading.
type ShiftRules struct {
	GenreMinViewers int     // ignore genres smaller than this on both sides
	GenreMinChange  float64 // |change| to report for a genre
	GameMinViewers  int
	GameSpike       float64 // game rises at least this much
	GameDrop        float64 // game falls at least this much (positive number)
	NewEntrant      int     // a game new to the top list with at least this many viewers
}

var DefaultRules = ShiftRules{
	GenreMinViewers: 5000, GenreMinChange: 0.30,
	GameMinViewers: 2000, GameSpike: 0.50, GameDrop: 0.40,
	NewEntrant: 3000,
}

// DetectShifts compares two scans. Results are biggest moves first.
func DetectShifts(prev, cur Snapshot, r ShiftRules) []Shift {
	window := cur.At.Sub(prev.At)
	var out []Shift

	before := map[string]GenreStat{}
	for _, g := range Genres(prev) {
		before[g.Name] = g
	}
	for _, g := range Genres(cur) {
		p, ok := before[g.Name]
		if !ok || g.Name == nonGaming || max(p.Viewers, g.Viewers) < r.GenreMinViewers || p.Viewers == 0 {
			continue
		}
		ch := float64(g.Viewers-p.Viewers) / float64(p.Viewers)
		if math.Abs(ch) >= r.GenreMinChange {
			out = append(out, Shift{Key: "genre:" + trends.Normalize(g.Name), Kind: "genre", Name: g.Name, From: p.Viewers, To: g.Viewers, Change: round(ch), Window: window})
		}
	}

	prevGames := map[string]GameStat{}
	for _, g := range prev.Games {
		prevGames[g.Key] = g
	}
	for _, g := range cur.Games {
		p, ok := prevGames[g.Key]
		switch {
		case !ok:
			if g.Viewers >= r.NewEntrant && len(prev.Games) > 0 {
				out = append(out, Shift{Key: "game:" + g.Key, Kind: "game", Name: g.Name, To: g.Viewers, Change: math.Inf(1), Window: window})
			}
		case p.Viewers > 0 && max(p.Viewers, g.Viewers) >= r.GameMinViewers:
			ch := float64(g.Viewers-p.Viewers) / float64(p.Viewers)
			if ch >= r.GameSpike || ch <= -r.GameDrop {
				out = append(out, Shift{Key: "game:" + g.Key, Kind: "game", Name: g.Name, From: p.Viewers, To: g.Viewers, Change: round(ch), Window: window})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return weight(out[i]) > weight(out[j]) })
	return out
}

func weight(s Shift) float64 {
	c := math.Abs(s.Change)
	if math.IsInf(c, 1) {
		c = 3
	}
	return c * math.Log10(float64(max(s.From, s.To))+10)
}

// NewShifts compares the latest scan with the one window ago and returns
// shifts not reported within cooldown.
func (s *Service) NewShifts(window, cooldown time.Duration, r ShiftRules) []Shift {
	cur, ok := s.Latest()
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.agoLocked(cur.At, window)
	if !ok || !prev.At.Before(cur.At) {
		return nil
	}
	if s.reported == nil {
		s.reported = map[string]time.Time{}
	}
	var out []Shift
	for _, sh := range DetectShifts(prev, cur, r) {
		if t, seen := s.reported[sh.Key]; seen && cur.At.Sub(t) < cooldown {
			continue
		}
		s.reported[sh.Key] = cur.At
		out = append(out, sh)
	}
	return out
}

type RaidTarget struct {
	UserID   string        `json:"user_id"`
	Login    string        `json:"login"`
	Name     string        `json:"name"`
	Title    string        `json:"title"`
	Game     string        `json:"game"`
	Viewers  int           `json:"viewers"`
	Uptime   time.Duration `json:"uptime"`
	Language string        `json:"language"`
	Mature   bool          `json:"mature"`
}

// RaidTargets finds small live channels in a game, the kind a raid
// genuinely helps. Channels that just started are skipped because they
// are still setting up.
func (s *Service) RaidTargets(ctx context.Context, game, language string, minViewers, maxViewers int, exclude string) ([]RaidTarget, string, error) {
	g, err := s.tw.GameByName(ctx, game)
	if err != nil {
		return nil, "", err
	}
	var out []RaidTarget
	cursor := ""
	now := s.now()
	for page := 0; page < 10; page++ {
		streams, next, err := s.tw.StreamsPage(ctx, g.ID, language, cursor)
		if err != nil {
			return out, g.Name, err
		}
		for _, st := range streams {
			if st.ViewerCount > maxViewers || st.ViewerCount < minViewers || strings.EqualFold(st.UserLogin, exclude) {
				continue
			}
			up := now.Sub(st.StartedAt)
			if up < 20*time.Minute {
				continue
			}
			out = append(out, RaidTarget{UserID: st.UserID, Login: st.UserLogin, Name: st.UserName, Title: st.Title, Game: g.Name,
				Viewers: st.ViewerCount, Uptime: up.Round(time.Minute), Language: st.Language, Mature: st.IsMature})
		}
		// Streams come most-viewed first: once a page ends below the
		// floor, later pages only get smaller.
		if next == "" || (len(streams) > 0 && streams[len(streams)-1].ViewerCount < minViewers) {
			break
		}
		cursor = next
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Viewers > out[j].Viewers })
	return out[:min(len(out), 5)], g.Name, nil
}

func short(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}
