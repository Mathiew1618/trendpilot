package games

import (
	"context"
	"math"
	"testing"
	"time"

	"trendpilot/internal/twitch"
)

func (f *fakeTwitch) GameByName(ctx context.Context, name string) (twitch.Game, error) {
	return twitch.Game{ID: "2", Name: "Fortnite"}, nil
}

// Two pages of streams: 100 big ones, then a tail of small ones.
func (f *fakeTwitch) StreamsPage(ctx context.Context, gameID, lang, cursor string) ([]twitch.Stream, string, error) {
	old := t0.Add(-2 * time.Hour)
	if cursor == "" {
		var out []twitch.Stream
		for i := 0; i < 100; i++ {
			out = append(out, twitch.Stream{UserLogin: "big", ViewerCount: 500 - i, StartedAt: old})
		}
		return out, "p2", nil
	}
	f.pages++
	return []twitch.Stream{
		{UserLogin: "me", ViewerCount: 25, StartedAt: old},
		{UserLogin: "good", UserName: "Good", ViewerCount: 22, StartedAt: old},
		{UserLogin: "juststarted", ViewerCount: 20, StartedAt: t0.Add(-5 * time.Minute)},
		{UserLogin: "small", ViewerCount: 8, StartedAt: old},
		{UserLogin: "tiny", ViewerCount: 1, StartedAt: old},
	}, "", nil
}

func TestDetectShifts(t *testing.T) {
	prev := Snapshot{At: t0, Games: []GameStat{
		{Key: "rpg", Name: "Big RPG", Genres: []string{"Role-playing (RPG)"}, Viewers: 50000},
		{Key: "horror", Name: "Scary Game", Genres: []string{"Horror"}, Viewers: 4000},
		{Key: "steady", Name: "Steady", Genres: []string{"Shooter"}, Viewers: 30000},
	}}
	cur := Snapshot{At: t0.Add(20 * time.Minute), Games: []GameStat{
		{Key: "rpg", Name: "Big RPG", Genres: []string{"Role-playing (RPG)"}, Viewers: 30000},
		{Key: "horror", Name: "Scary Game", Genres: []string{"Horror"}, Viewers: 14000},
		{Key: "steady", Name: "Steady", Genres: []string{"Shooter"}, Viewers: 31000},
		{Key: "newbie", Name: "Surprise Drop", Genres: []string{"Indie"}, Viewers: 6000},
	}}
	got := map[string]Shift{}
	for _, s := range DetectShifts(prev, cur, DefaultRules) {
		got[s.Key] = s
	}
	if s := got["genre:horror"]; s.Change != 2.5 || s.Window != 20*time.Minute {
		t.Errorf("horror genre %+v", s)
	}
	if s := got["genre:roleplayingrpg"]; s.Change != -0.4 {
		t.Errorf("rpg genre %+v", s)
	}
	if s := got["game:newbie"]; !math.IsInf(s.Change, 1) {
		t.Errorf("new entrant %+v", s)
	}
	if _, ok := got["game:steady"]; ok {
		t.Error("a 3% move is not an alert")
	}
	if _, ok := got["genre:shooter"]; ok {
		t.Error("shooter genre barely moved")
	}
	if s := got["genre:horror"].String(); s == "" {
		t.Error("empty message")
	}
}

func TestNewShiftsCooldown(t *testing.T) {
	s := NewService(&fakeTwitch{}, nil, NewHistory(""))
	now := t0
	s.now = func() time.Time { return now }
	add := func(v int) {
		s.ring = append(s.ring, Snapshot{At: now, Games: []GameStat{{Key: "x", Name: "X", Genres: []string{"Horror"}, Viewers: v}}})
	}
	add(4000)
	now = now.Add(20 * time.Minute)
	add(12000)
	if got := s.NewShifts(20*time.Minute, 2*time.Hour, DefaultRules); len(got) != 2 { // game + genre
		t.Fatalf("first: %+v", got)
	}
	now = now.Add(20 * time.Minute)
	add(30000)
	if got := s.NewShifts(20*time.Minute, 2*time.Hour, DefaultRules); len(got) != 0 {
		t.Fatalf("cooldown should suppress repeats: %+v", got)
	}
}

func TestRaidTargets(t *testing.T) {
	tw := &fakeTwitch{}
	s := NewService(tw, nil, NewHistory(""))
	s.now = func() time.Time { return t0 }
	got, game, err := s.RaidTargets(context.Background(), "fortnite", "en", 3, 30, "ME")
	if err != nil || game != "Fortnite" {
		t.Fatal(err, game)
	}
	if len(got) != 2 || got[0].Login != "good" || got[1].Login != "small" || got[0].Uptime != 2*time.Hour {
		t.Fatalf("targets %+v", got)
	}
}
