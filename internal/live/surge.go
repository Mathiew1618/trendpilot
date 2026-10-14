package live

import (
	"strings"
	"time"
)

// Surge is a burst of chat activity well above the stream's normal pace,
// usually the moment worth clipping.
type Surge struct {
	At       time.Time     `json:"at"`
	Rate     float64       `json:"rate"`     // weighted messages in the window
	Baseline float64       `json:"baseline"` // usual weighted messages per window
	Sample   []ChatMessage `json:"-"`        // recent messages, for the AI title
}

// SurgeDetector compares a short window of chat with a slow moving
// average of the same window size. Hype words ("clip", "LUL", "no way",
// "?????") count extra so a 10-viewer stream's big moment still registers.
type SurgeDetector struct {
	Window   time.Duration // default 20s
	Factor   float64       // surge = rate ≥ Factor × baseline (default 3)
	MinRate  float64       // and rate ≥ MinRate (default 6)
	Cooldown time.Duration // default 3m
	Warmup   time.Duration // baseline needs this much chat history (default 2m)

	msgs      []weighted
	baseline  float64
	baseSince time.Time
	lastFold  time.Time
	lastSurge time.Time
}

type weighted struct {
	ChatMessage
	w float64
}

func NewSurgeDetector() *SurgeDetector {
	return &SurgeDetector{Window: 20 * time.Second, Factor: 3, MinRate: 6, Cooldown: 3 * time.Minute, Warmup: 2 * time.Minute}
}

var hype = []string{"clip", "lul", "kekw", "omegalul", "pog", "no way", "noway", "wtf", "lmao", "holy", "insane", "???", "!!!", "gg", "w "}

func weight(text string) float64 {
	t := strings.ToLower(text) + " "
	w := 1.0
	for _, h := range hype {
		if strings.Contains(t, h) {
			w += 0.5
		}
	}
	return min(w, 3)
}

// Add feeds one message. It returns a Surge when this message tips the
// window over the threshold.
func (d *SurgeDetector) Add(m ChatMessage) (Surge, bool) {
	if d.baseSince.IsZero() {
		d.baseSince, d.lastFold = m.At, m.At
	}
	d.msgs = append(d.msgs, weighted{m, weight(m.Text)})

	// Fold finished windows into the baseline (EWMA over ~10 windows).
	for m.At.Sub(d.lastFold) >= d.Window {
		end := d.lastFold.Add(d.Window)
		d.baseline = 0.9*d.baseline + 0.1*d.sum(d.lastFold, end)
		d.lastFold = end
	}
	// Keep two windows of messages: the live one and the sample before it.
	cut := m.At.Add(-2 * d.Window)
	i := 0
	for i < len(d.msgs) && d.msgs[i].At.Before(cut) {
		i++
	}
	d.msgs = d.msgs[i:]

	rate := d.sum(m.At.Add(-d.Window), m.At.Add(time.Nanosecond))
	warm := m.At.Sub(d.baseSince) >= d.Warmup
	cooled := d.lastSurge.IsZero() || m.At.Sub(d.lastSurge) >= d.Cooldown
	if !warm || !cooled || rate < d.MinRate || rate < d.Factor*d.baseline {
		return Surge{}, false
	}
	d.lastSurge = m.At
	sample := make([]ChatMessage, 0, len(d.msgs))
	for _, x := range d.msgs[max(0, len(d.msgs)-25):] {
		sample = append(sample, x.ChatMessage)
	}
	return Surge{At: m.At, Rate: rate, Baseline: d.baseline, Sample: sample}, true
}

func (d *SurgeDetector) sum(from, to time.Time) float64 {
	s := 0.0
	for _, x := range d.msgs {
		if !x.At.Before(from) && x.At.Before(to) {
			s += x.w
		}
	}
	return s
}
