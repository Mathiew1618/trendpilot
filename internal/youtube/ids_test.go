package youtube

import "testing"

func TestVideoID(t *testing.T) {
	const want = "dQw4w9WgXcQ"
	for _, in := range []string{
		want,
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10s",
		"youtube.com/watch?v=dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?si=abc",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ",
		"https://m.youtube.com/live/dQw4w9WgXcQ",
	} {
		if got, ok := VideoID(in); !ok || got != want {
			t.Errorf("VideoID(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "hello", "https://example.com/watch?v=dQw4w9WgXcQ", "https://youtube.com/channel/UCabc"} {
		if _, ok := VideoID(in); ok {
			t.Errorf("VideoID(%q) should fail", in)
		}
	}
}
