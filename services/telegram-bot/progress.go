package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ProgressStep represents a single step in the republish pipeline
type ProgressStep struct {
	Name   string
	Status string // "pending", "processing", "done", "error"
	Detail string
}

// RepublishSession tracks a single republish pipeline run
type RepublishSession struct {
	ChatID       int64
	MessageID    int
	PriceID      uint
	Date         string
	StepsA       []ProgressStep // Pipeline Infografis: Fetch Data → AI Caption → Media Render → Repliz Upload
	StepsB       []ProgressStep // Pipeline Video Short: AI Caption → Video Short
	StartedAt    time.Time
	LastUpdateAt time.Time // updated setiap ada progress change
	FailedAt     *time.Time
	GroupADone   bool // true once group A reaches completion
	GroupBDone   bool // true once group B reaches completion
}

// ProgressTracker tracks all active republish sessions (thread-safe)
type ProgressTracker struct {
	sync.RWMutex
	sessions map[uint]*RepublishSession
	bot      *tgbotapi.BotAPI
}

// NewProgressTracker creates a new tracker
func NewProgressTracker(bot *tgbotapi.BotAPI) *ProgressTracker {
	return &ProgressTracker{
		sessions: make(map[uint]*RepublishSession),
		bot:      bot,
	}
}

// StartSession registers a new republish session with initial steps for both pipelines
func (t *ProgressTracker) StartSession(chatID int64, messageID int, priceID uint, date string, stepsA []ProgressStep, stepsB []ProgressStep) {
	t.Lock()
	defer t.Unlock()

	now := time.Now()
	t.sessions[priceID] = &RepublishSession{
		ChatID:       chatID,
		MessageID:    messageID,
		PriceID:      priceID,
		Date:         date,
		StartedAt:    now,
		LastUpdateAt: now,
		StepsA:       stepsA,
		StepsB:       stepsB,
	}
}

// UpdateStep updates a step's status in BOTH groups (for shared steps like AI Caption)
// Returns the session (nil if not found)
func (t *ProgressTracker) UpdateStep(priceID uint, stepName string, status string, detail string) *RepublishSession {
	t.Lock()
	defer t.Unlock()

	sess, ok := t.sessions[priceID]
	if !ok {
		return nil
	}

	// Update in group A
	for i, s := range sess.StepsA {
		if s.Name == stepName {
			sess.StepsA[i].Status = status
			if detail != "" {
				sess.StepsA[i].Detail = detail
			}
			break
		}
	}

	// Update in group B (shared steps like AI Caption)
	for i, s := range sess.StepsB {
		if s.Name == stepName {
			sess.StepsB[i].Status = status
			if detail != "" {
				sess.StepsB[i].Detail = detail
			}
			break
		}
	}

	sess.LastUpdateAt = time.Now()
	return sess
}

// MarkFailed marks all pending/processing steps as failed in both groups
func (t *ProgressTracker) MarkFailed(priceID uint, reason string) *RepublishSession {
	t.Lock()
	defer t.Unlock()

	sess, ok := t.sessions[priceID]
	if !ok {
		return nil
	}

	now := time.Now()
	sess.FailedAt = &now

	for i := range sess.StepsA {
		if sess.StepsA[i].Status != "done" {
			sess.StepsA[i].Status = "error"
			if sess.StepsA[i].Detail == "" {
				sess.StepsA[i].Detail = reason
			}
		}
	}
	for i := range sess.StepsB {
		if sess.StepsB[i].Status != "done" {
			sess.StepsB[i].Status = "error"
			if sess.StepsB[i].Detail == "" {
				sess.StepsB[i].Detail = reason
			}
		}
	}

	return sess
}

// GetSession returns a session for the given priceID (nil if not found)
func (t *ProgressTracker) GetSession(priceID uint) *RepublishSession {
	t.RLock()
	defer t.RUnlock()
	return t.sessions[priceID]
}

// RemoveSession removes a session from the tracker
func (t *ProgressTracker) RemoveSession(priceID uint) {
	t.Lock()
	defer t.Unlock()
	delete(t.sessions, priceID)
}

// CheckGroupADone returns true if all steps in group A are done
func (t *ProgressTracker) CheckGroupADone(priceID uint) bool {
	t.RLock()
	defer t.RUnlock()

	sess, ok := t.sessions[priceID]
	if !ok {
		return false
	}
	for _, s := range sess.StepsA {
		if s.Status != "done" {
			return false
		}
	}
	return true
}

// CheckGroupBDone returns true if all steps in group B are done
func (t *ProgressTracker) CheckGroupBDone(priceID uint) bool {
	t.RLock()
	defer t.RUnlock()

	sess, ok := t.sessions[priceID]
	if !ok {
		return false
	}
	for _, s := range sess.StepsB {
		if s.Status != "done" {
			return false
		}
	}
	return true
}

// AllDone returns true if both groups are complete
func (t *ProgressTracker) AllDone(priceID uint) bool {
	return t.CheckGroupADone(priceID) && t.CheckGroupBDone(priceID)
}

// GetStaleSessions returns sessions older than timeout (for failure detection)
func (t *ProgressTracker) GetStaleSessions(timeout time.Duration) []*RepublishSession {
	t.RLock()
	defer t.RUnlock()

	var stale []*RepublishSession
	now := time.Now()
	for _, sess := range t.sessions {
		if now.Sub(sess.LastUpdateAt) > timeout && sess.FailedAt == nil {
			stale = append(stale, sess)
		}
	}
	return stale
}

// EditMessage updates the progress message in Telegram
func (t *ProgressTracker) EditMessage(sess *RepublishSession) {
	if sess == nil {
		log.Printf("[progress-tracker] ⚠️ EditMessage called with nil session")
		return
	}

	text := RenderProgress(sess)

	edit := tgbotapi.NewEditMessageText(sess.ChatID, sess.MessageID, text)
	edit.ParseMode = "Markdown"
	if _, err := t.bot.Send(edit); err != nil {
		log.Printf("[progress-tracker] ⚠️ edit message error: %v", err)
	} else {
		log.Printf("[progress-tracker] ✅ Message edited: chat_id=%d, msg_id=%d", sess.ChatID, sess.MessageID)
	}
}

// SendToAdmin sends a message to the admin (chatID from session)
func (t *ProgressTracker) SendToAdmin(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := t.bot.Send(msg); err != nil {
		log.Printf("[progress-tracker] ⚠️ send to admin error: %v", err)
	} else {
		log.Printf("[progress-tracker] ✅ Admin notified: chat_id=%d", chatID)
	}
}

// renderGroup renders a single pipeline group section
func renderGroup(title string, emoji string, steps []ProgressStep) string {
	total := len(steps)
	done := 0
	hasError := false
	for _, s := range steps {
		if s.Status == "done" {
			done++
		}
		if s.Status == "error" {
			hasError = true
		}
	}

	// Progress bar
	bar := ""
	for _, s := range steps {
		if s.Status == "done" {
			bar += "■"
		} else if s.Status == "error" {
			bar += "■"
		} else {
			bar += "□"
		}
	}

	result := fmt.Sprintf("%s *%s*\n`%s` %d/%d\n", emoji, title, bar, done, total)

	for _, s := range steps {
		var icon string
		switch s.Status {
		case "done":
			icon = "✅"
		case "processing":
			icon = "⏳"
		case "error":
			icon = "❌"
		default:
			icon = "⬜"
		}

		detail := ""
		if s.Detail != "" {
			detail = " — " + s.Detail
		}

		result += fmt.Sprintf("%s %s%s\n", icon, s.Name, detail)
	}

	// Group status footer
	if hasError {
		result += "🔴 Gagal\n"
	} else if done == total {
		result += "✅ Selesai\n"
	} else {
		result += "⏳ Berjalan...\n"
	}

	return result
}

// RenderProgress generates the progress message text with two parallel pipeline groups
func RenderProgress(sess *RepublishSession) string {
	if sess == nil {
		return ""
	}

	elapsed := time.Since(sess.StartedAt)
	result := fmt.Sprintf("🔄 *Republish Progress — %s*\n⏱️ %.0fs\n\n", sess.Date, elapsed.Seconds())

	// Group A: Pipeline Infografis
	result += renderGroup("Pipeline Infografis", "📊", sess.StepsA)
	result += "\n"
	// Group B: Pipeline Video Short
	result += renderGroup("Pipeline Video Short", "🎬", sess.StepsB)

	return result
}
