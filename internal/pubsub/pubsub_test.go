package pubsub

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		channel, payload string
		ok               bool
	}{
		{"bekup_session_events", "revoked:3f1c8a8e-0000-4000-8000-000000000000:3f1c8a8e-0000-4000-8000-000000000001", true},
		{"bekup_x", "", true},
		{"session_events", "x", false},
		{"bekup_Session", "x", false},
		{"bekup_a;drop", "x", false},
		{"bekup_" + strings.Repeat("a", 49), "x", false},
		{"bekup_ok", "name with spaces", false},
		{"bekup_ok", `{"json":1}`, false},
		{"bekup_ok", strings.Repeat("a", MaxPayloadBytes+1), false},
		{"bekup_ok", strings.Repeat("a", MaxPayloadBytes), true},
	}
	for _, c := range cases {
		err := Validate(c.channel, c.payload)
		if (err == nil) != c.ok {
			t.Errorf("Validate(%q, %.20q) = %v, want ok=%v", c.channel, c.payload, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("error %v does not wrap ErrInvalidMessage", err)
		}
	}
}

func FuzzValidate(f *testing.F) {
	f.Add("bekup_session_events", "revoked:a:b")
	f.Fuzz(func(t *testing.T, channel, payload string) {
		if Validate(channel, payload) == nil {
			if len(payload) > MaxPayloadBytes || strings.ContainsAny(payload, " \"'{}\n;") {
				t.Fatalf("accepted payload %q", payload)
			}
		}
	})
}

func TestBusFanOutAndClose(t *testing.T) {
	b := NewBus()
	s1 := b.Subscribe("bekup_a", 4)
	s2 := b.Subscribe("bekup_a", 4)
	other := b.Subscribe("bekup_b", 4)
	b.Publish("bekup_a", "1")
	b.Publish("bekup_a", "bad payload") // dropped
	for _, s := range []*Subscription{s1, s2} {
		select {
		case m := <-s.C():
			if m.Channel != "bekup_a" || m.Payload != "1" {
				t.Fatalf("got %+v", m)
			}
		case <-time.After(time.Second):
			t.Fatal("no message")
		}
		select {
		case m := <-s.C():
			t.Fatalf("invalid message delivered: %+v", m)
		default:
		}
	}
	select {
	case m := <-other.C():
		t.Fatalf("message on the wrong channel: %+v", m)
	default:
	}
	s1.Close()
	s1.Close()
	if n := b.Subscribers("bekup_a"); n != 1 {
		t.Fatalf("subscribers %d", n)
	}
	b.Publish("bekup_a", "2")
	select {
	case <-s1.C():
		t.Fatal("closed subscription received a message")
	default:
	}
}

func TestBusOverflowSignalsResync(t *testing.T) {
	b := NewBus()
	s := b.Subscribe("bekup_a", 1)
	b.Publish("bekup_a", "1")
	b.Publish("bekup_a", "2") // does not fit, never blocks
	b.Publish("bekup_a", "3")
	select {
	case <-s.Resync():
	default:
		t.Fatal("no resync signal after overflow")
	}
	if m := <-s.C(); m.Payload != "1" {
		t.Fatalf("got %+v", m)
	}
	b.Resync("bekup_a")
	select {
	case <-s.Resync():
	default:
		t.Fatal("no resync signal")
	}
}

func TestJitter(t *testing.T) {
	for range 1000 {
		d := jitter(time.Second)
		if d < 500*time.Millisecond || d > time.Second {
			t.Fatalf("jitter %s out of [500ms, 1s]", d)
		}
	}
}

func TestListenerReconnectsAfterConnectFailures(t *testing.T) {
	calls := 0
	l, err := NewListener(ListenerOptions{
		Connect: func(context.Context) (*pgx.Conn, error) {
			calls++
			return nil, errors.New("connection refused")
		},
		Channels:   []Channel{SessionEvents},
		Bus:        NewBus(),
		MinBackoff: time.Millisecond,
		MaxBackoff: 4 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := l.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if calls < 5 {
		t.Fatalf("only %d connection attempts", calls)
	}
	if l.Connected() {
		t.Fatal("reported connected")
	}
}

func TestNewListenerValidates(t *testing.T) {
	connect := func(context.Context) (*pgx.Conn, error) { return nil, errors.New("x") }
	bad := []ListenerOptions{
		{},
		{Connect: connect, Bus: NewBus()},
		{Connect: connect, Bus: NewBus(), Channels: []Channel{{Name: "nope", Listen: SessionEvents.Listen}}},
		{Connect: connect, Bus: NewBus(), Channels: []Channel{{Name: "bekup_x"}}},
	}
	for i, o := range bad {
		if _, err := NewListener(o); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}
