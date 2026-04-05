package main

import (
	"log"
	"os"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

type CommandHandler struct {
	cfg *config.Config
	db  *gorm.DB
	bot *tgbotapi.BotAPI
}

func NewCommandHandler(cfg *config.Config, db *gorm.DB, bot *tgbotapi.BotAPI) *CommandHandler {
	return &CommandHandler{cfg: cfg, db: db, bot: bot}
}

// Listen mendengarkan update/command dari user Telegram
func (h *CommandHandler) Listen(quit chan os.Signal) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := h.bot.GetUpdatesChan(u)

	log.Println("[command-handler] 📡 Listening for user commands...")

	for {
		select {
		case <-quit:
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if update.Message == nil {
				continue
			}
			h.handleMessage(update.Message)
		}
	}
}

func (h *CommandHandler) handleMessage(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	username := msg.From.UserName
	firstName := msg.From.FirstName

	log.Printf("[command-handler] 📨 Message from @%s (%d): %s", username, chatID, msg.Text)

	switch msg.Command() {
	case "start":
		h.handleStart(chatID, username, firstName)
	case "subscribe":
		h.handleSubscribe(chatID, username, firstName)
	case "unsubscribe":
		h.handleUnsubscribe(chatID)
	case "status":
		h.handleStatus(chatID)
	case "help":
		h.handleHelp(chatID)
	default:
		if msg.IsCommand() {
			h.send(chatID, "❓ Command tidak dikenal. Ketik /help untuk daftar command.")
		}
	}
}

func (h *CommandHandler) handleStart(chatID int64, username, firstName string) {
	text := "👋 Halo " + firstName + "!\n\n" +
		"Selamat datang di *DnarMasID Bot* 🥇\n\n" +
		"Bot ini memberikan update harga emas Antam setiap hari.\n\n" +
		"Ketik /subscribe untuk mulai berlangganan\n" +
		"Ketik /help untuk bantuan"

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	h.bot.Send(msg)
}

func (h *CommandHandler) handleSubscribe(chatID int64, username, firstName string) {
	var sub models.Subscriber
	result := h.db.Where("chat_id = ?", chatID).First(&sub)

	if result.Error == nil {
		// Sudah ada — aktifkan kembali
		h.db.Model(&sub).Update("status", "active")
		h.send(chatID, "✅ Langganan kamu sudah aktif! Kamu akan menerima update harga emas setiap hari. 🥇")
		return
	}

	// Daftar baru
	h.db.Create(&models.Subscriber{
		ChatID:    chatID,
		Username:  username,
		FirstName: firstName,
		Status:    "active",
	})

	h.send(chatID, "🎉 Berhasil berlangganan *DnarMasID*!\n\nKamu akan menerima update harga emas Antam setiap hari jam 08.00 WIB. 🥇")
}

func (h *CommandHandler) handleUnsubscribe(chatID int64) {
	h.db.Model(&models.Subscriber{}).
		Where("chat_id = ?", chatID).
		Update("status", "inactive")

	h.send(chatID, "😢 Kamu telah berhenti berlangganan.\nKetik /subscribe kapan saja untuk berlangganan kembali.")
}

func (h *CommandHandler) handleStatus(chatID int64) {
	var sub models.Subscriber
	result := h.db.Where("chat_id = ?", chatID).First(&sub)

	if result.Error != nil {
		h.send(chatID, "❌ Kamu belum berlangganan. Ketik /subscribe untuk mulai.")
		return
	}

	status := "✅ Aktif"
	if sub.Status == "inactive" {
		status = "⏸️ Tidak aktif"
	}

	text := "📋 *Status Langganan*\n\n" +
		"Nama: " + sub.FirstName + "\n" +
		"Status: " + status + "\n" +
		"Bergabung: " + sub.SubscribedAt.Format("02 Jan 2006")

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	h.bot.Send(msg)
}

func (h *CommandHandler) handleHelp(chatID int64) {
	text := "🆘 *DnarMasID Bot — Commands*\n\n" +
		"/start — Mulai bot\n" +
		"/subscribe — Berlangganan update harian\n" +
		"/unsubscribe — Berhenti berlangganan\n" +
		"/status — Cek status langganan\n" +
		"/help — Tampilkan bantuan\n\n" +
		"📲 Follow kami: @DnarMasID"

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	h.bot.Send(msg)
}

func (h *CommandHandler) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := h.bot.Send(msg); err != nil {
		log.Printf("[command-handler] ⚠️ send error: %v", err)
	}
}
