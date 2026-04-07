package realtime

import "strings"

// TaskPayload is the JSON payload exchanged between clients and server.
type TaskPayload struct {
	ID             string `json:"id"`
	Text           string `json:"text"`
	DateTime       string `json:"datetime"`
	CreatedAt      string `json:"createdAt"`
	OriginClientID string `json:"originClientId"`
}

// Envelope preserves the task event semantics from the original Socket.IO example.
type Envelope struct {
	Type    string      `json:"type"`
	Payload TaskPayload `json:"payload"`
}

// Valid returns true when the payload contains the minimum required fields.
func (p TaskPayload) Valid() bool {
	return strings.TrimSpace(p.Text) != ""
}
