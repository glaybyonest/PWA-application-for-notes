package reminders

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const SnoozeDuration = 5 * time.Minute

var (
	ErrReminderNotFound = errors.New("reminder not found")
	ErrReminderPastTime = errors.New("reminder time must be in the future")
)

// Reminder describes a server-scheduled push reminder.
type Reminder struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	ReminderTime int64  `json:"reminderTime"`
}

// Notifier sends push notifications when a reminder fires.
type Notifier interface {
	NotifyReminder(Reminder) error
}

type job struct {
	reminder Reminder
	timer    *time.Timer
}

// Manager keeps active in-memory reminder timers.
type Manager struct {
	logger   *slog.Logger
	notifier Notifier

	mu   sync.Mutex
	jobs map[string]*job
}

// NewManager creates a reminder manager.
func NewManager(logger *slog.Logger, notifier Notifier) *Manager {
	if logger == nil {
		logger = slog.Default()
	}

	return &Manager{
		logger:   logger,
		notifier: notifier,
		jobs:     make(map[string]*job),
	}
}

// Schedule creates or replaces a server-side reminder timer.
func (m *Manager) Schedule(reminder Reminder) error {
	if strings.TrimSpace(reminder.ID) == "" {
		return errors.New("reminder id is required")
	}
	if strings.TrimSpace(reminder.Text) == "" {
		return errors.New("reminder text is required")
	}

	reminderTime := time.UnixMilli(reminder.ReminderTime)
	delay := time.Until(reminderTime)
	if delay <= 0 {
		return ErrReminderPastTime
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.jobs[reminder.ID]; ok && existing.timer != nil {
		existing.timer.Stop()
	}

	job := &job{reminder: reminder}
	job.timer = time.AfterFunc(delay, func() {
		m.fire(reminder.ID)
	})
	m.jobs[reminder.ID] = job

	m.logger.Info("reminder scheduled",
		"reminderID", reminder.ID,
		"reminderTime", reminderTime.Format(time.RFC3339),
		"delay", delay.String(),
		"activeReminders", len(m.jobs),
	)

	return nil
}

// Snooze postpones an existing reminder and reschedules it.
func (m *Manager) Snooze(reminderID string, duration time.Duration) (Reminder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.jobs[reminderID]
	if !ok {
		m.logger.Warn("reminder not found for snooze", "reminderID", reminderID)
		return Reminder{}, ErrReminderNotFound
	}

	if existing.timer != nil {
		existing.timer.Stop()
	}

	baseTime := time.Now()
	reminderTime := time.UnixMilli(existing.reminder.ReminderTime)
	if reminderTime.After(baseTime) {
		baseTime = reminderTime
	}

	updatedTime := baseTime.Add(duration)
	delay := time.Until(updatedTime)
	if delay < 0 {
		delay = 0
	}

	existing.reminder.ReminderTime = updatedTime.UnixMilli()
	updated := existing.reminder
	existing.timer = time.AfterFunc(delay, func() {
		m.fire(reminderID)
	})

	m.logger.Info("reminder snoozed",
		"reminderID", reminderID,
		"newReminderTime", updatedTime.Format(time.RFC3339),
		"delay", delay.String(),
	)

	return updated, nil
}

// Shutdown stops all active timers.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, reminderJob := range m.jobs {
		if reminderJob.timer != nil {
			reminderJob.timer.Stop()
		}
		delete(m.jobs, id)
	}
}

func (m *Manager) fire(reminderID string) {
	m.mu.Lock()
	reminderJob, ok := m.jobs[reminderID]
	if !ok {
		m.mu.Unlock()
		return
	}

	reminder := reminderJob.reminder
	reminderJob.timer = nil
	activeCount := len(m.jobs)
	m.mu.Unlock()

	m.logger.Info("reminder fired",
		"reminderID", reminder.ID,
		"activeReminders", activeCount,
	)

	if m.notifier == nil {
		return
	}

	if err := m.notifier.NotifyReminder(reminder); err != nil {
		m.logger.Error("failed to deliver reminder push", "reminderID", reminder.ID, "error", err)
		return
	}

	m.logger.Info("reminder push sent",
		"reminderID", reminder.ID,
		"scheduledFor", fmt.Sprintf("%d", reminder.ReminderTime),
	)
}
