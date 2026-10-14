package live

import (
	"bufio"
	"context"
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParsePrivmsg(t *testing.T) {
	m, ok := ParsePrivmsg(`@badge-info=;display-name=Ronni;emotes= :ronni!ronni@ronni.tmi.twitch.tv PRIVMSG #ronni :Kappa Keepo: Kappa`)
	if !ok || m.User != "Ronni" || m.Text != "Kappa Keepo: Kappa" {
		t.Fatalf("%+v %v", m, ok)
	}
	if _, ok := ParsePrivmsg(":tmi.twitch.tv 001 justinfan123 :Welcome"); ok {
		t.Fatal("non-PRIVMSG parsed")
	}
}

func TestSurge(t *testing.T) {
	d := NewSurgeDetector()
	t0 := time.Unix(1_800_000_000, 0)
	at := t0
	// Five minutes of calm chat: one message every 10 s.
	for at.Before(t0.Add(5 * time.Minute)) {
		if _, ok := d.Add(ChatMessage{At: at, Text: "hello"}); ok {
			t.Fatal("calm chat surged")
		}
		at = at.Add(10 * time.Second)
	}
	// Then everyone reacts within 10 s.
	var got Surge
	var fired int
	for i := 0; i < 12; i++ {
		if s, ok := d.Add(ChatMessage{At: at, User: "v", Text: "CLIP IT no way LUL"}); ok {
			got, fired = s, fired+1
		}
		at = at.Add(800 * time.Millisecond)
	}
	if fired != 1 || got.Rate < 6 || len(got.Sample) == 0 {
		t.Fatalf("fired=%d surge=%+v", fired, got)
	}
}

// TestReadChat runs the IRC client against a fake TLS server.
func TestReadChat(t *testing.T) {
	srv := httptest.NewUnstartedServer(nil)
	srv.StartTLS() // only for its certificate
	srv.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srv.TLS)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "JOIN #creator") {
				conn.Write([]byte("PING :tmi.twitch.tv\r\n"))
			}
			if strings.HasPrefix(line, "PONG :tmi.twitch.tv") {
				conn.Write([]byte("@display-name=Fan :fan!fan@fan.tmi.twitch.tv PRIVMSG #creator :hi\r\n"))
			}
		}
	}()

	old := ChatAddr
	ChatAddr = ln.Addr().String()
	defer func() { ChatAddr = old }()
	insecure := tlsInsecure
	tlsInsecure = true
	defer func() { tlsInsecure = insecure }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan ChatMessage, 1)
	go ReadChat(ctx, "Creator", out)
	select {
	case m := <-out:
		if m.User != "Fan" || m.Text != "hi" {
			t.Fatalf("%+v", m)
		}
	case <-ctx.Done():
		t.Fatal("no message")
	}
}
