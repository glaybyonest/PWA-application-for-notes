package httpserver

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"frontendandbackend1317/internal/config"
	"frontendandbackend1317/internal/realtime"
	"frontendandbackend1317/internal/reminders"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/gorilla/websocket"
)

type stubWebSocket struct{}

func (stubWebSocket) ServeWS(http.ResponseWriter, *http.Request) {}

type stubPush struct {
	publicKey string
	available bool
	added     []*webpush.Subscription
	removed   []string
}

type stubReminder struct {
	scheduled []reminders.Reminder
	snoozed   []string
}

func (s *stubReminder) Schedule(reminder reminders.Reminder) error {
	s.scheduled = append(s.scheduled, reminder)
	return nil
}

func (s *stubReminder) Snooze(reminderID string, _ time.Duration) (reminders.Reminder, error) {
	s.snoozed = append(s.snoozed, reminderID)
	return reminders.Reminder{ID: reminderID, Text: "Snoozed", ReminderTime: time.Now().Add(5 * time.Minute).UnixMilli()}, nil
}

func (s *stubPush) PublicKey() string {
	return s.publicKey
}

func (s *stubPush) SupportsPush() bool {
	return s.available
}

func (s *stubPush) AddSubscription(subscription *webpush.Subscription) error {
	s.added = append(s.added, subscription)
	return nil
}

func (s *stubPush) RemoveSubscription(endpoint string) bool {
	s.removed = append(s.removed, endpoint)
	return true
}

func TestConfigEndpoint(t *testing.T) {
	handler := newTestHandler(t)

	request := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", response.Code)
	}

	var payload configResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if payload.WSPath != "/ws" {
		t.Fatalf("unexpected ws path: %q", payload.WSPath)
	}
	if payload.VAPIDPublicKey != "public-key" {
		t.Fatalf("unexpected public key: %q", payload.VAPIDPublicKey)
	}
	if !payload.PushAvailable {
		t.Fatal("expected push to be reported as available")
	}
}

func TestSubscribeEndpoint(t *testing.T) {
	handler, push := newTestHandlerWithPush(t)

	body := strings.NewReader(`{"endpoint":"https://push.example/subscription/1","expirationTime":null,"keys":{"p256dh":"abc","auth":"xyz"}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/push/subscribe", body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", response.Code)
	}
	if len(push.added) != 1 {
		t.Fatalf("expected one stored subscription, got %d", len(push.added))
	}
	if push.added[0].Endpoint != "https://push.example/subscription/1" {
		t.Fatalf("unexpected endpoint: %q", push.added[0].Endpoint)
	}
}

func TestUnsubscribeEndpoint(t *testing.T) {
	handler, push := newTestHandlerWithPush(t)

	request := httptest.NewRequest(http.MethodPost, "/api/push/unsubscribe", strings.NewReader(`{"endpoint":"https://push.example/subscription/1"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", response.Code)
	}
	if len(push.removed) != 1 {
		t.Fatalf("expected one removed subscription, got %d", len(push.removed))
	}
}

func TestCreateReminderEndpoint(t *testing.T) {
	handler, _, reminder := newTestHandlerWithDependencies(t)

	request := httptest.NewRequest(http.MethodPost, "/api/reminders", strings.NewReader(`{"id":"rem-1","text":"Submit coursework","reminderTime":1893456000000}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", response.Code)
	}
	if len(reminder.scheduled) != 1 {
		t.Fatalf("expected one scheduled reminder, got %d", len(reminder.scheduled))
	}
	if reminder.scheduled[0].ID != "rem-1" {
		t.Fatalf("unexpected reminder id: %q", reminder.scheduled[0].ID)
	}
}

func TestSnoozeReminderEndpoint(t *testing.T) {
	handler, _, reminder := newTestHandlerWithDependencies(t)

	request := httptest.NewRequest(http.MethodPost, "/api/reminders/snooze", strings.NewReader(`{"reminderId":"rem-1"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", response.Code)
	}
	if len(reminder.snoozed) != 1 || reminder.snoozed[0] != "rem-1" {
		t.Fatalf("expected reminder rem-1 to be snoozed, got %+v", reminder.snoozed)
	}
}

func TestWebSocketEndpointUpgrades(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html><title>test</title>"), 0o644); err != nil {
		t.Fatalf("failed to create temp index: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := realtime.NewHub(logger, nil)
	t.Cleanup(hub.Shutdown)

	handler, err := NewHandler(config.Config{WebDir: webDir}, logger, hub, &stubPush{}, &stubReminder{})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	server := httptest.NewTLSServer(handler)
	defer server.Close()

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	wsURL := "wss" + strings.TrimPrefix(server.URL, "https") + "/ws"

	conn, response, err := dialer.Dial(wsURL, nil)
	if err != nil {
		body := ""
		if response != nil && response.Body != nil {
			payload, readErr := io.ReadAll(response.Body)
			if readErr == nil {
				body = string(payload)
			}
		}
		t.Fatalf("failed to upgrade websocket: %v (status=%v body=%q)", err, responseStatus(response), body)
	}
	defer conn.Close()
}

func responseStatus(response *http.Response) int {
	if response == nil {
		return 0
	}
	return response.StatusCode
}

func newTestHandler(t *testing.T) http.Handler {
	handler, _, _ := newTestHandlerWithDependencies(t)
	return handler
}

func newTestHandlerWithPush(t *testing.T) (http.Handler, *stubPush) {
	handler, push, _ := newTestHandlerWithDependencies(t)
	return handler, push
}

func newTestHandlerWithDependencies(t *testing.T) (http.Handler, *stubPush, *stubReminder) {
	t.Helper()

	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html><title>test</title>"), 0o644); err != nil {
		t.Fatalf("failed to create temp index: %v", err)
	}

	push := &stubPush{
		publicKey: "public-key",
		available: true,
	}
	reminder := &stubReminder{}

	handler, err := NewHandler(config.Config{WebDir: webDir}, slog.New(slog.NewTextHandler(io.Discard, nil)), stubWebSocket{}, push, reminder)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	return handler, push, reminder
}
