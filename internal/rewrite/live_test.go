package rewrite

import (
	"context"
	"os"
	"testing"
	"time"

	"trendpilot/internal/llm"
	"trendpilot/internal/trends"
)

// Run with TRENDPILOT_LIVE=1 to call the real model (Hermes/Nous login or
// TRENDPILOT_LLM_API_KEY).
func TestLiveSuggest(t *testing.T) {
	if os.Getenv("TRENDPILOT_LIVE") == "" {
		t.Skip("set TRENDPILOT_LIVE=1")
	}
	var tok llm.TokenSource = &llm.HermesToken{}
	if k := os.Getenv("TRENDPILOT_LLM_API_KEY"); k != "" {
		tok = llm.StaticToken(k)
	}
	base, model := os.Getenv("TRENDPILOT_LLM_BASE_URL"), os.Getenv("TRENDPILOT_LLM_MODEL")
	if base == "" {
		base, model = "https://inference-api.nousresearch.com/v1", "upstage/solar-mini4:free"
	}
	c := llm.New(base, model, tok)
	ts := []trends.Trend{
		{Term: "#HalloweenCostume", Key: "halloweencostume", Score: 30, Growth: 0.8, Crowding: 0.1, Examples: []string{"DIY costume in 10 minutes"}},
		{Term: "#NFL", Key: "nfl", Score: 25, Growth: 0.1, Crowding: 0.3, Examples: []string{"Week 6 highlights"}},
		{Term: "#AutumnVibes", Key: "autumnvibes", Score: 18, Growth: 0.4, Crowding: 0.05, Examples: []string{"Cozy fall morning"}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s, err := New(c).Suggest(ctx, Draft{Text: "dressed my corgi as a pumpkin and he hated it", Platform: "tiktok", Niche: "pets"}, ts)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", s)
}
