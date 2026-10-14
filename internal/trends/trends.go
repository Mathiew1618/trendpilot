// Package trends turns a platform's trending chart into scored terms
// (hashtags and topic tags), favouring terms that are rising but not yet
// crowded.
package trends

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"trendpilot/internal/youtube"
)

type Trend struct {
	Term     string   `json:"term"`     // display form, e.g. "#AutumnVibes"
	Key      string   `json:"key"`      // normalised: lowercase letters and digits
	Videos   int      `json:"videos"`   // trending videos using the term
	Channels int      `json:"channels"` // distinct channels using it
	Heat     float64  `json:"heat"`     // sum of log(1 + views per hour)
	Growth   float64  `json:"growth"`   // change in heat since the last snapshot
	Crowding float64  `json:"crowding"` // share of trending channels already on it
	Score    float64  `json:"score"`
	Examples []string `json:"examples"` // up to 3 video titles
}

type Source interface {
	MostPopular(ctx context.Context, region, category string, max int) ([]youtube.Video, error)
}

// Tracker keeps the previous snapshot per region/category in memory so it can
// measure growth between polls.
type Tracker struct {
	src  Source
	now  func() time.Time
	mu   sync.Mutex
	prev map[string]map[string]float64 // region|category -> key -> heat
}

func NewTracker(src Source) *Tracker {
	return &Tracker{src: src, now: time.Now, prev: make(map[string]map[string]float64)}
}

// Snapshot fetches the chart, scores every term and returns them best first.
func (t *Tracker) Snapshot(ctx context.Context, region, category string) ([]Trend, error) {
	vs, err := t.src.MostPopular(ctx, region, category, 50)
	if err != nil {
		return nil, err
	}
	scope := region + "|" + category
	t.mu.Lock()
	prev := t.prev[scope]
	t.mu.Unlock()

	out := Score(vs, prev, t.now())

	heat := make(map[string]float64, len(out))
	for _, tr := range out {
		heat[tr.Key] = tr.Heat
	}
	t.mu.Lock()
	t.prev[scope] = heat
	t.mu.Unlock()
	return out, nil
}

type acc struct {
	forms    map[string]int
	videos   int
	channels map[string]bool
	heat     float64
	examples []string
}

// Score is pure so it can be tested without the network. prev may be nil
// (first snapshot), in which case growth is zero for every term.
func Score(vs []youtube.Video, prev map[string]float64, now time.Time) []Trend {
	terms := map[string]*acc{}
	allChannels := map[string]bool{}
	for _, v := range vs {
		allChannels[v.ChannelID] = true
		hours := math.Max(now.Sub(v.PublishedAt).Hours(), 1)
		vph := math.Log1p(float64(v.Views) / hours)
		seen := map[string]bool{}
		for _, form := range extract(v) {
			k := Normalize(form)
			if !usable(k) || seen[k] {
				continue
			}
			seen[k] = true
			a := terms[k]
			if a == nil {
				a = &acc{forms: map[string]int{}, channels: map[string]bool{}}
				terms[k] = a
			}
			a.forms[form]++
			a.videos++
			a.channels[v.ChannelID] = true
			a.heat += vph
			if len(a.examples) < 3 {
				a.examples = append(a.examples, v.Title)
			}
		}
	}

	out := make([]Trend, 0, len(terms))
	for k, a := range terms {
		// A tag only one channel uses is that channel's branding, not a trend.
		if len(a.channels) < 2 {
			continue
		}
		growth := 0.0
		if prev != nil {
			p, ok := prev[k]
			if !ok {
				growth = 1 // new entrant since last poll
			} else {
				growth = (a.heat - p) / (p + 1)
			}
		}
		crowding := float64(len(a.channels)) / float64(len(allChannels))
		g := math.Max(-0.9, math.Min(growth, 3))
		out = append(out, Trend{
			Term: display(a.forms), Key: k,
			Videos: a.videos, Channels: len(a.channels),
			Heat: round(a.heat), Growth: round(growth), Crowding: round(crowding),
			Score:    round(a.heat * (1 + g) * (1 - crowding)),
			Examples: a.examples,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Key < out[j].Key
	})
	return out
}

var hashtagRE = regexp.MustCompile(`#[\p{L}\p{N}_]{2,}`)

func extract(v youtube.Video) []string {
	forms := append([]string{}, v.Tags...)
	forms = append(forms, hashtagRE.FindAllString(v.Title, -1)...)
	forms = append(forms, hashtagRE.FindAllString(v.Description, -1)...)
	return forms
}

// Normalize maps "#Autumn Vibes", "autumnvibes" and "#AUTUMN_VIBES" to the
// same key.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var stop = map[string]bool{
	"video": true, "videos": true, "youtube": true, "shorts": true, "short": true,
	"new": true, "official": true, "viral": true, "trending": true, "fyp": true,
	"foryou": true, "subscribe": true, "music": true, "funny": true, "live": true,
}

func usable(k string) bool {
	n := len([]rune(k))
	return n >= 2 && n <= 40 && !stop[k]
}

// display picks the most common original spelling, rendered as a hashtag.
func display(forms map[string]int) string {
	best, bestN := "", -1
	for f, n := range forms {
		if n > bestN || (n == bestN && f < best) {
			best, bestN = f, n
		}
	}
	best = strings.TrimPrefix(best, "#")
	if strings.ContainsAny(best, " -") {
		// Multi-word tag: CamelCase it so it works as a hashtag.
		parts := strings.FieldsFunc(best, func(r rune) bool { return r == ' ' || r == '-' })
		for i, p := range parts {
			rs := []rune(p)
			rs[0] = unicode.ToUpper(rs[0])
			parts[i] = string(rs)
		}
		best = strings.Join(parts, "")
	}
	return "#" + best
}

func round(f float64) float64 { return math.Round(f*100) / 100 }
