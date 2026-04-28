package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"
	"html"

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

// SendContent mengirim satu caption tunggal hasil AI ke chat yang ditentukan (Admin atau Topic Grup)
func (b *Broadcaster) SendContent(event *models.ContentReadyEvent) error {
	if b.cfg.TelegramGroupID == 0 {
		return fmt.Errorf("TELEGRAM_GROUP_ID belum dikonfigurasi")
	}

	chatID := b.cfg.TelegramGroupID
	threadID := b.cfg.TelegramThreadPostID

	content, ok := event.Contents[models.PlatformGeneral]
	if !ok || content == "" {
		return fmt.Errorf("caption tidak ditemukan")
	}

	// Kirim konten utama
	b.sendText(chatID, threadID, content)

	// Update status DB
	b.db.Model(&models.GeneratedContent{}).
		Where("price_id = ? AND status = ?", event.PriceID, "pending").
		Update("status", "sent")

	return nil
}

// SendMedia mengirim gambar/video ke admin chat
func (b *Broadcaster) SendMedia(event *models.MediaReadyEvent) error {
	if b.cfg.TelegramGroupID == 0 {
		return fmt.Errorf("TELEGRAM_GROUP_ID belum dikonfigurasi")
	}

	chatID := b.cfg.TelegramGroupID
	threadID := b.cfg.TelegramThreadPostID

	switch event.MediaType {
	case models.MediaTypeImage:
		return b.sendImageFile(chatID, threadID, event)
	case models.MediaTypeVideo:
		return b.sendVideoFile(chatID, threadID, event)
	}

	return nil
}

// sendImageFile upload dan kirim file gambar ke Telegram
func (b *Broadcaster) sendImageFile(chatID int64, threadID int, event *models.MediaReadyEvent) error {
	caption := fmt.Sprintf("<b>🖼️ Infografis Harga Emas</b>\n📅 %s\n\nSiap diposting ke Instagram, Facebook, Threads.", event.Date)

	if event.PublicURL != "" || event.ScreenshotPriceURL != "" || event.ScreenshotBuybackURL != "" {
		caption += "\n\n<b>🔗 Download:</b>"
		if event.PublicURL != "" {
			caption += fmt.Sprintf("\n• <a href=\"%s\">Infografis HD</a>", event.PublicURL)
		}
		if event.ScreenshotPriceURL != "" {
			caption += fmt.Sprintf("\n• <a href=\"%s\">Screenshot Harga</a>", event.ScreenshotPriceURL)
		}
		if event.ScreenshotBuybackURL != "" {
			caption += fmt.Sprintf("\n• <a href=\"%s\">Screenshot Buyback</a>", event.ScreenshotBuybackURL)
		}
	}

	// Jika FilePath adalah URL, kirim langsung via URL
	if strings.HasPrefix(event.FilePath, "http") {
		params := tgbotapi.Params{}
		params.AddNonZero64("chat_id", chatID)
		params.AddNonZero("message_thread_id", threadID)
		params.AddNonEmpty("photo", event.FilePath)
		params.AddNonEmpty("caption", caption)
		params.AddNonEmpty("parse_mode", "HTML")
		_, err := b.bot.MakeRequest("sendPhoto", params)
		if err != nil {
			return fmt.Errorf("send photo by url: %w", err)
		}
	} else {
		// Kirim via upload file lokal
		f, err := os.Open(event.FilePath)
		if err != nil {
			return fmt.Errorf("open image file: %w", err)
		}
		defer f.Close()

		params := tgbotapi.Params{}
		params.AddNonZero64("chat_id", chatID)
		params.AddNonZero("message_thread_id", threadID)
		params.AddNonEmpty("caption", caption)
		params.AddNonEmpty("parse_mode", "HTML")

		_, err = b.bot.UploadFiles("sendPhoto", params, []tgbotapi.RequestFile{{
			Name: "photo",
			Data: tgbotapi.FileReader{Name: event.FileName, Reader: f},
		}})
		if err != nil {
			return fmt.Errorf("send photo by upload: %w", err)
		}
	}

	log.Printf("[broadcaster] 🖼️ Image sent to chat %d (thread %d): %s", chatID, threadID, event.FileName)

	b.db.Model(&models.GeneratedMedia{}).
		Where("file_name = ?", event.FileName).
		Update("status", "sent")

	return nil
}

// sendVideoFile upload dan kirim file video ke Telegram
func (b *Broadcaster) sendVideoFile(chatID int64, threadID int, event *models.MediaReadyEvent) error {
	// Skip placeholder .todo file
	if strings.HasSuffix(event.FileName, ".todo") {
		b.sendHTML(chatID, threadID,
			"<b>🎬 Video/Reels</b>\n⚠️ Video generation belum diimplementasikan.\nImplement FFmpeg di <code>services/media-generator/image.go</code> → <code>GenerateVideo()</code>")
		return nil
	}

	caption := fmt.Sprintf("<b>🎬 Video/Reels Harga Emas</b>\n📅 %s\n\nSiap diposting ke TikTok, YouTube Shorts, Instagram Reels.", event.Date)

	if event.PublicURL != "" || event.ScreenshotPriceURL != "" || event.ScreenshotBuybackURL != "" {
		caption += "\n\n<b>🔗 Download:</b>"
		if event.PublicURL != "" {
			caption += fmt.Sprintf("\n• <a href=\"%s\">Video HD</a>", event.PublicURL)
		}
		if event.ScreenshotPriceURL != "" {
			caption += fmt.Sprintf("\n• <a href=\"%s\">Screenshot Harga</a>", event.ScreenshotPriceURL)
		}
		if event.ScreenshotBuybackURL != "" {
			caption += fmt.Sprintf("\n• <a href=\"%s\">Screenshot Buyback</a>", event.ScreenshotBuybackURL)
		}
	}

	// Jika FilePath adalah URL, kirim langsung via URL
	if strings.HasPrefix(event.FilePath, "http") {
		params := tgbotapi.Params{}
		params.AddNonZero64("chat_id", chatID)
		params.AddNonZero("message_thread_id", threadID)
		params.AddNonEmpty("video", event.FilePath)
		params.AddNonEmpty("caption", caption)
		params.AddNonEmpty("parse_mode", "HTML")
		_, err := b.bot.MakeRequest("sendVideo", params)
		if err != nil {
			return fmt.Errorf("send video by url: %w", err)
		}
	} else {
		f, err := os.Open(event.FilePath)
		if err != nil {
			return fmt.Errorf("open video file: %w", err)
		}
		defer f.Close()

		params := tgbotapi.Params{}
		params.AddNonZero64("chat_id", chatID)
		params.AddNonZero("message_thread_id", threadID)
		params.AddNonEmpty("caption", caption)
		params.AddNonEmpty("parse_mode", "HTML")

		_, err = b.bot.UploadFiles("sendVideo", params, []tgbotapi.RequestFile{{
			Name: "video",
			Data: tgbotapi.FileReader{Name: event.FileName, Reader: f},
		}})
		if err != nil {
			return fmt.Errorf("send video by upload: %w", err)
		}
	}

	log.Printf("[broadcaster] 🎬 Video sent to chat %d (thread %d): %s", chatID, threadID, event.FileName)

	b.db.Model(&models.GeneratedMedia{}).
		Where("file_name = ?", event.FileName).
		Update("status", "sent")

	return nil
}

// ─────────────────────────────────────────
// Helper send functions
// ─────────────────────────────────────────

func (b *Broadcaster) sendHTML(chatID int64, threadID int, text string) {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_thread_id", threadID)
	params.AddNonEmpty("text", text)
	params.AddNonEmpty("parse_mode", "HTML")
	if _, err := b.bot.MakeRequest("sendMessage", params); err != nil {
		log.Printf("[broadcaster] ⚠️ sendHTML error: %v", err)
	}
}

func (b *Broadcaster) sendText(chatID int64, threadID int, text string) {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_thread_id", threadID)
	params.AddNonEmpty("text", text)
	if _, err := b.bot.MakeRequest("sendMessage", params); err != nil {
		log.Printf("[broadcaster] ⚠️ sendText error: %v", err)
	}
}

// SendScrapeNotification mengirim notifikasi singkat harga harian
func (b *Broadcaster) SendScrapeNotification(event *models.GoldScrapedEvent) error {
	if b.cfg.TelegramGroupID == 0 {
		return fmt.Errorf("TELEGRAM_GROUP_ID belum dikonfigurasi")
	}

	chatID := b.cfg.TelegramGroupID
	threadID := b.cfg.TelegramThreadGeneralID

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

	b.sendText(chatID, threadID, msgText)
	log.Printf("[broadcaster] ✅ Sent scrape notification to chat %d (thread %d)", chatID, threadID)

	return nil
}

// SendScrapeFailureNotification mengirim notifikasi kegagalan scraping
func (b *Broadcaster) SendScrapeFailureNotification(event *models.ScrapeFailedEvent) error {
	if b.cfg.TelegramGroupID == 0 {
		return fmt.Errorf("TELEGRAM_GROUP_ID belum dikonfigurasi")
	}

	chatID := b.cfg.TelegramGroupID
	threadID := b.cfg.TelegramThreadGeneralID

	msgText := fmt.Sprintf(
		"⚠️ <b>Gagal Mengambil Data Harga Emas</b>\n\n"+
			"📅 Tanggal: %s\n"+
			"🔍 Sumber: %s\n"+
			"❌ Error: <code>%s</code>\n\n"+
			"Sistem akan mencoba kembali pada jadwal berikutnya.",
		event.Date, event.Source, html.EscapeString(event.Message),
	)

	b.sendHTML(chatID, threadID, msgText)
	log.Printf("[broadcaster] ⚠️ Sent scrape failure notification to chat %d (thread %d)", chatID, threadID)

	return nil
}
