// Package games watches the live game market on Twitch (viewers, streamers,
// genres) and YouTube's trending gaming chart, and learns which hours of the
// week give a creator the most audience for the least competition.
package games

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"trendpilot/internal/trends"
	"trendpilot/internal/twitch"
	"trendpilot/internal/youtube"
)

type GameStat struct {
	ID       string   `json:"id"`
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Genres   []string `json:"genres"`
	Viewers  int      `json:"viewers"`
	Channels int      `json:"channels"`
	More     bool     `json:"more"`      // more live channels than were scanned
	EntryBar int      `json:"entry_bar"` // viewers of the #25 stream: what it takes to be on the first screens
	Growth   float64  `json:"growth"`    // viewer change vs ~1 h ago (0.25 = +25%)
	// TailViewers watch streams outside the top 3: the audience actually
	// spread across smaller channels. One mega-streamer doesn't count.
	TailViewers int `json:"tail_viewers"`
	// Opportunity = tail viewers per entry-bar viewer, nudged by growth: a
	// big audience you can reach the top of with a small stream.
	Opportunity float64 `json:"opportunity"`
	YTVideos    int     `json:"yt_videos"` // trending YouTube gaming videos about it
}

type Snapshot struct {
	At       time.Time  `json:"at"`
	Language string     `json:"language,omitempty"`
	Viewers  int        `json:"viewers"` // across scanned categories
	Games    []GameStat `json:"games"`   // most viewers first
}

type TwitchSource interface {
	TopGames(ctx context.Context, n int) ([]twitch.Game, error)
	Streams(ctx context.Context, gameID, language string, pages int) ([]twitch.Stream, bool, error)
	Genres(ctx context.Context, igdbIDs []string) (map[string][]string, error)
	GenresByName(ctx context.Context, name string) ([]string, error)
	GameByName(ctx context.Context, name string) (twitch.Game, error)
	StreamsPage(ctx context.Context, gameID, language, cursor string) ([]twitch.Stream, string, error)
}

type YouTubeSource interface {
	MostPopular(ctx context.Context, region, category string, max int) ([]youtube.Video, error)
}

const (
	entryRank  = 25
	ytGaming   = "20"
	nonGaming  = "Non-gaming"
	ringWindow = 48 * time.Hour
)

type Service struct {
	TopN     int    // categories to scan
	Pages    int    // pages of 100 streams per category
	Language string // broadcaster language filter, "" = all
	Region   string // YouTube region
	Loc      *time.Location
	Hist     *History

	tw  TwitchSource
	yt  YouTubeSource // may be nil
	now func() time.Time

	mu       sync.RWMutex
	ring     []Snapshot
	genres   map[string][]string  // igdb id -> genres; static, so cached for the process lifetime
	reported map[string]time.Time // shift key -> last alerted
}

func NewService(tw TwitchSource, yt YouTubeSource, hist *History) *Service {
	return &Service{TopN: 30, Pages: 2, Region: "US", Loc: time.Local, Hist: hist,
		tw: tw, yt: yt, now: time.Now, genres: map[string][]string{}}
}

// Latest returns the most recent scan.
func (s *Service) Latest() (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.ring) == 0 {
		return Snapshot{}, false
	}
	return s.ring[len(s.ring)-1], true
}

// Ago returns the scan closest to d before the latest one, if one exists
// within 25% of d.
func (s *Service) Ago(d time.Duration) (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agoLocked(s.now(), d)
}

func (s *Service) agoLocked(now time.Time, d time.Duration) (Snapshot, bool) {
	var best Snapshot
	bestErr := time.Duration(math.MaxInt64)
	for _, sn := range s.ring {
		e := now.Sub(sn.At) - d
		if e < 0 {
			e = -e
		}
		if e < bestErr {
			best, bestErr = sn, e
		}
	}
	return best, bestErr <= d/4
}

func (s *Service) Scan(ctx context.Context) (Snapshot, error) {
	now := s.now()
	top, err := s.tw.TopGames(ctx, s.TopN)
	if err != nil {
		return Snapshot{}, err
	}

	stats := make([]GameStat, len(top))
	errs := make([]error, len(top))
	sem := make(chan struct{}, 4) // stay well inside Twitch's 800 req/min
	var wg sync.WaitGroup
	for i, g := range top {
		wg.Add(1)
		go func(i int, g twitch.Game) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			streams, more, err := s.tw.Streams(ctx, g.ID, s.Language, s.Pages)
			if err != nil {
				errs[i] = err
				return
			}
			stats[i] = summarize(g, streams, more)
		}(i, g)
	}
	wg.Wait()

	s.fillGenres(ctx, top, stats)

	snap := Snapshot{At: now, Language: s.Language}
	for i, st := range stats {
		if errs[i] != nil || st.Key == "" {
			continue
		}
		snap.Games = append(snap.Games, st)
		snap.Viewers += st.Viewers
	}
	if len(snap.Games) == 0 {
		return Snapshot{}, errors.Join(errs...)
	}

	if s.yt != nil {
		if vs, err := s.yt.MostPopular(ctx, s.Region, ytGaming, 100); err != nil {
			slog.Warn("youtube gaming chart failed", "err", err)
		} else {
			mentions(snap.Games, vs)
			for _, v := range vs {
				hours := math.Max(now.Sub(v.PublishedAt).Hours(), 1)
				s.Hist.AddYouTube(v.ID, v.PublishedAt, float64(v.Views)/hours)
			}
		}
	}

	s.mu.Lock()
	if prev, ok := s.agoLocked(now, time.Hour); ok {
		score(snap.Games, prev)
	} else {
		score(snap.Games, Snapshot{})
	}
	sort.Slice(snap.Games, func(i, j int) bool { return snap.Games[i].Viewers > snap.Games[j].Viewers })
	s.ring = append(s.ring, snap)
	for len(s.ring) > 0 && now.Sub(s.ring[0].At) > ringWindow {
		s.ring = s.ring[1:]
	}
	s.mu.Unlock()

	s.Hist.Add(snap)
	return snap, nil
}

func summarize(g twitch.Game, streams []twitch.Stream, more bool) GameStat {
	st := GameStat{ID: g.ID, Key: trends.Normalize(g.Name), Name: g.Name, Channels: len(streams), More: more}
	sort.Slice(streams, func(i, j int) bool { return streams[i].ViewerCount > streams[j].ViewerCount })
	for i, x := range streams {
		st.Viewers += x.ViewerCount
		if i >= 3 {
			st.TailViewers += x.ViewerCount
		}
	}
	if len(streams) >= entryRank {
		st.EntryBar = streams[entryRank-1].ViewerCount
	}
	return st
}

func (s *Service) fillGenres(ctx context.Context, top []twitch.Game, stats []GameStat) {
	var missing []string
	var unlinked []twitch.Game
	s.mu.RLock()
	for _, g := range top {
		if _, ok := s.genres[g.IGDBID]; g.IGDBID != "" && !ok {
			missing = append(missing, g.IGDBID)
		}
		if _, ok := s.genres["name:"+g.Name]; g.IGDBID == "" && !NonGaming(g.Name) && !ok {
			unlinked = append(unlinked, g)
		}
	}
	s.mu.RUnlock()
	for _, g := range unlinked {
		gs, err := s.tw.GenresByName(ctx, g.Name)
		if err != nil {
			slog.Warn("genre search failed", "game", g.Name, "err", err)
			continue
		}
		s.mu.Lock()
		s.genres["name:"+g.Name] = gs
		s.mu.Unlock()
	}
	if len(missing) > 0 {
		found, err := s.tw.Genres(ctx, missing)
		if err != nil {
			slog.Warn("genre lookup failed", "err", err)
		}
		s.mu.Lock()
		for _, id := range missing {
			if err == nil || found[id] != nil {
				s.genres[id] = found[id] // nil = IGDB has no genres; don't ask again
			}
		}
		s.mu.Unlock()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i, g := range top {
		switch {
		case NonGaming(g.Name):
			stats[i].Genres = []string{nonGaming}
		case g.IGDBID == "" && len(s.genres["name:"+g.Name]) > 0:
			stats[i].Genres = s.genres["name:"+g.Name]
		case len(s.genres[g.IGDBID]) > 0:
			stats[i].Genres = s.genres[g.IGDBID]
		default:
			stats[i].Genres = []string{"Other"}
		}
	}
}

// nonGamingCategories are Twitch categories that aren't games.
var nonGamingCategories = map[string]bool{
	"justchatting": true, "irl": true, "slots": true, "virtualcasino": true, "sports": true,
	"alwayson": true, "music": true, "art": true, "talkshowspodcasts": true, "asmr": true,
	"poolshottubsandbeaches": true, "specialevents": true, "travelandoutdoors": true,
	"travelamputdoors": true, "foodanddrink": true, "fooddrink": true, "scienceandtechnology": true,
	"sciencetechnology": true, "softwareandgamedevelopment": true, "softwaregamedevelopment": true,
	"makersandcrafting": true, "makerscrafting": true, "politics": true, "fitnessandhealth": true,
	"fitnesshealth": true, "beautyandbodyart": true, "beautybodyart": true, "animals": true,
	"animalsaquariumsandzoos": true, "animalsaquariumszoos": true, "dj": true, "crypto": true,
	"stocksandbonds": true, "stocksbonds": true, "lottery": true, "pokerstars": true,
	"spirituality": true, "writingandreading": true, "writingreading": true, "co_working": true,
	"coworkingandstudying": true, "coworkingstudying": true, "hometvanddecor": true,
	"housingandhomedecor": true, "parentingandfamily": true, "fashion": true, "watchparty": true,
	"creatorsmp": true, "eventsandeasts": true,
}

// NonGaming reports whether a Twitch category is not a game.
func NonGaming(name string) bool { return nonGamingCategories[trends.Normalize(name)] }

func score(gs []GameStat, prev Snapshot) {
	before := map[string]int{}
	for _, g := range prev.Games {
		before[g.Key] = g.Viewers
	}
	for i := range gs {
		g := &gs[i]
		if p, ok := before[g.Key]; ok && p > 0 {
			g.Growth = round(float64(g.Viewers-p) / float64(p))
		}
		boost := 1 + math.Max(-0.5, math.Min(g.Growth, 1))
		g.Opportunity = round(float64(g.TailViewers) / float64(g.EntryBar+1) * boost)
	}
}

// mentions counts trending YouTube gaming videos whose title or tags
// contain the game's name as whole words.
func mentions(gs []GameStat, vs []youtube.Video) {
	texts := make([][]string, len(vs))
	for i, v := range vs {
		texts[i] = words(v.Title + " " + strings.Join(v.Tags, " "))
	}
	for i := range gs {
		if gs[i].Genres != nil && gs[i].Genres[0] == nonGaming {
			continue
		}
		name := words(gs[i].Name)
		if len(name) == 0 || (len(name) == 1 && len([]rune(name[0])) < 4) {
			continue // too short to match safely ("Rust" ok, "LoL" not)
		}
		for _, t := range texts {
			if containsSeq(t, name) {
				gs[i].YTVideos++
			}
		}
	}
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func containsSeq(hay, needle []string) bool {
outer:
	for i := 0; i+len(needle) <= len(hay); i++ {
		for j, w := range needle {
			if hay[i+j] != w {
				continue outer
			}
		}
		return true
	}
	return false
}
