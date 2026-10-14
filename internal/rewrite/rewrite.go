// Package rewrite asks the LLM for caption and hashtag variants that tie a
// creator's draft to current trends that genuinely fit it.
package rewrite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trendpilot/internal/cache"
	"trendpilot/internal/trends"
)

type Draft struct {
	Text     string `json:"text"`
	Platform string `json:"platform"` // youtube, tiktok, instagram
	Niche    string `json:"niche,omitempty"`
}

type Caption struct {
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

type Hashtag struct {
	Tag      string  `json:"tag"`
	Trending bool    `json:"trending"` // matched a live trend
	Score    float64 `json:"score,omitempty"`
	Reason   string  `json:"reason"`
}

type Suggestion struct {
	Captions []Caption `json:"captions"`
	Hashtags []Hashtag `json:"hashtags"`
	Skipped  []string  `json:"skipped"` // trends the model judged off-topic
}

type Chatter interface {
	Chat(ctx context.Context, system, user string) (string, error)
}

type Rewriter struct {
	llm   Chatter
	cache *cache.TTL[string, Suggestion]
}

func New(llm Chatter) *Rewriter {
	return &Rewriter{llm: llm, cache: cache.New[string, Suggestion](15 * time.Minute)}
}

const system = `You help a content creator word a post so it is discoverable through current trends.
Rules:
- Only use a trend if it genuinely fits the content. Off-topic trend tags get posts down-ranked as spam; list those under "skipped".
- Keep the creator's voice and meaning. Make small wording changes, not a rewrite.
- 3 caption variants. 3-8 hashtags total, mixing broad and niche.
- Reply with JSON only:
{"captions":[{"text":"...","reason":"..."}],"hashtags":[{"tag":"#...","reason":"..."}],"skipped":["#..."]}`

func (r *Rewriter) Suggest(ctx context.Context, d Draft, ts []trends.Trend) (Suggestion, error) {
	if strings.TrimSpace(d.Text) == "" {
		return Suggestion{}, fmt.Errorf("draft text is empty")
	}
	ts = ts[:min(len(ts), 25)]
	key := cacheKey(d, ts)
	if s, ok := r.cache.Get(key); ok {
		return s, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Platform: %s\n", d.Platform)
	if d.Niche != "" {
		fmt.Fprintf(&b, "Creator niche: %s\n", d.Niche)
	}
	fmt.Fprintf(&b, "Draft:\n%s\n\nLive trends (best opportunity first; growth>0 is rising, low crowding is less competition):\n", d.Text)
	for _, t := range ts {
		fmt.Fprintf(&b, "- %s score=%.1f growth=%+.2f crowding=%.2f e.g. %q\n", t.Term, t.Score, t.Growth, t.Crowding, first(t.Examples))
	}

	out, err := r.llm.Chat(ctx, system, b.String())
	if err != nil {
		return Suggestion{}, err
	}
	s, err := Parse(out)
	if err != nil {
		return Suggestion{}, err
	}
	byKey := make(map[string]trends.Trend, len(ts))
	for _, t := range ts {
		byKey[t.Key] = t
	}
	for i, h := range s.Hashtags {
		if !strings.HasPrefix(h.Tag, "#") {
			s.Hashtags[i].Tag = "#" + h.Tag
		}
		if t, ok := byKey[trends.Normalize(h.Tag)]; ok {
			s.Hashtags[i].Trending = true
			s.Hashtags[i].Score = t.Score
		}
	}
	r.cache.Set(key, s)
	return s, nil
}

// Parse pulls the JSON object out of the model's reply, tolerating code
// fences and chatter around it.
func Parse(out string) (Suggestion, error) {
	i, j := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if i < 0 || j <= i {
		return Suggestion{}, fmt.Errorf("model reply had no JSON: %.200s", out)
	}
	var s Suggestion
	if err := json.Unmarshal([]byte(out[i:j+1]), &s); err != nil {
		return Suggestion{}, fmt.Errorf("model reply was not valid JSON: %w", err)
	}
	if len(s.Captions) == 0 {
		return Suggestion{}, fmt.Errorf("model returned no captions")
	}
	return s, nil
}

func cacheKey(d Draft, ts []trends.Trend) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s", d.Platform, d.Niche, d.Text)
	for _, t := range ts {
		fmt.Fprintf(h, "\x00%s:%.1f", t.Key, t.Score)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func first(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[0]
}
