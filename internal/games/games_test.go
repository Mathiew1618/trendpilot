package games

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"trendpilot/internal/twitch"
	"trendpilot/internal/youtube"
)

type fakeTwitch struct {
	viewers    map[string][]int // game id -> per-stream viewer counts
	genreCalls int
	pages      int
}

func (f *fakeTwitch) TopGames(ctx context.Context, n int) ([]twitch.Game, error) {
	return []twitch.Game{
		{ID: "1", Name: "Just Chatting"},
		{ID: "2", Name: "Fortnite", IGDBID: "1905"},
		{ID: "3", Name: "Hollow Knight: Silksong", IGDBID: "115289"},
	}, nil
}

func (f *fakeTwitch) Streams(ctx context.Context, gameID, lang string, pages int) ([]twitch.Stream, bool, error) {
	var out []twitch.Stream
	for _, v := range f.viewers[gameID] {
		out = append(out, twitch.Stream{GameID: gameID, ViewerCount: v})
	}
	return out, false, nil
}

func (f *fakeTwitch) Genres(ctx context.Context, ids []string) (map[string][]string, error) {
	f.genreCalls++
	return map[string][]string{"1905": {"Shooter"}, "115289": {"Platform", "Adventure"}}, nil
}

type fakeYT struct{}

func (fakeYT) MostPopular(ctx context.Context, region, category string, max int) ([]youtube.Video, error) {
	return []youtube.Video{
		{ID: "a", Title: "Silksong speedrun is insane", Tags: []string{"hollow knight silksong"}, Views: 50000, PublishedAt: t0.Add(-5 * time.Hour)},
		{ID: "b", Title: "Fortnite chapter 7 live event", Views: 900000, PublishedAt: t0.Add(-3 * time.Hour)},
		{ID: "c", Title: "Fortnite tips", Views: 1000, PublishedAt: t0.Add(-3 * time.Hour)},
	}, nil
}

// Wednesday 2026-10-14 20:00 UTC.
var t0 = time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)

func spread(top, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = top / (i + 1)
	}
	return out
}

func TestScanScoresGrowthGenresAndMentions(t *testing.T) {
	tw := &fakeTwitch{viewers: map[string][]int{
		"1": spread(50000, 100), // huge, high entry bar
		"2": spread(30000, 100),
		"3": append([]int{4000, 3000, 2000}, make([]int, 60)...), // a few big streams, then tiny ones
	}}
	s := NewService(tw, fakeYT{}, NewHistory(""))
	now := t0
	s.now = func() time.Time { return now }

	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	tw.viewers["3"][0] = 13000 // Silksong surges
	now = now.Add(time.Hour)
	snap, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tw.genreCalls != 1 {
		t.Fatalf("genres should be cached, calls=%d", tw.genreCalls)
	}
	by := map[string]GameStat{}
	for _, g := range snap.Games {
		by[g.Name] = g
	}
	silk := by["Hollow Knight: Silksong"]
	if silk.Growth <= 0.5 || silk.EntryBar != 0 || silk.YTVideos != 1 || silk.Genres[0] != "Platform" {
		t.Fatalf("silksong %+v", silk)
	}
	if by["Just Chatting"].Genres[0] != nonGaming || by["Fortnite"].YTVideos != 2 {
		t.Fatalf("chatting %+v fortnite %+v", by["Just Chatting"], by["Fortnite"])
	}
	ops := s.Opportunities("")
	if len(ops) != 2 || ops[0].Name != "Hollow Knight: Silksong" {
		t.Fatalf("opportunities %+v", ops)
	}
	if got := s.Opportunities("shoot"); len(got) != 1 || got[0].Name != "Fortnite" {
		t.Fatalf("genre filter %+v", got)
	}

	o, _ := s.Overview()
	if o.HourAgo == 0 || len(o.Rising) != 1 || o.TopGenres[0].Name != nonGaming {
		t.Fatalf("overview %+v", o)
	}
}

func TestHistoryBestTimesAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.json")
	h := NewHistory(path)
	ny, _ := time.LoadLocation("America/New_York")
	// Same game sampled at two hours: 20:00 UTC busy but crowded, 02:00 UTC
	// smaller audience with a far lower entry bar.
	for i := 0; i < 3; i++ {
		h.Add(Snapshot{At: t0, Viewers: 100, Games: []GameStat{{Key: "fortnite", Name: "Fortnite", Viewers: 60000, EntryBar: 900}}})
		h.Add(Snapshot{At: t0.Add(6 * time.Hour), Viewers: 50, Games: []GameStat{{Key: "fortnite", Name: "Fortnite", Viewers: 20000, EntryBar: 40}}})
	}
	h.AddYouTube("v", t0, 1000)
	h.AddYouTube("v", t0, 1000) // duplicate ignored
	if err := h.Save(); err != nil {
		t.Fatal(err)
	}

	h2 := NewHistory(path)
	bt, ok := h2.Best("fortnite", ny, t0)
	if !ok || bt.Coverage != 2 {
		t.Fatalf("best %+v", bt)
	}
	// Peak audience: Wed 20:00 UTC = 16:00 New York.
	if bt.PeakWeek[0].Hour != 16 || bt.PeakWeek[0].Weekday != time.Wednesday {
		t.Fatalf("peak %+v", bt.PeakWeek[0])
	}
	// Best go-live: Thu 02:00 UTC = Wed 22:00 New York.
	if bt.GoLive[0].Hour != 22 || bt.GoLive[0].Weekday != time.Wednesday {
		t.Fatalf("go-live %+v", bt.GoLive[0])
	}
	if len(bt.NextDay) != 2 || bt.NextDay[0].Hour != 22 {
		t.Fatalf("next day %+v", bt.NextDay)
	}
	if len(bt.YTPublish) != 1 || bt.YTPublish[0] != 16 {
		t.Fatalf("yt publish %v", bt.YTPublish)
	}
	if len(h2.YTSeen) != 1 {
		t.Fatal("seen IDs should persist")
	}
}

func TestFind(t *testing.T) {
	s := NewService(&fakeTwitch{}, nil, NewHistory(""))
	s.Hist.Add(Snapshot{At: t0, Games: []GameStat{
		{Key: "hollowknightsilksong", Name: "Hollow Knight: Silksong"},
		{Key: "hollowknight", Name: "Hollow Knight"},
	}})
	if k, _, ok := s.Find("hollow knight"); !ok || k != "hollowknight" {
		t.Fatalf("exact match failed: %q", k)
	}
	if k, _, ok := s.Find("silksong"); !ok || k != "hollowknightsilksong" {
		t.Fatalf("substring match failed: %q", k)
	}
	if k, _, _ := s.Find(""); k != AllKey {
		t.Fatalf("empty query should mean all games, got %q", k)
	}
}
