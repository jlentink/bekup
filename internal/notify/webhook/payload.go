package webhook

import (
	"encoding/json"
	"fmt"
	"time"
)

// EventTypeTest is the event type of a "send test" delivery.
const EventTypeTest = "webhook.test"

// Envelope is the JSON body of every delivery. Data holds the event's
// minimal fields (job name, status, time, link; never secrets or database
// contents), built by the notification core (task 5.3).
type Envelope struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Data      any       `json:"data"`
}

// BuildPayload marshals an Envelope after checking the ID and type with
// the same rules as Send.
func BuildPayload(eventID, eventType string, createdAt time.Time, data any) ([]byte, error) {
	if err := checkEventID(eventID); err != nil {
		return nil, err
	}
	if err := checkEventType(eventType); err != nil {
		return nil, err
	}
	if data == nil {
		data = struct{}{}
	}
	b, err := json.Marshal(Envelope{ID: eventID, Type: eventType, CreatedAt: createdAt.UTC().Truncate(time.Second), Data: data})
	if err != nil {
		return nil, fmt.Errorf("webhook: marshal payload: %w", err)
	}
	if len(b) > MaxPayloadBytes {
		return nil, ErrPayloadInvalid
	}
	return b, nil
}

// TestPayload is the body of a "send test" delivery.
func TestPayload(eventID string, now time.Time) ([]byte, error) {
	return BuildPayload(eventID, EventTypeTest, now, map[string]string{
		"message": "This is a test delivery from bekup.",
	})
}
