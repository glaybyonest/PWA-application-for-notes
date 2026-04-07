package push

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/SherClockHolmes/webpush-go"
)

func TestNormalizeKeyPairSwappedFiles(t *testing.T) {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("failed to generate VAPID keys: %v", err)
	}

	normalizedPublic, normalizedPrivate, swapped, err := normalizeKeyPair(privateKey, publicKey)
	if err != nil {
		t.Fatalf("normalizeKeyPair returned error: %v", err)
	}
	if !swapped {
		t.Fatal("expected swapped=true for reversed key files")
	}
	if normalizedPublic != publicKey {
		t.Fatal("expected public key to be normalized into public position")
	}
	if normalizedPrivate != privateKey {
		t.Fatal("expected private key to be normalized into private position")
	}
}

func TestNormalizeKeyPairAlreadyCorrect(t *testing.T) {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("failed to generate VAPID keys: %v", err)
	}

	normalizedPublic, normalizedPrivate, swapped, err := normalizeKeyPair(publicKey, privateKey)
	if err != nil {
		t.Fatalf("normalizeKeyPair returned error: %v", err)
	}
	if swapped {
		t.Fatal("expected swapped=false for correctly ordered key files")
	}
	if normalizedPublic != publicKey || normalizedPrivate != privateKey {
		t.Fatal("expected keys to remain unchanged")
	}
}

func TestHandlePushResponseReturnsErrorForForbidden(t *testing.T) {
	service := &Service{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:  NewStore(),
	}

	subscription := &webpush.Subscription{Endpoint: "https://push.example/subscription/1"}
	response := &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader("forbidden")),
	}

	err := service.handlePushResponse(subscription, response)
	if err == nil {
		t.Fatal("expected non-2xx push response to return an error")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected HTTP 403 in error, got %v", err)
	}
}

func TestHandlePushResponseRemovesGoneSubscription(t *testing.T) {
	service := &Service{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:  NewStore(),
	}

	subscription := &webpush.Subscription{Endpoint: "https://push.example/subscription/2"}
	service.store.Add(subscription)

	response := &http.Response{
		StatusCode: http.StatusGone,
		Body:       io.NopCloser(strings.NewReader("expired")),
	}

	err := service.handlePushResponse(subscription, response)
	if err == nil {
		t.Fatal("expected gone subscription to return an error")
	}
	if service.store.Count() != 0 {
		t.Fatalf("expected gone subscription to be removed, got count %d", service.store.Count())
	}
}
