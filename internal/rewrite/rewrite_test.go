package rewrite

import (
	"context"
	"testing"

	"trendpilot/internal/trends"
)

type fakeLLM struct{ calls int }

func (f *fakeLLM) Chat(ctx context.Context, system, user string) (string, error) {
	f.calls++
	return "Sure!\n```json\n" + `{"captions":[{"text":"Leaf pile chaos","reason":"fits"}],
		"hashtags":[{"tag":"AutumnVibes","reason":"rising"},{"tag":"#dogsoftiktok","reason":"niche"}],
		"skipped":["#nfl"]}` + "\n```", nil
}

func TestSuggestMarksTrendingAndCaches(t *testing.T) {
	llm := &fakeLLM{}
	r := New(llm)
	ts := []trends.Trend{{Term: "#AutumnVibes", Key: "autumnvibes", Score: 9}}
	d := Draft{Text: "my dog in the leaves", Platform: "youtube"}

	s, err := r.Suggest(context.Background(), d, ts)
	if err != nil {
		t.Fatal(err)
	}
	if s.Hashtags[0].Tag != "#AutumnVibes" || !s.Hashtags[0].Trending || s.Hashtags[0].Score != 9 {
		t.Fatalf("first tag %+v", s.Hashtags[0])
	}
	if s.Hashtags[1].Trending {
		t.Fatal("niche tag should not be marked trending")
	}
	if _, err := r.Suggest(context.Background(), d, ts); err != nil || llm.calls != 1 {
		t.Fatalf("second call should hit the cache, calls=%d err=%v", llm.calls, err)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse("no json here"); err == nil {
		t.Fatal("want error")
	}
}
