// Package live follows the creator's own stream: go-live and end-of-stream
// posts, chat-surge detection, and clips sent to Discord for approval.
package live

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"strings"
	"time"

	"trendpilot/internal/usage"
)

type ChatMessage struct {
	At   time.Time
	User string
	Text string
}

// ChatAddr is Twitch's TLS IRC endpoint.
var ChatAddr = "irc.chat.twitch.tv:6697"

// tlsInsecure is only set by tests talking to a local fake server.
var tlsInsecure = false

// ReadChat joins a channel's chat anonymously (read-only, no sign-in) and
// sends each message to out until ctx ends, reconnecting with backoff.
func ReadChat(ctx context.Context, channel string, out chan<- ChatMessage) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := readChatOnce(ctx, strings.ToLower(channel), out)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		slog.Warn("chat disconnected; reconnecting", "channel", channel, "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 2*time.Minute)
	}
}

func readChatOnce(ctx context.Context, channel string, out chan<- ChatMessage) error {
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 15 * time.Second}, Config: &tls.Config{InsecureSkipVerify: tlsInsecure}}
	conn, err := d.DialContext(ctx, "tcp", ChatAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close() // unblock the read on shutdown
		case <-done:
		}
	}()

	nick := fmt.Sprintf("justinfan%d", 10000+rand.Intn(80000))
	fmt.Fprintf(conn, "CAP REQ :twitch.tv/tags\r\nNICK %s\r\nJOIN #%s\r\n", nick, channel)

	r := bufio.NewReaderSize(countingConn{conn}, 16<<10)
	for {
		// Twitch PINGs about every 5 minutes; silence for longer means a dead link.
		conn.SetReadDeadline(time.Now().Add(7 * time.Minute))
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "PING") {
			fmt.Fprintf(conn, "PONG%s\r\n", strings.TrimPrefix(line, "PING"))
			continue
		}
		if m, ok := ParsePrivmsg(line); ok {
			m.At = time.Now()
			select {
			case out <- m:
			default: // never block the socket on a slow consumer
			}
		}
	}
}

// countingConn adds chat traffic to TrendPilot's usage report.
type countingConn struct{ net.Conn }

func (c countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	usage.Count(int64(n), 0)
	return n, err
}

// ParsePrivmsg extracts the sender and text from a tagged IRC PRIVMSG.
func ParsePrivmsg(line string) (ChatMessage, bool) {
	tags := ""
	if strings.HasPrefix(line, "@") {
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			return ChatMessage{}, false
		}
		tags, line = line[1:sp], line[sp+1:]
	}
	// :nick!nick@nick.tmi.twitch.tv PRIVMSG #chan :text
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 4 || parts[1] != "PRIVMSG" || !strings.HasPrefix(parts[3], ":") {
		return ChatMessage{}, false
	}
	user := strings.TrimPrefix(parts[0], ":")
	if i := strings.IndexByte(user, '!'); i >= 0 {
		user = user[:i]
	}
	for _, kv := range strings.Split(tags, ";") {
		if v, ok := strings.CutPrefix(kv, "display-name="); ok && v != "" {
			user = v
		}
	}
	return ChatMessage{User: user, Text: parts[3][1:]}, true
}
