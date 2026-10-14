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
	// Related are tags that appear on exactly the same videos (one story's
	// tag set). They are folded into this trend so a single viral video
	// cannot fill the whole list.
	Related     []string `json:"related,omitempty"`
	relatedKeys []string
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
	vs, err := t.src.MostPopular(ctx, region, category, 200)
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
		for _, k := range tr.relatedKeys {
			heat[k] = tr.Heat // same videos, same heat
		}
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
	vids     []int
}

// Score is pure so it can be tested without the network. prev may be nil
// (first snapshot), in which case growth is zero for every term.
func Score(vs []youtube.Video, prev map[string]float64, now time.Time) []Trend {
	terms := map[string]*acc{}
	allChannels := map[string]bool{}
	for i, v := range vs {
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
			a.vids = append(a.vids, i)
			if len(a.examples) < 3 {
				a.examples = append(a.examples, v.Title)
			}
		}
	}

	out := make([]Trend, 0, len(terms))
	sets := map[string][]int{} // key -> indices of the videos using it
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
		sets[k] = a.vids
	}
	// Best first; on a tie prefer the tag that names the story in its
	// titles ("Narnia" over "Aslan"), then the shorter one.
	hits := func(t Trend) int {
		n := 0
		for _, ex := range t.Examples {
			if strings.Contains(Normalize(ex), t.Key) {
				n++
			}
		}
		return n
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if hi, hj := hits(out[i]), hits(out[j]); hi != hj {
			return hi > hj
		}
		if len(out[i].Key) != len(out[j].Key) {
			return len(out[i].Key) < len(out[j].Key)
		}
		return out[i].Key < out[j].Key
	})

	// Fold tags that mostly share videos with a better-ranked tag into it:
	// they are one story's tag set.
	grouped := out[:0:0]
next:
	for _, t := range out {
		for i := range grouped {
			if a, b := sets[t.Key], sets[grouped[i].Key]; jaccard(a, b) >= 0.6 || overlap(a, b) >= 0.8 {
				if len(grouped[i].Related) < 5 {
					grouped[i].Related = append(grouped[i].Related, t.Term)
				}
				grouped[i].relatedKeys = append(grouped[i].relatedKeys, t.Key)
				continue next
			}
		}
		grouped = append(grouped, t)
	}
	return grouped
}

// intersect counts the indices two sorted lists share.
func intersect(a, b []int) int {
	i, j, n := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			n++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return n
}

// jaccard is |a∩b| / |a∪b|.
func jaccard(a, b []int) float64 {
	n := intersect(a, b)
	if u := len(a) + len(b) - n; u > 0 {
		return float64(n) / float64(u)
	}
	return 0
}

// overlap is |a∩b| / min(|a|,|b|): 1 when one set sits inside the other.
func overlap(a, b []int) float64 {
	if m := min(len(a), len(b)); m > 0 {
		return float64(intersect(a, b)) / float64(m)
	}
	return 0
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

// stop lists tags too generic to help anyone be discovered.
var stop = map[string]bool{
	"trailer": true, "officialtrailer": true, "movietrailer": true, "teaser": true,
	"musicvideo": true, "officialmusicvideo": true, "newmusic": true, "officialvideo": true,
	"gaming": true, "gameplay": true, "games": true, "game": true, "movie": true, "movies": true,
	"adventure": true, "comedy": true, "vlog": true, "news": true, "highlights": true,
	"update": true, "preview": true, "stream": true, "streamer": true, "streaming": true,
	"ad": true, "ads": true, "sponsored": true, "the": true, "all": true, "and": true,
	"of": true, "to": true, "in": true, "on": true, "for": true, "with": true, "my": true,
	"you": true, "is": true, "it": true, "this": true, "that": true, "best": true, "top": true,
	"video": true, "videos": true, "youtube": true, "shorts": true, "short": true,
	"new": true, "official": true, "viral": true, "trending": true, "fyp": true,
	"foryou": true, "subscribe": true, "music": true, "funny": true, "live": true,
}

func usable(k string) bool {
	n := len([]rune(k))
	if n < 2 || n > 40 || stop[k] {
		return false
	}
	for _, r := range k {
		if !unicode.IsDigit(r) {
			return true
		}
	}
	return false // bare numbers and years
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
	// Apostrophes vanish ("Magician's" -> "Magicians"); any other
	// punctuation or space splits words, which are CamelCased so the
	// result is a valid hashtag (letters and digits only).
	best = strings.NewReplacer("'", "", "’", "").Replace(best)
	parts := strings.FieldsFunc(best, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	if len(parts) > 1 {
		for i, p := range parts {
			rs := []rune(p)
			rs[0] = unicode.ToUpper(rs[0])
			parts[i] = string(rs)
		}
	}
	return "#" + strings.Join(parts, "")
}

func round(f float64) float64 { return math.Round(f*100) / 100 }
