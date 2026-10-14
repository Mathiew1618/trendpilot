// Package usage measures what TrendPilot costs the machine it runs on
// (network bytes, CPU time, memory) and keeps that cost low, so streamers
// can see it never competes with OBS or their game.
package usage

import (
	"net/http"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync/atomic"
	"time"
)

var (
	sent, received atomic.Int64
	requests       atomic.Int64
	started        = time.Now()
)

// Transport counts bytes on every outgoing API call. Install it with
// http.DefaultTransport = usage.Transport(http.DefaultTransport).
func Transport(next http.RoundTripper) http.RoundTripper {
	return roundTripper{next}
}

type roundTripper struct{ next http.RoundTripper }

func (t roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	requests.Add(1)
	if req.ContentLength > 0 {
		sent.Add(req.ContentLength)
	}
	sent.Add(int64(len(req.URL.String())) + 300) // request line + typical headers
	resp, err := t.next.RoundTrip(req)
	if err == nil && resp.Body != nil {
		resp.Body = &countingBody{resp.Body}
	}
	return resp, err
}

type countingBody struct {
	r interface {
		Read([]byte) (int, error)
		Close() error
	}
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	received.Add(int64(n))
	return n, err
}

func (b *countingBody) Close() error { return b.r.Close() }

// Count records traffic that doesn't go through HTTP (Twitch chat).
func Count(down, up int64) {
	received.Add(down)
	sent.Add(up)
}

// Limit caps the process: at most cpus OS threads running Go code at once
// and a soft memory ceiling. It also lowers the process priority where the
// OS allows it.
func Limit(cpus int, memMiB int64) {
	if cpus > 0 {
		runtime.GOMAXPROCS(cpus)
	}
	if memMiB > 0 {
		debug.SetMemoryLimit(memMiB << 20)
	}
	lowerPriority()
}

type Report struct {
	Uptime        string  `json:"uptime"`
	Requests      int64   `json:"api_requests"`
	DownloadedMB  float64 `json:"downloaded_mb"`
	UploadedMB    float64 `json:"uploaded_mb"`
	AvgKbps       float64 `json:"avg_kbps"`          // both directions, averaged over uptime
	MBPerHour     float64 `json:"mb_per_hour"`       // both directions
	CPUPercent    float64 `json:"cpu_percent"`       // of one core, averaged over uptime
	MemoryMB      float64 `json:"memory_mb"`         // Go heap + runtime, approx. resident
	CPUCores      int     `json:"cpu_cores_allowed"` // GOMAXPROCS
	LowPriority   bool    `json:"low_priority"`
	MemoryLimitMB int64   `json:"memory_limit_mb"`
}

func Snapshot() Report {
	up := time.Since(started)
	samples := []metrics.Sample{
		{Name: "/cpu/classes/total:cpu-seconds"},
		{Name: "/cpu/classes/idle:cpu-seconds"},
		{Name: "/memory/classes/total:bytes"},
	}
	metrics.Read(samples)
	cpu := samples[0].Value.Float64() - samples[1].Value.Float64()
	mem := float64(samples[2].Value.Uint64())

	down, upB := float64(received.Load()), float64(sent.Load())
	hours := up.Hours()
	r := Report{
		Uptime:        up.Round(time.Second).String(),
		Requests:      requests.Load(),
		DownloadedMB:  round(down / 1e6),
		UploadedMB:    round(upB / 1e6),
		MemoryMB:      round(mem / (1 << 20)),
		CPUCores:      runtime.GOMAXPROCS(0),
		LowPriority:   lowered,
		MemoryLimitMB: debug.SetMemoryLimit(-1) >> 20,
	}
	if s := up.Seconds(); s > 0 {
		r.AvgKbps = round((down + upB) * 8 / 1000 / s)
		// The runtime counts CPU time across all Ps, so this is "% of one core".
		r.CPUPercent = round(100 * cpu / s)
	}
	if hours > 0 {
		r.MBPerHour = round((down + upB) / 1e6 / hours)
	}
	return r
}

func round(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
