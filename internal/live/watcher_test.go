package live

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"trendpilot/internal/twitch"
)

type fakeSrc struct {
	mu     sync.Mutex
	online []bool
}

func (f *fakeSrc) StreamByLogin(ctx context.Context, login string) (twitch.Stream, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	on := f.online[0]
	if len(f.online) > 1 {
		f.online = f.online[1:]
	}
	return twitch.Stream{UserName: "Creator", GameName: "Hades II", Title: "first run", ViewerCount: 40,
		StartedAt: time.Unix(1_800_000_000, 0)}, on, nil
}

type fakePoster struct {
	mu            sync.Mutex
	golive, recap []string
	queued        []*QueueItem
}

func (p *fakePoster) GoLive(ctx context.Context, t string) {
	p.mu.Lock()
	p.golive = append(p.golive, t)
	p.mu.Unlock()
}
func (p *fakePoster) Recap(ctx context.Context, t string) {
	p.mu.Lock()
	p.recap = append(p.recap, t)
	p.mu.Unlock()
}
func (p *fakePoster) Queue(ctx context.Context, it *QueueItem) {
	p.mu.Lock()
	p.queued = append(p.queued, it)
	p.mu.Unlock()
}

type fakeWriter struct{ reply string }

func (f fakeWriter) Chat(ctx context.Context, system, user string) (string, error) {
	if f.reply == "" {
		return "", errors.New("down")
	}
	return f.reply, nil
}

type fakeClipper struct{}

func (fakeClipper) CreateClip(ctx context.Context) (twitch.Clip, error) {
	return twitch.Clip{URL: "https://clips.twitch.tv/X", EditURL: "https://clips.twitch.tv/X/edit"}, nil
}

func TestGoLiveSurgeAndRecap(t *testing.T) {
	src := &fakeSrc{online: []bool{true, true, false, false}}
	p := &fakePoster{}
	w := NewWatcher("Creator", src, p, fakeWriter{reply: `Lets go @everyone <@123> new run in Hades II tonight 🔥 https://evil.example`}, fakeClipper{}, nil, nil)
	w.now = func() time.Time { return time.Unix(1_800_000_000, 0).Add(90 * time.Minute) }
	ctx := context.Background()

	w.Poll(ctx) // goes live
	if len(p.golive) != 1 {
		t.Fatalf("golive %v", p.golive)
	}
	post := p.golive[0]
	if strings.Contains(post, "@everyone") || strings.Contains(post, "<@123>") || strings.Contains(post, "evil.example") || !strings.HasSuffix(post, "https://twitch.tv/creator") {
		t.Fatalf("unsafe go-live text: %q", post)
	}

	w.writer = fakeWriter{reply: "```json\n{\"title\":\"The dodge nobody believed\",\"hashtags\":[\"Hades2\",\"#roguelike\"]}\n```"}
	w.HandleSurge(ctx, Surge{At: time.Unix(1_800_000_000, 0).Add(42 * time.Minute), Sample: []ChatMessage{{User: "fan", Text: "NO WAY"}}})
	if len(p.queued) != 1 {
		t.Fatal("nothing queued")
	}
	it := p.queued[0]
	if it.Title != "The dodge nobody believed" || it.Hashtags[0] != "#Hades2" || it.ClipURL == "" || it.Offset != 42*time.Minute || it.Chat[0] != "fan: NO WAY" {
		t.Fatalf("item %+v", it)
	}
	if got, ok := w.Item(it.ID); !ok || got.Title != it.Title {
		t.Fatal("item lookup")
	}
	w.writer = fakeWriter{reply: `{"title":"Second take","hashtags":["#x"]}`}
	if got, ok := w.NewTitle(ctx, it.ID); !ok || got.Title != "Second take" {
		t.Fatalf("retitle %+v", got)
	}
	if got, _ := w.SetStatus(it.ID, "approved"); got.Status != "approved" || got.Title != "Second take" {
		t.Fatalf("status %+v", got)
	}

	w.Poll(ctx) // still live
	w.Poll(ctx) // one offline blip: ignored
	if len(p.recap) != 0 {
		t.Fatal("recap after a single offline poll")
	}
	w.Poll(ctx) // second offline poll: ended
	if len(p.recap) != 1 || !strings.Contains(p.recap[0], "1 highlight") || !strings.Contains(p.recap[0], "0:42:00") {
		t.Fatalf("recap %v", p.recap)
	}
}

func TestFallbacksWithoutAI(t *testing.T) {
	p := &fakePoster{}
	w := NewWatcher("c", &fakeSrc{online: []bool{true}}, p, fakeWriter{}, nil, nil, nil)
	w.Poll(context.Background())
	if !strings.Contains(p.golive[0], "is live") {
		t.Fatalf("template %q", p.golive[0])
	}
	w.HandleSurge(context.Background(), Surge{At: time.Unix(1_800_000_000, 0).Add(time.Hour)})
	it := p.queued[0]
	if it.Title == "" || it.Hashtags[0] != "#HadesII" || !strings.Contains(it.ClipNote, "connect-twitch") {
		t.Fatalf("fallback item %+v", it)
	}
}
