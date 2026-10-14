package discord

import (
	"testing"
	"time"
)

func TestParseZone(t *testing.T) {
	for in, want := range map[string]string{"PST": "America/Los_Angeles", " uk ": "Europe/London", "Asia/Tokyo": "Asia/Tokyo", "utc": "UTC"} {
		loc, err := ParseZone(in)
		if err != nil || loc.String() != want {
			t.Errorf("ParseZone(%q) = %v, %v", in, loc, err)
		}
	}
	for _, bad := range []string{"", "local", "Mars/Olympus"} {
		if _, err := ParseZone(bad); err == nil {
			t.Errorf("ParseZone(%q) should fail", bad)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(20*time.Second, 2)
	now := time.Date(2026, 10, 14, 23, 0, 0, 0, time.UTC)
	if ok, _ := l.Allow("g", "u1", now); !ok {
		t.Fatal("first call blocked")
	}
	if ok, why := l.Allow("g", "u1", now.Add(5*time.Second)); ok || why == "" {
		t.Fatal("cooldown not applied")
	}
	if ok, _ := l.Allow("g", "u2", now.Add(5*time.Second)); !ok {
		t.Fatal("another user should be allowed")
	}
	if ok, _ := l.Allow("g", "u3", now.Add(6*time.Second)); ok {
		t.Fatal("daily server cap not applied")
	}
	if ok, _ := l.Allow("other", "u3", now.Add(7*time.Second)); !ok {
		t.Fatal("caps are per server")
	}
	if ok, _ := l.Allow("g", "u3", now.Add(2*time.Hour)); !ok {
		t.Fatal("cap should reset the next UTC day")
	}
}
