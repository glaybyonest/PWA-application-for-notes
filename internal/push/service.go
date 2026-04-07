package push

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"frontendandbackend1317/internal/config"
	"frontendandbackend1317/internal/realtime"
	"frontendandbackend1317/internal/reminders"

	"github.com/SherClockHolmes/webpush-go"
)

// SubscriptionManager is the interface consumed by the HTTP layer.
type SubscriptionManager interface {
	PublicKey() string
	SupportsPush() bool
	AddSubscription(*webpush.Subscription) error
	RemoveSubscription(string) bool
}

// Notifier can broadcast push notifications for newly added tasks.
type Notifier interface {
	NotifyTask(realtime.TaskPayload) error
}

// Service handles VAPID setup, subscription management, and outbound web push.
type Service struct {
	logger     *slog.Logger
	store      *Store
	publicKey  string
	privateKey string
	subject    string
}

// NewService creates a push service and generates missing VAPID keys on first run.
func NewService(cfg config.Config, logger *slog.Logger) (*Service, error) {
	if logger == nil {
		logger = slog.Default()
	}

	publicKey, privateKey, generated, err := ensureKeys(cfg.VAPIDPublicKeyPath, cfg.VAPIDPrivateKeyPath)
	if err != nil {
		return nil, err
	}

	if generated {
		logger.Info("generated new VAPID key pair", "publicKey", publicKey, "publicKeyPath", cfg.VAPIDPublicKeyPath)
	}

	return &Service{
		logger:     logger,
		store:      NewStore(),
		publicKey:  publicKey,
		privateKey: privateKey,
		subject:    cfg.VAPIDSubject,
	}, nil
}

func ensureKeys(publicPath, privatePath string) (string, string, bool, error) {
	publicKey, publicErr := readKey(publicPath)
	privateKey, privateErr := readKey(privatePath)
	if publicErr == nil && privateErr == nil {
		normalizedPublic, normalizedPrivate, swapped, err := normalizeKeyPair(publicKey, privateKey)
		if err == nil {
			if swapped {
				if err := writeKey(publicPath, normalizedPublic); err != nil {
					return "", "", false, err
				}
				if err := writeKey(privatePath, normalizedPrivate); err != nil {
					return "", "", false, err
				}
			}
			return normalizedPublic, normalizedPrivate, swapped, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(publicPath), 0o755); err != nil {
		return "", "", false, fmt.Errorf("create VAPID key directory: %w", err)
	}

	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", false, fmt.Errorf("generate VAPID keys: %w", err)
	}

	if err := writeKey(publicPath, publicKey); err != nil {
		return "", "", false, err
	}
	if err := writeKey(privatePath, privateKey); err != nil {
		return "", "", false, err
	}

	return publicKey, privateKey, true, nil
}

func normalizeKeyPair(publicKey, privateKey string) (normalizedPublic string, normalizedPrivate string, swapped bool, err error) {
	publicKind, err := detectVAPIDKeyKind(publicKey)
	if err != nil {
		return "", "", false, err
	}
	privateKind, err := detectVAPIDKeyKind(privateKey)
	if err != nil {
		return "", "", false, err
	}

	if publicKind == "public" && privateKind == "private" {
		return publicKey, privateKey, false, nil
	}
	if publicKind == "private" && privateKind == "public" {
		return privateKey, publicKey, true, nil
	}

	return "", "", false, fmt.Errorf("invalid VAPID key pair: public file looks like %s, private file looks like %s", publicKind, privateKind)
}

func detectVAPIDKeyKind(value string) (string, error) {
	decoded, err := decodeBase64URL(value)
	if err != nil {
		return "", fmt.Errorf("decode VAPID key: %w", err)
	}

	switch len(decoded) {
	case 32:
		return "private", nil
	case 65:
		if decoded[0] != 0x04 {
			return "", fmt.Errorf("unexpected public key prefix: %d", decoded[0])
		}
		return "public", nil
	default:
		return "", fmt.Errorf("unexpected VAPID key length: %d", len(decoded))
	}
}

func decodeBase64URL(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	decoded, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err == nil {
		return decoded, nil
	}

	padding := strings.Repeat("=", (4-(len(trimmed)%4))%4)
	return base64.URLEncoding.DecodeString(trimmed + padding)
}

func readKey(path string) (string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(bytes)), nil
}

func writeKey(path, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create key directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		return fmt.Errorf("write key %s: %w", path, err)
	}

	return nil
}

// PublicKey returns the VAPID public key used by the browser.
func (s *Service) PublicKey() string {
	return s.publicKey
}

// SupportsPush reports whether push is available.
func (s *Service) SupportsPush() bool {
	return s.publicKey != "" && s.privateKey != ""
}

// AddSubscription inserts a subscription into the in-memory store.
func (s *Service) AddSubscription(subscription *webpush.Subscription) error {
	if subscription == nil {
		return errors.New("subscription payload is required")
	}
	if strings.TrimSpace(subscription.Endpoint) == "" {
		return errors.New("subscription endpoint is required")
	}

	s.store.Add(subscription)
	s.logger.Info("push subscription stored", "endpoint", subscription.Endpoint, "count", s.store.Count())
	return nil
}

// RemoveSubscription removes a subscription by endpoint.
func (s *Service) RemoveSubscription(endpoint string) bool {
	removed := s.store.Remove(strings.TrimSpace(endpoint))
	s.logger.Info("push subscription removed", "endpoint", endpoint, "removed", removed, "count", s.store.Count())
	return removed
}

// NotifyTask sends a push notification to all subscribed clients.
func (s *Service) NotifyTask(task realtime.TaskPayload) error {
	return s.sendPayload(map[string]any{
		"title": "Новая задача",
		"body":  notificationBody(task.Text, task.DateTime),
		"url":   "/",
	}, "task", task.ID)
}

// NotifyReminder sends a scheduled reminder push notification.
func (s *Service) NotifyReminder(reminder reminders.Reminder) error {
	return s.sendPayload(map[string]any{
		"title":      "⏰ Напоминание",
		"body":       strings.TrimSpace(reminder.Text),
		"url":        "/",
		"reminderId": reminder.ID,
	}, "reminder", reminder.ID)
}

func (s *Service) sendPayload(payload map[string]any, kind, referenceID string) error {
	if !s.SupportsPush() {
		return nil
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal push payload: %w", err)
	}

	subscriptions := s.store.List()
	if len(subscriptions) == 0 {
		s.logger.Warn("skipping push delivery because there are no active subscriptions", "kind", kind, "referenceID", referenceID)
		return nil
	}

	s.logger.Info("sending push notifications", "subscriptions", len(subscriptions), "kind", kind, "referenceID", referenceID)

	var errs []error
	for _, subscription := range subscriptions {
		resp, sendErr := webpush.SendNotification(payloadBytes, subscription, &webpush.Options{
			Subscriber:      s.subject,
			VAPIDPublicKey:  s.publicKey,
			VAPIDPrivateKey: s.privateKey,
			TTL:             30,
			Urgency:         webpush.UrgencyHigh,
		})
		if sendErr != nil {
			errs = append(errs, fmt.Errorf("push %s: %w", subscription.Endpoint, sendErr))
			continue
		}

		if responseErr := s.handlePushResponse(subscription, resp); responseErr != nil {
			errs = append(errs, responseErr)
		}
	}

	joinedErr := errors.Join(errs...)
	if joinedErr != nil {
		s.logger.Error("push delivery completed with errors", "kind", kind, "referenceID", referenceID, "error", joinedErr)
		return joinedErr
	}

	s.logger.Info("push delivery finished successfully", "subscriptions", len(subscriptions), "kind", kind, "referenceID", referenceID)
	return nil
}

func (s *Service) handlePushResponse(subscription *webpush.Subscription, resp *http.Response) error {
	if resp == nil {
		return errors.New("push endpoint returned nil response")
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	body := strings.TrimSpace(string(bodyBytes))

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		s.store.Remove(subscription.Endpoint)
		s.logger.Warn("removed invalid push subscription", "endpoint", subscription.Endpoint, "status", resp.StatusCode)
	}

	if body == "" {
		return fmt.Errorf("push %s returned HTTP %d", subscription.Endpoint, resp.StatusCode)
	}

	return fmt.Errorf("push %s returned HTTP %d: %s", subscription.Endpoint, resp.StatusCode, body)
}

func notificationBody(text, datetime string) string {
	text = strings.TrimSpace(text)
	if datetime == "" {
		return text
	}
	return fmt.Sprintf("%s (срок: %s)", text, datetime)
}
