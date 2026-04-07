package push

import (
	"testing"

	"github.com/SherClockHolmes/webpush-go"
)

func TestStoreDeduplicatesByEndpoint(t *testing.T) {
	store := NewStore()

	store.Add(&webpush.Subscription{
		Endpoint: "https://example.com/subscription/1",
		Keys: webpush.Keys{
			Auth:   "first-auth",
			P256dh: "first-key",
		},
	})
	store.Add(&webpush.Subscription{
		Endpoint: "https://example.com/subscription/1",
		Keys: webpush.Keys{
			Auth:   "updated-auth",
			P256dh: "updated-key",
		},
	})

	if got := store.Count(); got != 1 {
		t.Fatalf("expected one subscription after deduplication, got %d", got)
	}

	items := store.List()
	if len(items) != 1 {
		t.Fatalf("expected one subscription in list, got %d", len(items))
	}
	if items[0].Keys.Auth != "updated-auth" {
		t.Fatalf("expected latest subscription to be stored, got %q", items[0].Keys.Auth)
	}
}

func TestStoreRemoveByEndpoint(t *testing.T) {
	store := NewStore()
	store.Add(&webpush.Subscription{Endpoint: "https://example.com/subscription/1"})

	if removed := store.Remove("https://example.com/subscription/1"); !removed {
		t.Fatal("expected remove to return true for existing endpoint")
	}
	if removed := store.Remove("https://example.com/subscription/1"); removed {
		t.Fatal("expected remove to return false for missing endpoint")
	}
}
