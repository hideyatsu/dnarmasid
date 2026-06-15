package main

import (
	"fmt"
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
	ChatID    int64
	MessageID int
	PriceID   uint
	Date      string
	Steps     []ProgressStep
	StartedAt time.Time
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

// StartSession registers a new republish session with initial steps
func (t *ProgressTracker) StartSession(chatID int64, messageID int, priceID uint, date string, initialSteps []ProgressStep) {
	t.Lock()
	defer t.Unlock()

	t.sessions[priceID] = &RepublishSession{
		ChatID:    chatID,
		MessageID: messageID,
		PriceID:   priceID,
		Date:      date,
		StartedAt: time.Now(),
		Steps:     initialSteps,
	}
}

// UpdateStep updates a step's status and returns the session (nil if not found)
func (t *ProgressTracker) UpdateStep(priceID uint, stepName string, status string, detail string) *RepublishSession {
	t.Lock()
	defer t.Unlock()

	sess, ok := t.sessions[priceID]
	if !ok {
		return nil
	}

	for i, s := range sess.Steps {
		if s.Name == stepName {
			sess.Steps[i].Status = status
			if detail != "" {
				sess.Steps[i].Detail = detail
			}
			break
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

// EditMessage updates the progress message in Telegram
func (t *ProgressTracker) EditMessage(sess *RepublishSession) {
	if sess == nil {
		return
	}

	text := RenderProgress(sess)

	edit := tgbotapi.NewEditMessageText(sess.ChatID, sess.MessageID, text)
	edit.ParseMode = "Markdown"
	if _, err := t.bot.Send(edit); err != nil {
		fmt.Printf("[progress-tracker] ⚠️ edit message error: %v\n", err)
	}
}

// SendToAdmin sends a message to the admin (chatID from session)
func (t *ProgressTracker) SendToAdmin(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := t.bot.Send(msg); err != nil {
		fmt.Printf("[progress-tracker] ⚠️ send to admin error: %v\n", err)
	}
}

// RenderProgress generates the progress message text
func RenderProgress(sess *RepublishSession) string {
	if sess == nil {
		return ""
	}

	total := len(sess.Steps)
	done := 0
	for _, s := range sess.Steps {
		if s.Status == "done" {
			done++
		}
	}

	// Build progress bar Unicode
	bar := ""
	for i := 0; i < total; i++ {
		if i < done {
			bar += "■"
		} else {
			bar += "□"
		}
	}

	// Header
	result := fmt.Sprintf("🔄 *Republish Progress — %s*\n\n", sess.Date)
	result += fmt.Sprintf("`%s` %d/%d\n\n", bar, done, total)

	// Steps
	for _, s := range sess.Steps {
		var icon string
		switch s.Status {
		case "done":
			icon = "✅"
		case "processing":
			icon = "⏳"
		case "error":
			icon = "❌"
		default:
			icon = "□"
		}

		detail := ""
		if s.Detail != "" {
			detail = " — " + s.Detail
		}

		result += fmt.Sprintf("%s %s%s\n", icon, s.Name, detail)
	}

	// Footer
	if done == total {
		elapsed := time.Since(sess.StartedAt)
		result += fmt.Sprintf("\n✅ *Pipeline selesai dalam %.0fs*", elapsed.Seconds())
	} else {
		result += "\n⏳ Menunggu..."
	}

	return result
}
