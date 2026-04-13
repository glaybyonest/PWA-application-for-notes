package reminders

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type testNotifier struct {
	mu        sync.Mutex
	delivered []Reminder
	ch        chan Reminder
}

func newTestNotifier() *testNotifier {
	return &testNotifier{ch: make(chan Reminder, 8)}
}

func (n *testNotifier) NotifyReminder(reminder Reminder) error {
	n.mu.Lock()
	n.delivered = append(n.delivered, reminder)
	n.mu.Unlock()
	n.ch <- reminder
	return nil
}

func TestManagerScheduleFiresReminder(t *testing.T) {
	notifier := newTestNotifier()
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), notifier)
	t.Cleanup(manager.Shutdown)

	reminder := Reminder{
		ID:           "reminder-1",
		Text:         "Finish practice 17",
		ReminderTime: time.Now().Add(80 * time.Millisecond).UnixMilli(),
	}

	if err := manager.Schedule(reminder); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}

	select {
	case delivered := <-notifier.ch:
		if delivered.ID != reminder.ID {
			t.Fatalf("unexpected reminder id: got %q want %q", delivered.ID, reminder.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reminder delivery")
	}
}

func TestManagerSnoozeReschedulesReminder(t *testing.T) {
	notifier := newTestNotifier()
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), notifier)
	t.Cleanup(manager.Shutdown)

	reminder := Reminder{
		ID:           "reminder-2",
		Text:         "Snooze me",
		ReminderTime: time.Now().Add(150 * time.Millisecond).UnixMilli(),
	}

	if err := manager.Schedule(reminder); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}

	time.Sleep(30 * time.Millisecond)

	updated, err := manager.Snooze(reminder.ID, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("Snooze returned error: %v", err)
	}
	if updated.ReminderTime <= reminder.ReminderTime {
		t.Fatal("expected snoozed reminder time to be moved forward")
	}

	select {
	case delivered := <-notifier.ch:
		if delivered.ID != reminder.ID {
			t.Fatalf("unexpected reminder id after snooze: got %q want %q", delivered.ID, reminder.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snoozed reminder delivery")
	}
}

func TestManagerSnoozeKeepsFutureReminderInFuture(t *testing.T) {
	notifier := newTestNotifier()
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), notifier)
	t.Cleanup(manager.Shutdown)

	reminder := Reminder{
		ID:           "reminder-2-future",
		Text:         "Snooze later",
		ReminderTime: time.Now().Add(600 * time.Millisecond).UnixMilli(),
	}

	if err := manager.Schedule(reminder); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}

	time.Sleep(25 * time.Millisecond)

	updated, err := manager.Snooze(reminder.ID, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("Snooze returned error: %v", err)
	}
	if updated.ReminderTime <= reminder.ReminderTime {
		t.Fatal("expected snoozed reminder time to stay later than the original schedule")
	}

	select {
	case delivered := <-notifier.ch:
		t.Fatalf("reminder fired too early after snooze: %+v", delivered)
	case <-time.After(500 * time.Millisecond):
	}

	select {
	case delivered := <-notifier.ch:
		if delivered.ID != reminder.ID {
			t.Fatalf("unexpected reminder id after future snooze: got %q want %q", delivered.ID, reminder.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for future snoozed reminder delivery")
	}
}

func TestManagerSnoozeAfterReminderFires(t *testing.T) {
	notifier := newTestNotifier()
	manager := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), notifier)
	t.Cleanup(manager.Shutdown)

	reminder := Reminder{
		ID:           "reminder-3",
		Text:         "Snooze after fire",
		ReminderTime: time.Now().Add(80 * time.Millisecond).UnixMilli(),
	}

	if err := manager.Schedule(reminder); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}

	select {
	case <-notifier.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first reminder delivery")
	}

	updated, err := manager.Snooze(reminder.ID, 120*time.Millisecond)
	if err != nil {
		t.Fatalf("Snooze returned error after fire: %v", err)
	}
	if updated.ReminderTime <= reminder.ReminderTime {
		t.Fatal("expected snoozed reminder time to move forward after fire")
	}

	select {
	case delivered := <-notifier.ch:
		if delivered.ID != reminder.ID {
			t.Fatalf("unexpected reminder id after fire+snooze: got %q want %q", delivered.ID, reminder.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snoozed reminder after fire")
	}
}
