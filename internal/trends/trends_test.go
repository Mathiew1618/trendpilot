package trends

import (
	"context"
	"strings"
	"testing"
	"time"

	"trendpilot/internal/youtube"
)

var now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func vid(ch, title string, views int64, tags ...string) youtube.Video {
	return youtube.Video{ChannelID: ch, Title: title, Views: views, Tags: tags, PublishedAt: now.Add(-10 * time.Hour)}
}

func TestScoreMergesFormsAndDropsSingleChannelTags(t *testing.T) {
	vs := []youtube.Video{
		vid("a", "Leaf pile jump #AutumnVibes", 100000, "MyChannelBrand"),
		vid("b", "Cozy soup", 50000, "autumn vibes"),
		vid("c", "Unrelated", 1000, "MyChannelBrand2"),
		vid("a", "Second from a", 1000, "MyChannelBrand"),
	}
	got := Score(vs, nil, now)
	if len(got) != 1 {
		t.Fatalf("want only the cross-channel term, got %+v", got)
	}
	tr := got[0]
	if tr.Key != "autumnvibes" || tr.Channels != 2 || tr.Videos != 2 {
		t.Fatalf("bad trend %+v", tr)
	}
	if tr.Term != "#AutumnVibes" {
		t.Fatalf("display %q", tr.Term)
	}
	if tr.Growth != 0 {
		t.Fatalf("first snapshot should have zero growth, got %v", tr.Growth)
	}
}

func TestRisingUncrowdedBeatsCrowded(t *testing.T) {
	var vs []youtube.Video
	// "everywhere" is on 8 of 10 channels; "rising" on 2 but new since last poll.
	for i, ch := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		tags := []string{"everywhere"}
		if i < 2 {
			tags = append(tags, "rising")
		}
		vs = append(vs, vid(ch, "t", 20000, tags...))
	}
	vs = append(vs, vid("i", "t", 20000), vid("j", "t", 20000))

	prev := map[string]float64{"everywhere": 60}
	got := Score(vs, prev, now)
	if got[0].Key != "rising" {
		t.Fatalf("want rising first, got %+v", got)
	}
}

type fakeSrc struct{ calls int }

func (f *fakeSrc) MostPopular(ctx context.Context, region, category string, max int) ([]youtube.Video, error) {
	f.calls++
	vs := []youtube.Video{vid("a", "x", 1000, "fall"), vid("b", "y", 1000, "fall")}
	if f.calls > 1 {
		vs = append(vs, vid("c", "z", 90000, "fall"))
	}
	return vs, nil
}

func TestTrackerMeasuresGrowth(t *testing.T) {
	tr := NewTracker(&fakeSrc{})
	tr.now = func() time.Time { return now }
	first, _ := tr.Snapshot(context.Background(), "US", "")
	second, _ := tr.Snapshot(context.Background(), "US", "")
	if first[0].Growth != 0 || second[0].Growth <= 0 {
		t.Fatalf("growth first=%v second=%v", first[0].Growth, second[0].Growth)
	}
}

func TestOneStoryDoesNotFillTheList(t *testing.T) {
	// Two channels upload the same trailer with the same tag set; a third
	// trend is unrelated.
	tags := []string{"Narnia", "Aslan", "The Magician's Nephew", "Netflix"}
	vs := []youtube.Video{
		vid("a", "NARNIA: The Magician's Nephew | Official Trailer", 900000, tags...),
		vid("b", "NARNIA: The Magician's Nephew | Official Trailer", 800000, tags...),
		vid("c", "Speedrun", 5000, "speedrun"),
		vid("d", "Speedrun 2", 5000, "speedrun"),
	}
	got := Score(vs, nil, now)
	if len(got) != 2 {
		t.Fatalf("want 2 grouped trends, got %+v", got)
	}
	if got[0].Term != "#Narnia" || len(got[0].Related) != 3 {
		t.Fatalf("lead %+v", got[0])
	}
	for _, r := range got[0].Related {
		if strings.ContainsAny(r, "' ") {
			t.Errorf("invalid hashtag %q", r)
		}
	}
	if got[1].Key != "speedrun" {
		t.Fatalf("second %+v", got[1])
	}
}

func TestTrackerRemembersRelatedHeat(t *testing.T) {
	src := &staticSrc{vs: []youtube.Video{vid("a", "x", 1000, "one", "two"), vid("b", "y", 1000, "one", "two")}}
	tr := NewTracker(src)
	tr.now = func() time.Time { return now }
	tr.Snapshot(context.Background(), "US", "")
	second, _ := tr.Snapshot(context.Background(), "US", "")
	if second[0].Growth != 0 {
		t.Fatalf("related tag should not look new on the next poll: %+v", second[0])
	}
}

type staticSrc struct{ vs []youtube.Video }

func (s *staticSrc) MostPopular(ctx context.Context, region, category string, max int) ([]youtube.Video, error) {
	return s.vs, nil
}
