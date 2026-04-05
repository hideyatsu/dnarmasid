package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

type Broadcaster struct {
	cfg *config.Config
	db  *gorm.DB
	bot *tgbotapi.BotAPI
}

func NewBroadcaster(cfg *config.Config, db *gorm.DB, bot *tgbotapi.BotAPI) *Broadcaster {
	return &Broadcaster{cfg: cfg, db: db, bot: bot}
}

// SendContent mengirim semua caption per platform ke admin chat
func (b *Broadcaster) SendContent(event *models.ContentReadyEvent) error {
	adminID := b.cfg.TelegramAdminChatID

	// ── Header
	header := fmt.Sprintf(
		"━━━━━━━━━━━━━━━━━━━━━━\n"+
			"📊 *DAILY GOLD REPORT — DnarMasID*\n"+
			"📅 %s\n"+
			"━━━━━━━━━━━━━━━━━━━━━━\n\n"+
			"💡 *Analisis:*\n%s",
		event.Date,
		event.Analysis,
	)
	b.sendMarkdown(adminID, header)

	// ── Caption per platform
	platforms := []struct {
		key   models.Platform
		emoji string
		label string
	}{
		{models.PlatformInstagram, "📸", "INSTAGRAM"},
		{models.PlatformFacebook, "👥", "FACEBOOK"},
		{models.PlatformThreads, "🧵", "THREADS"},
		{models.PlatformTwitter, "🐦", "TWITTER / X"},
		{models.PlatformYouTube, "▶️", "YOUTUBE"},
		{models.PlatformTikTok, "🎵", "TIKTOK"},
	}

	for _, p := range platforms {
		content, ok := event.Contents[p.key]
		if !ok || content == "" {
			continue
		}

		// Kirim divider + label platform
		divider := fmt.Sprintf("━━━━━━━━━━━━━━━━━━━━━━\n%s *%s*\n━━━━━━━━━━━━━━━━━━━━━━",
			p.emoji, p.label)
		b.sendMarkdown(adminID, divider)

		// Kirim konten (plain text supaya bisa di-copy langsung)
		b.sendText(adminID, content)

		log.Printf("[broadcaster] ✅ Sent %s caption to admin", p.key)
	}

	// ── Footer
	footer := "━━━━━━━━━━━━━━━━━━━━━━\n✅ *Semua caption siap!*\nCopy-paste ke masing-masing platform.\n@DnarMasID"
	b.sendMarkdown(adminID, footer)

	// Update status DB
	b.db.Model(&models.GeneratedContent{}).
		Where("price_id = ? AND status = ?", event.PriceID, "pending").
		Update("status", "sent")

	return nil
}

// SendMedia mengirim gambar/video ke admin chat
func (b *Broadcaster) SendMedia(event *models.MediaReadyEvent) error {
	adminID := b.cfg.TelegramAdminChatID

	switch event.MediaType {
	case models.MediaTypeImage:
		return b.sendImageFile(adminID, event)
	case models.MediaTypeVideo:
		return b.sendVideoFile(adminID, event)
	}

	return nil
}

// sendImageFile upload dan kirim file gambar ke Telegram
func (b *Broadcaster) sendImageFile(chatID int64, event *models.MediaReadyEvent) error {
	f, err := os.Open(event.FilePath)
	if err != nil {
		return fmt.Errorf("open image file: %w", err)
	}
	defer f.Close()

	caption := fmt.Sprintf("🖼️ *Infografis Harga Emas*\n📅 %s\n\nSiap diposting ke Instagram, Facebook, Threads.", event.Date)

	msg := tgbotapi.NewPhoto(chatID, tgbotapi.FileReader{
		Name:   event.FileName,
		Reader: f,
	})
	msg.Caption = caption
	msg.ParseMode = "Markdown"

	_, err = b.bot.Send(msg)
	if err != nil {
		return fmt.Errorf("send photo: %w", err)
	}

	log.Printf("[broadcaster] 🖼️ Image sent to admin: %s", event.FileName)

	b.db.Model(&models.GeneratedMedia{}).
		Where("file_name = ?", event.FileName).
		Update("status", "sent")

	return nil
}

// sendVideoFile upload dan kirim file video ke Telegram
func (b *Broadcaster) sendVideoFile(chatID int64, event *models.MediaReadyEvent) error {
	// Skip placeholder .todo file
	if strings.HasSuffix(event.FileName, ".todo") {
		b.sendMarkdown(chatID,
			"🎬 *Video/Reels*\n⚠️ Video generation belum diimplementasikan.\nImplement FFmpeg di `services/media-generator/image.go` → `GenerateVideo()`")
		return nil
	}

	f, err := os.Open(event.FilePath)
	if err != nil {
		return fmt.Errorf("open video file: %w", err)
	}
	defer f.Close()

	caption := fmt.Sprintf("🎬 *Video/Reels Harga Emas*\n📅 %s\n\nSiap diposting ke TikTok, YouTube Shorts, Instagram Reels.", event.Date)

	msg := tgbotapi.NewVideo(chatID, tgbotapi.FileReader{
		Name:   event.FileName,
		Reader: f,
	})
	msg.Caption = caption
	msg.ParseMode = "Markdown"

	_, err = b.bot.Send(msg)
	if err != nil {
		return fmt.Errorf("send video: %w", err)
	}

	log.Printf("[broadcaster] 🎬 Video sent to admin: %s", event.FileName)

	b.db.Model(&models.GeneratedMedia{}).
		Where("file_name = ?", event.FileName).
		Update("status", "sent")

	return nil
}

// ─────────────────────────────────────────
// Helper send functions
// ─────────────────────────────────────────

func (b *Broadcaster) sendMarkdown(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := b.bot.Send(msg); err != nil {
		log.Printf("[broadcaster] ⚠️ sendMarkdown error: %v", err)
	}
}

func (b *Broadcaster) sendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := b.bot.Send(msg); err != nil {
		log.Printf("[broadcaster] ⚠️ sendText error: %v", err)
	}
}

// SendScrapeNotification mengirim notifikasi singkat harga harian
func (b *Broadcaster) SendScrapeNotification(event *models.GoldScrapedEvent) error {
	adminID := b.cfg.TelegramAdminChatID

	// Cari harga 1 gram
	var price1g models.GoldPrice
	for _, p := range event.Prices {
		if p.Gram == 1 {
			price1g = p
			break
		}
	}

	if price1g.BuyPrice == 0 {
		return fmt.Errorf("no 1 gram price found")
	}

	bbChangeAmt := event.BuybackChangeAmt
	if bbChangeAmt < 0 {
		bbChangeAmt = -bbChangeAmt
	}

	importHelper := func(n int64) string {
		if n < 0 {
			n = -n
		}
		s := fmt.Sprintf("%d", n)
		var parts []string
		for i := len(s); i > 0; i -= 3 {
			start := i - 3
			if start < 0 {
				start = 0
			}
			parts = append([]string{s[start:i]}, parts...)
		}
		return strings.Join(parts, ".")
	}

	// Deteksi panah trend (Buy)
	trendArrow := "▬"
	if event.Trend == "up" {
		trendArrow = "▲"
	} else if event.Trend == "down" {
		trendArrow = "▼"
	}

	// Deteksi panah trend (Buyback)
	bbTrendArrow := "▬"
	if event.BuybackTrend == "up" {
		bbTrendArrow = "▲"
	} else if event.BuybackTrend == "down" {
		bbTrendArrow = "▼"
	}

	dateFmt := event.Date
	if event.UpdateTime != "" {
		dateFmt = event.UpdateTime
	} else if parsed, err := time.Parse("2006-01-02", event.Date); err == nil {
		dateFmt = parsed.Format("02 Jan 2006")
	}

	msgText := fmt.Sprintf(
		"Harga Emas Antam Hari Ini 🪙\n\n"+
			"📅 %s\n"+
			"💰 Rp %s / gram %s (Rp %s)\n"+
			"🔄 Rp %s / gram %s (Rp %s)\n",
		dateFmt,
		importHelper(price1g.BuyPrice), trendArrow, importHelper(event.ChangeAmt),
		importHelper(price1g.SellPrice), bbTrendArrow, importHelper(event.BuybackChangeAmt),
	)

	b.sendText(adminID, msgText)
	log.Printf("[broadcaster] ✅ Sent scrape notification to admin")

	return nil
}
