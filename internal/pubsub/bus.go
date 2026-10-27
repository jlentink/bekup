// Package pubsub moves small change notifications between the parts of
// bekup serve, in-process and across instances (task 1.10).
//
//   - Bus is an in-process fan-out: Publish never blocks; a subscriber
//     whose buffer is full loses the message and is told so through its
//     Resync channel (it must then re-check whatever it watches).
//   - Listener is the LISTEN/NOTIFY bridge: one dedicated Postgres
//     connection that LISTENs on fixed channels and feeds every
//     notification into the Bus. It reconnects with exponential backoff and
//     jitter and signals Resync on every (re)connect, because notifications
//     sent while it was disconnected are lost.
//   - Publisher.PublishTx sends a notification with pg_notify inside the
//     caller's transaction (Postgres delivers it to every instance on
//     commit and drops it on rollback) and also publishes it on the local
//     Bus at once.
//
// Payloads carry identifiers only (D-050, plan "WS subscriptions"): never
// names, secrets or other content. Receivers treat a message as a hint and
// load the current state themselves (with their own authorization), so a
// duplicated, reordered, forged or lost message can delay but never widen
// what anyone sees. Session events (internal/auth) use it today; run
// events (task 3.7) and notifications (task 5.3b) will reuse it.
package pubsub

import (
	"errors"
	"fmt"
	"regexp"
	"sync"
)

// MaxPayloadBytes caps a payload (Postgres allows 8000 bytes; IDs need far
// less).
const MaxPayloadBytes = 256

// channelName is the shape of a channel name: bekup_ + lower-case words.
var channelName = regexp.MustCompile(`^bekup_[a-z0-9_]{1,48}$`)

// payloadChars are the only characters a payload may contain: identifiers
// (UUIDs, kinds) and separators.
var payloadChars = regexp.MustCompile(`^[A-Za-z0-9_:.\-]*$`)

// ErrInvalidMessage is returned for a channel or payload of the wrong
// shape.
var ErrInvalidMessage = errors.New("pubsub: invalid channel or payload")

// Validate checks a channel name and payload.
func Validate(channel, payload string) error {
	if !channelName.MatchString(channel) {
		return fmt.Errorf("%w: channel %q", ErrInvalidMessage, channel)
	}
	if len(payload) > MaxPayloadBytes || !payloadChars.MatchString(payload) {
		return fmt.Errorf("%w: payload on %s", ErrInvalidMessage, channel)
	}
	return nil
}

// Message is one notification.
type Message struct {
	Channel string
	Payload string
}

// Subscription receives the messages of one channel.
type Subscription struct {
	bus     *Bus
	channel string
	c       chan Message
	resync  chan struct{}
	once    sync.Once
}

// C delivers the messages in publish order (per publisher).
func (s *Subscription) C() <-chan Message { return s.c }

// Resync receives a value whenever messages may have been lost (buffer
// overflow, Listener reconnect). The receiver must re-check its state.
// Signals coalesce.
func (s *Subscription) Resync() <-chan struct{} { return s.resync }

// Close unsubscribes; safe to call more than once. C is not closed (a
// concurrent Publish may still be selecting on it).
func (s *Subscription) Close() {
	s.once.Do(func() { s.bus.remove(s) })
}

func (s *Subscription) signalResync() {
	select {
	case s.resync <- struct{}{}:
	default:
	}
}

// Bus is an in-process fan-out of messages by channel. The zero value is
// not usable; use NewBus.
type Bus struct {
	mu   sync.RWMutex
	subs map[string]map[*Subscription]struct{}
}

// NewBus returns an empty Bus.
func NewBus() *Bus {
	return &Bus{subs: map[string]map[*Subscription]struct{}{}}
}

// Subscribe registers a subscriber of channel with a buffer of buffer
// messages (at least 1).
func (b *Bus) Subscribe(channel string, buffer int) *Subscription {
	s := &Subscription{
		bus:     b,
		channel: channel,
		c:       make(chan Message, max(buffer, 1)),
		resync:  make(chan struct{}, 1),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.subs[channel]
	if m == nil {
		m = map[*Subscription]struct{}{}
		b.subs[channel] = m
	}
	m[s] = struct{}{}
	return s
}

func (b *Bus) remove(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if m := b.subs[s.channel]; m != nil {
		delete(m, s)
		if len(m) == 0 {
			delete(b.subs, s.channel)
		}
	}
}

// Publish delivers payload to every subscriber of channel without
// blocking; a subscriber with a full buffer gets a Resync signal instead.
// Invalid messages are dropped.
func (b *Bus) Publish(channel, payload string) {
	if Validate(channel, payload) != nil {
		return
	}
	msg := Message{Channel: channel, Payload: payload}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs[channel] {
		select {
		case s.c <- msg:
		default:
			s.signalResync()
		}
	}
}

// Resync signals every subscriber of channel that messages may have been
// lost.
func (b *Bus) Resync(channel string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs[channel] {
		s.signalResync()
	}
}

// Subscribers returns the number of subscribers of channel (tests,
// metrics).
func (b *Bus) Subscribers(channel string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[channel])
}
