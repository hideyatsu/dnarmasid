package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"
	"dnarmasid/shared/queue"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

type CommandHandler struct {
	cfg      *config.Config
	db       *gorm.DB
	bot      *tgbotapi.BotAPI
	q        *queue.Client
	pipeline *PipelineHandler
}

func NewCommandHandler(cfg *config.Config, db *gorm.DB, bot *tgbotapi.BotAPI, q *queue.Client) *CommandHandler {
	return &CommandHandler{
		cfg:      cfg,
		db:       db,
		bot:      bot,
		q:        q,
		pipeline: NewPipelineHandler(cfg, db, bot, q),
	}
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
	case "admin":
		h.handleAdmin(chatID)
	case "scrape":
		h.handleScrape(chatID)
	case "threads":
		h.handleThreads(chatID, msg.CommandArguments())
	case "pipeline":
		h.pipeline.Handle(chatID, msg.CommandArguments())
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
		"/help — Tampilkan bantuan\n"

	text += "\n📲 Follow kami: @DnarMasID"

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	h.bot.Send(msg)
}

func (h *CommandHandler) handleAdmin(chatID int64) {
	if chatID != h.cfg.TelegramAdminChatID {
		return // silent drop for non-admin
	}

	text := "⚙️ *Admin Commands*\n\n" +
		"*Scraper:*\n" +
		"`/scrape` — Trigger manual scrape harga Antam\n\n" +
		"*Threads:*\n" +
		"`/threads` — List pending konten Threads\n" +
		"`/threads <nomor>` — Preview full konten\n\n" +
		"*Pipeline:* (modular step-by-step)\n" +
		"`/pipeline scrape` — Trigger scraper\n" +
		"`/pipeline ai` — Trigger AI generator (caption)\n" +
		"`/pipeline media` — Trigger media generator (infografis)\n" +
		"`/pipeline threads` — Trigger threads generator\n" +
		"`/pipeline publish` — Trigger repliz uploader (posting sosmed)\n" +
		"`/pipeline status` — Cek status pipeline hari ini\n"
	h.send(chatID, text)
}

func (h *CommandHandler) handleScrape(chatID int64) {
	if chatID != h.cfg.TelegramAdminChatID {
		return // silent drop for non-admin
	}

	h.send(chatID, "⏳ Memulai proses scraping Antam secara manual...")

	job := map[string]string{
		"triggered_at": time.Now().Format(time.RFC3339),
		"source":       "telegram_bot",
	}

	if err := h.q.Publish(queue.KeyJobScrape, job); err != nil {
		h.send(chatID, "❌ Gagal mengirim job ke queue: "+err.Error())
		return
	}

	h.send(chatID, "✅ Job scraping berhasil dikirim ke antrean. Mohon tunggu notifikasi hasilnya.")
}

func (h *CommandHandler) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := h.bot.Send(msg); err != nil {
		log.Printf("[command-handler] ⚠️ send error: %v", err)
	}
}

func (h *CommandHandler) handleThreads(chatID int64, args string) {
	if chatID != h.cfg.TelegramAdminChatID {
		return // silent drop for non-admin
	}

	// If args is a number, show detail
	if args != "" {
		num, err := strconv.Atoi(args)
		if err == nil {
			h.handleThreadsDetail(chatID, num)
			return
		}
	}

	// List pending threads
	var contents []models.GeneratedContent
	h.db.Where("platform = ? AND status = ?", models.PlatformThreads, "pending").
		Order("created_at DESC").
		Limit(10).
		Find(&contents)

	if len(contents) == 0 {
		h.send(chatID, "🧵 Belum ada konten Threads pending.")
		return
	}

	var sb strings.Builder
	sb.WriteString("🧵 *Konten Threads Pending:*\n\n")

	for i, c := range contents {
		date := c.CreatedAt.Format("02 Jan")
		preview := c.ContentText
		runes := []rune(preview)
		if len(runes) > 60 {
			preview = string(runes[:60]) + "..."
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] *%s*\n   %s\n\n", i+1, date, c.ThreadType, preview))
	}

	sb.WriteString("Ketik `/threads <nomor>` untuk lihat full konten.")

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "Markdown"
	h.bot.Send(msg)
}

func (h *CommandHandler) handleThreadsDetail(chatID int64, num int) {
	var contents []models.GeneratedContent
	h.db.Where("platform = ? AND status = ?", models.PlatformThreads, "pending").
		Order("created_at DESC").
		Limit(10).
		Find(&contents)

	if num < 1 || num > len(contents) {
		h.send(chatID, "❌ Nomor tidak valid.")
		return
	}

	c := contents[num-1]
	text := fmt.Sprintf("🧵 *Threads [%s]* — %s\n\n%s\n\n---\nStatus: %s | Created: %s",
		c.ThreadType, c.CreatedAt.Format("02 Jan 2006 15:04"),
		c.ContentText, c.Status, c.CreatedAt.Format("02 Jan 2006"))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	h.bot.Send(msg)
}
