package realtime

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestHubRegisterBroadcastAndUnregister(t *testing.T) {
	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	t.Cleanup(hub.Shutdown)

	client := &Client{
		hub:  hub,
		send: make(chan Envelope, 1),
	}

	hub.registerClient(client)
	waitForHubClients(t, hub, 1)

	expected := Envelope{Type: "taskAdded", Payload: TaskPayload{ID: "1", Text: "Test note"}}
	hub.Broadcast(expected)

	select {
	case message := <-client.send:
		if message.Type != expected.Type {
			t.Fatalf("unexpected message type: got %q want %q", message.Type, expected.Type)
		}
		if message.Payload.Text != expected.Payload.Text {
			t.Fatalf("unexpected payload text: got %q want %q", message.Payload.Text, expected.Payload.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broadcast")
	}

	hub.unregisterClient(client)
	waitForHubClients(t, hub, 0)
}

func waitForHubClients(t *testing.T, hub *Hub, expected int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(hub.clients) == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for hub client count %d, got %d", expected, len(hub.clients))
}
