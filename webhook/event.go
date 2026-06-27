package webhook

import (
	"encoding/json"
	"time"

	"github.com/rs/zerolog/log"
	"plutoploy/plutoploy-gh-bot/sse"
)

// Event is the normalized payload pushed to SSE subscribers. Owner doubles
// as the room key so events are delivered only to that user's clients.
type Event struct {
	Action     string    `json:"action"`
	Repo       string    `json:"repo"`
	Owner      string    `json:"owner"`
	RunID      int64     `json:"run_id,omitempty"`
	RunName    string    `json:"run_name,omitempty"`
	Status     string    `json:"status,omitempty"`
	Conclusion string    `json:"conclusion,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	SHA        string    `json:"sha,omitempty"`
	CommitMsg  string    `json:"commit_msg,omitempty"`
	Author     string    `json:"author,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// publish serializes the event and delivers it to the owner's room.
func publish(broker *sse.Broker, e Event) {
	data, err := json.Marshal(e)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal event")
		return
	}
	delivered := broker.Publish(e.Owner, data)
	log.Debug().
		Str("room", e.Owner).
		Str("action", e.Action).
		Int("delivered", delivered).
		Msg("Published event to room")
}
