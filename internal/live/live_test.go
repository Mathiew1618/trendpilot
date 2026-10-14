package live

import (
	"context"
	"os"
	"testing"
	"time"
)

// Run with TRENDPILOT_LIVE=1 [TRENDPILOT_LIVE_CHANNEL=name] to read real
// Twitch chat anonymously for 30 seconds.
func TestLiveChat(t *testing.T) {
	if os.Getenv("TRENDPILOT_LIVE") == "" {
		t.Skip("set TRENDPILOT_LIVE=1")
	}
	ch := os.Getenv("TRENDPILOT_LIVE_CHANNEL")
	if ch == "" {
		ch = "monstercat"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out := make(chan ChatMessage, 1024)
	go ReadChat(ctx, ch, out)
	d := NewSurgeDetector()
	n, surges := 0, 0
	for {
		select {
		case m := <-out:
			n++
			if n <= 3 {
				t.Logf("%s: %.60s", m.User, m.Text)
			}
			if _, ok := d.Add(m); ok {
				surges++
			}
		case <-ctx.Done():
			t.Logf("#%s: %d messages in 30s, %d surges", ch, n, surges)
			return
		}
	}
}
