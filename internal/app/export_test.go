package app

import "github.com/jlentink/bekup/internal/pubsub"

// SubscriptionsReady reports whether a's LISTEN connection is up and its
// WebSocket hub receives session events (tests poll it instead of
// sleeping).
func SubscriptionsReady(a *App) bool {
	return a.subs.listener.Connected() && a.subs.bus.Subscribers(pubsub.SessionEvents.Name) > 0
}
