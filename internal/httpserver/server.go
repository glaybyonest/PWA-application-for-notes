package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	pathpkg "path"
	"strings"
	"time"

	"frontendandbackend1317/internal/config"
	"frontendandbackend1317/internal/realtime"
	"frontendandbackend1317/internal/reminders"

	"github.com/SherClockHolmes/webpush-go"
)

type pushManager interface {
	PublicKey() string
	SupportsPush() bool
	AddSubscription(*webpush.Subscription) error
	RemoveSubscription(string) bool
}

type reminderManager interface {
	Schedule(reminders.Reminder) error
	Snooze(string, time.Duration) (reminders.Reminder, error)
}

// NewHandler wires API endpoints, websocket endpoint, and static frontend assets.
func NewHandler(cfg config.Config, logger *slog.Logger, ws realtime.WebSocketHandler, push pushManager, reminder reminderManager) (http.Handler, error) {
	if logger == nil {
		logger = slog.Default()
	}

	info, err := os.Stat(cfg.WebDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", cfg.WebDir)
	}

	app := &application{
		config:   cfg,
		logger:   logger,
		ws:       ws,
		push:     push,
		reminder: reminder,
	}

	fileServer := http.FileServer(http.Dir(cfg.WebDir))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", app.handleConfig)
	mux.HandleFunc("/api/push/subscribe", app.handleSubscribe)
	mux.HandleFunc("/api/push/unsubscribe", app.handleUnsubscribe)
	mux.HandleFunc("/api/reminders", app.handleCreateReminder)
	mux.HandleFunc("/api/reminders/snooze", app.handleSnoozeReminder)
	mux.HandleFunc("/ws", app.handleWS)
	mux.Handle("/", fileServer)

	return app.loggingMiddleware(mux), nil
}

type application struct {
	config   config.Config
	logger   *slog.Logger
	ws       realtime.WebSocketHandler
	push     pushManager
	reminder reminderManager
}

type configResponse struct {
	VAPIDPublicKey string `json:"vapidPublicKey"`
	PushAvailable  bool   `json:"pushAvailable"`
	WSPath         string `json:"wsPath"`
}

type endpointRequest struct {
	Endpoint string `json:"endpoint"`
}

type reminderRequest struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	ReminderTime int64  `json:"reminderTime"`
}

type snoozeRequest struct {
	ReminderID string `json:"reminderId"`
}

func (a *application) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	writeJSON(w, http.StatusOK, configResponse{
		VAPIDPublicKey: a.push.PublicKey(),
		PushAvailable:  a.push.SupportsPush(),
		WSPath:         "/ws",
	})
}

func (a *application) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	var subscription webpush.Subscription
	if err := decodeJSON(r.Body, &subscription); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(subscription.Endpoint) == "" {
		http.Error(w, "subscription endpoint is required", http.StatusBadRequest)
		return
	}

	if err := a.push.AddSubscription(&subscription); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "subscribed",
		"endpoint":    subscription.Endpoint,
		"storedInMem": true,
	})
}

func (a *application) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	var request endpointRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(request.Endpoint) == "" {
		http.Error(w, "endpoint is required", http.StatusBadRequest)
		return
	}

	removed := a.push.RemoveSubscription(request.Endpoint)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "unsubscribed",
		"endpoint": request.Endpoint,
		"removed":  removed,
	})
}

func (a *application) handleWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	a.ws.ServeWS(w, r)
}

func (a *application) handleCreateReminder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	if a.reminder == nil {
		http.Error(w, "reminder manager is unavailable", http.StatusServiceUnavailable)
		return
	}

	var request reminderRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reminder := reminders.Reminder{
		ID:           strings.TrimSpace(request.ID),
		Text:         strings.TrimSpace(request.Text),
		ReminderTime: request.ReminderTime,
	}

	if err := a.reminder.Schedule(reminder); err != nil {
		if errors.Is(err, reminders.ErrReminderPastTime) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "scheduled",
		"reminderId":   reminder.ID,
		"reminderTime": reminder.ReminderTime,
	})
}

func (a *application) handleSnoozeReminder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	if a.reminder == nil {
		http.Error(w, "reminder manager is unavailable", http.StatusServiceUnavailable)
		return
	}

	var request snoozeRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	updated, err := a.reminder.Snooze(strings.TrimSpace(request.ReminderID), reminders.SnoozeDuration)
	if err != nil {
		if errors.Is(err, reminders.ErrReminderNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "snoozed",
		"reminderId":   updated.ID,
		"reminderTime": updated.ReminderTime,
	})
}

func (a *application) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		next.ServeHTTP(recorder, r)

		a.logger.Info("http request",
			"method", r.Method,
			"path", cleanRequestPath(r.URL.Path),
			"status", recorder.status,
			"duration", time.Since(started).String(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func decodeJSON(reader io.Reader, dst any) error {
	decoder := json.NewDecoder(reader)

	if err := decoder.Decode(dst); err != nil {
		return err
	}

	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func cleanRequestPath(path string) string {
	cleaned := pathpkg.Clean(path)
	if cleaned == "." {
		return "/"
	}
	return cleaned
}
