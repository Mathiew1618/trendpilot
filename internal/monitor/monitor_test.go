package monitor

import (
	"context"
	"testing"
	"time"

	"trendpilot/internal/youtube"
)

type fakeStats struct{ views, comments []int64 }

func (f *fakeStats) Stats(ctx context.Context, ids []string) ([]youtube.Video, error) {
	v := youtube.Video{ID: "v1", Title: "Leaf pile", Views: f.views[0], Comments: f.comments[0]}
	f.views, f.comments = f.views[1:], f.comments[1:]
	return []youtube.Video{v}, nil
}

func TestAlerts(t *testing.T) {
	src := &fakeStats{
		views:    []int64{0, 100, 200, 300, 1300},
		comments: []int64{0, 0, 0, 2, 2},
	}
	var got []Alert
	m := New(src, func(owner string, a Alert) {
		if owner != "chan-1" {
			t.Errorf("owner %q", owner)
		}
		got = append(got, a)
	})
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	m.Watch("v1", "chan-1")

	for i := 0; i < 5; i++ {
		if err := m.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Hour)
	}
	if len(got) != 2 || got[0].Kind != "comments" || got[1].Kind != "breakout" {
		t.Fatalf("alerts %+v", got)
	}
	st := m.List()[0]
	if st.ViewsPerHour != 1000 || st.Latest.Views != 1300 {
		t.Fatalf("status %+v", st)
	}
}

type staticStats struct{ calls int }

func (f *staticStats) Stats(ctx context.Context, ids []string) ([]youtube.Video, error) {
	f.calls++
	if len(ids) != 1 {
		panic("ids should be de-duplicated")
	}
	return []youtube.Video{{ID: "v1", Title: "t", Comments: int64(f.calls)}}, nil
}

func TestEachOwnerGetsAlerts(t *testing.T) {
	owners := map[string]int{}
	m := New(&staticStats{}, func(owner string, a Alert) { owners[owner]++ })
	m.Watch("v1", "a")
	m.Watch("v1", "b")
	m.Poll(context.Background())
	m.Poll(context.Background())
	if owners["a"] != 1 || owners["b"] != 1 {
		t.Fatalf("alerts per owner %v", owners)
	}
	if m.Unwatch("v1", "c") || !m.Unwatch("v1", "a") || len(m.List()) != 1 {
		t.Fatal("unwatch should only remove the caller's entry")
	}
}
