package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"
	"dnarmasid/shared/queue"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

// PipelineHandler handles manual pipeline triggers from admin
type PipelineHandler struct {
	cfg *config.Config
	db  *gorm.DB
	bot *tgbotapi.BotAPI
	q   *queue.Client
}

func NewPipelineHandler(cfg *config.Config, db *gorm.DB, bot *tgbotapi.BotAPI, q *queue.Client) *PipelineHandler {
	return &PipelineHandler{cfg: cfg, db: db, bot: bot, q: q}
}

func (p *PipelineHandler) Handle(chatID int64, args string) {
	if chatID != p.cfg.TelegramAdminChatID {
		return // silent drop for non-admin
	}

	parts := strings.Fields(args)
	if len(parts) == 0 {
		p.showHelp(chatID)
		return
	}

	action := strings.ToLower(parts[0])
	switch action {
	case "scrape":
		p.triggerScrape(chatID)
	case "ai":
		p.triggerAI(chatID)
	case "media":
		p.triggerMedia(chatID)
	case "threads":
		p.triggerThreads(chatID)
	case "publish":
		p.triggerPublish(chatID)
	case "republish":
		p.triggerRepublish(chatID)
	case "status":
		p.showStatus(chatID)
	default:
		p.send(chatID, "❌ Subcommand tidak dikenal. Ketik /pipeline untuk bantuan.")
	}
}

func (p *PipelineHandler) showHelp(chatID int64) {
	text := "⚙️ *Pipeline Manual Triggers (Admin)*\n\n" +
		"`/pipeline scrape` — Trigger scraper (ambil data harga baru)\n" +
		"`/pipeline ai` — Trigger AI generator → auto-trigger media generator\n" +
		"`/pipeline media` — Trigger media generator saja (skip AI)\n" +
		"`/pipeline threads` — Trigger threads generator\n" +
		"`/pipeline publish` — Trigger repliz uploader (posting ke sosmed)\n" +
		"`/republish` — Republish pipeline lengkap (AI → Media → Posting)\n" +
		"`/pipeline status` — Cek status pipeline hari ini\n\n" +
		"Pipeline serial: scrape → ai → media → publish.\n" +
		"Gunakan dengan hati-hati."

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	p.bot.Send(msg)
}

// getLatestEvent builds a complete GoldScrapedEvent from latest DB record
// Uses 1g Antam as base for change calculation + screenshots from generated_media if available
func (p *PipelineHandler) getLatestEvent() (*models.GoldScrapedEvent, error) {
	// Get latest 1g Antam price
	var today models.GoldPrice
	if err := p.db.Where("gram = ?", 1.0).Order("date DESC").First(&today).Error; err != nil {
		return nil, fmt.Errorf("tidak ada data harga 1g Antam di database: %w", err)
	}

	// Get all grams for today
	var prices []models.GoldPrice
	p.db.Where("date = ?", today.Date).Order("gram ASC").Find(&prices)

	if len(prices) == 0 {
		return nil, fmt.Errorf("tidak ada harga untuk tanggal %s", today.Date.Format("02 Jan 2006"))
	}

	// Get yesterday's 1g price for change calculation
	yesterday, _ := p.getPreviousPrice(today.Date)
	changePct, changeAmt, trend, bbChangeAmt, bbTrend := calcChange(today, yesterday)

	dateStr := formatDate(today.Date)
	var updateTimeStr string
	if today.SourceUpdateTime != nil {
		updateTimeStr = formatDate(*today.SourceUpdateTime) + " " + today.SourceUpdateTime.Format("15:04:05")
	}

	// Try to get screenshot URLs from previous generated_media
	var screenshotPriceURL, screenshotBuybackURL string
	var heroMedia models.GeneratedMedia
	if err := p.db.Where("price_id = ? AND file_name LIKE ?", today.ID, "hero_screenshot_%").First(&heroMedia).Error; err == nil {
		screenshotPriceURL = heroMedia.PublicURL
	}
	if err := p.db.Where("price_id = ? AND file_name LIKE ?", today.ID, "screenshot_%").First(&heroMedia).Error; err == nil {
		screenshotBuybackURL = heroMedia.PublicURL
	}

	return &models.GoldScrapedEvent{
		Date:                dateStr,
		UpdateTime:          updateTimeStr,
		PriceID:             today.ID,
		Prices:              prices,
		ChangePct:           changePct,
		ChangeAmt:           changeAmt,
		Trend:               trend,
		BuybackChangeAmt:    bbChangeAmt,
		BuybackTrend:        bbTrend,
		ScreenshotPriceURL:  screenshotPriceURL,
		ScreenshotBuybackURL: screenshotBuybackURL,
	}, nil
}

// getPreviousPrice fetches the 1g price from the previous trading day
func (p *PipelineHandler) getPreviousPrice(today time.Time) (*models.GoldPrice, error) {
	var prev models.GoldPrice
	err := p.db.Where("date < ? AND gram = ?", today, 1.0).
		Order("date DESC").
		First(&prev).Error
	if err != nil {
		return nil, err
	}
	return &prev, nil
}

// calcChange computes change metrics between today and yesterday 1g prices
func calcChange(today models.GoldPrice, yesterday *models.GoldPrice) (changePct float64, changeAmt int64, trend string, bbChangeAmt int64, bbTrend string) {
	if yesterday == nil || yesterday.BuyPrice == 0 {
		return 0, 0, "stable", 0, "stable"
	}

	changeAmt = today.BuyPrice - yesterday.BuyPrice
	changePct = float64(changeAmt) / float64(yesterday.BuyPrice) * 100

	trend = "stable"
	if changeAmt > 0 {
		trend = "up"
	} else if changeAmt < 0 {
		trend = "down"
	}

	bbChangeAmt = today.SellPrice - yesterday.SellPrice
	bbTrend = "stable"
	if bbChangeAmt > 0 {
		bbTrend = "up"
	} else if bbChangeAmt < 0 {
		bbTrend = "down"
	}

	return
}

func formatDate(t time.Time) string {
	s := t.Format("02 Jan 2006")
	s = strings.ReplaceAll(s, "May", "Mei")
	s = strings.ReplaceAll(s, "Aug", "Agt")
	s = strings.ReplaceAll(s, "Oct", "Okt")
	s = strings.ReplaceAll(s, "Dec", "Des")
	return s
}

func (p *PipelineHandler) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := p.bot.Send(msg); err != nil {
		log.Printf("[pipeline-handler] ⚠️ send error: %v", err)
	}
}

func (p *PipelineHandler) triggerScrape(chatID int64) {
	p.send(chatID, "⏳ Memulai proses scraping Antam secara manual...")

	job := map[string]string{
		"triggered_at": time.Now().Format(time.RFC3339),
		"source":       "telegram_bot",
	}

	if err := p.q.Publish(queue.KeyJobScrape, job); err != nil {
		p.send(chatID, "❌ Gagal mengirim job ke queue: "+err.Error())
		return
	}

	p.send(chatID, "✅ Job scraping berhasil dikirim ke antrean. Mohon tunggu notifikasi hasilnya.")
}

func (p *PipelineHandler) triggerAI(chatID int64) {
	event, err := p.getLatestEvent()
	if err != nil {
		p.send(chatID, "❌ Gagal mengambil data harga: "+err.Error())
		return
	}

	trendEmoji := "➡️"
	if event.Trend == "up" {
		trendEmoji = "🟢"
	} else if event.Trend == "down" {
		trendEmoji = "🔴"
	}

	p.send(chatID, fmt.Sprintf(
		"⏳ Triggering AI generator untuk tanggal *%s* ...\n\n"+
			"💰 Buy: Rp %s | Sell: Rp %s\n"+
			"%s Trend: *%s* | Change: Rp %s (%.2f%%)\n"+
			"\n_Caption + media akan diproses otomatis via serial pipeline..._",
		event.Date,
		formatPriceIDR(event.Prices[0].BuyPrice),
		formatPriceIDR(event.Prices[0].SellPrice),
		trendEmoji, event.Trend,
		formatPriceIDR(event.ChangeAmt),
		event.ChangePct,
	))

	if err := p.q.Publish(queue.KeyGoldScrapedAI, event); err != nil {
		p.send(chatID, "❌ Gagal publish ke queue AI: "+err.Error())
		return
	}

	p.send(chatID, fmt.Sprintf("✅ AI generator triggered untuk *%s*. Caption akan muncul dalam 30-60 detik.", event.Date))
}

func (p *PipelineHandler) triggerMedia(chatID int64) {
	event, err := p.getLatestEvent()
	if err != nil {
		p.send(chatID, "❌ Gagal mengambil data harga: "+err.Error())
		return
	}

	p.send(chatID, fmt.Sprintf("⏳ Triggering media generator untuk tanggal *%s* ...", event.Date))

	if err := p.q.Publish(queue.KeyGoldProcessed, event); err != nil {
		p.send(chatID, "❌ Gagal publish ke queue media: "+err.Error())
		return
	}

	p.send(chatID, fmt.Sprintf("✅ Media generator triggered untuk *%s*. Infografis akan muncul dalam 1-2 menit.", event.Date))
}

func (p *PipelineHandler) triggerThreads(chatID int64) {
	event, err := p.getLatestEvent()
	if err != nil {
		p.send(chatID, "❌ Gagal mengambil data harga: "+err.Error())
		return
	}

	p.send(chatID, fmt.Sprintf("⏳ Triggering threads generator untuk tanggal *%s* ...", event.Date))

	if err := p.q.Publish(queue.KeyGoldScrapedThreads, event); err != nil {
		p.send(chatID, "❌ Gagal publish ke queue threads: "+err.Error())
		return
	}

	p.send(chatID, fmt.Sprintf("✅ Threads generator triggered untuk *%s*. Konten akan dibuat dalam 30-60 detik.", event.Date))
}

func (p *PipelineHandler) triggerRepublish(chatID int64) {
	event, err := p.getLatestEvent()
	if err != nil {
		p.send(chatID, "❌ Gagal mengambil data harga: "+err.Error())
		return
	}

	trendEmoji := "➡️"
	if event.Trend == "up" {
		trendEmoji = "🟢"
	} else if event.Trend == "down" {
		trendEmoji = "🔴"
	}

	p.send(chatID, fmt.Sprintf(
		"🔄 *Republish Pipeline Started*\n\n"+
			"📅 Tanggal: *%s*\n"+
			"💰 1g Antam: Buy Rp %s | Sell Rp %s\n"+
			"%s Trend: *%s* | Change: Rp %s (%.2f%%)\n\n"+
			"▸ Step 1/3: AI Caption generating...\n"+
			"▸ Step 2/3: Infografis rendering...\n"+
			"▸ Step 3/3: Posting ke sosmed...\n\n"+
			"_Pipeline akan berjalan otomatis, pantau notifikasi berikutnya._",
		event.Date,
		formatPriceIDR(event.Prices[0].BuyPrice),
		formatPriceIDR(event.Prices[0].SellPrice),
		trendEmoji, event.Trend,
		formatPriceIDR(event.ChangeAmt),
		event.ChangePct,
	))

	// Trigger AI generator (which triggers media, then repliz via serial pipeline)
	if err := p.q.Publish(queue.KeyGoldScrapedAI, event); err != nil {
		p.send(chatID, "❌ Gagal publish ke queue AI: "+err.Error())
		return
	}

	log.Printf("[pipeline-handler] 🔄 Republish triggered for %s (price_id=%d)", event.Date, event.PriceID)
}

func (p *PipelineHandler) triggerPublish(chatID int64) {
	var latestPrice models.GoldPrice
	if err := p.db.Order("date DESC, gram ASC").First(&latestPrice).Error; err != nil {
		p.send(chatID, "❌ Gagal mengambil data harga: "+err.Error())
		return
	}

	dateStr := formatDate(latestPrice.Date)

	// Cari caption terbaru untuk price ini
	var caption models.GeneratedContent
	if err := p.db.Where("price_id = ? AND content_type = ?", latestPrice.ID, models.ContentCaption).
		Order("id DESC").First(&caption).Error; err != nil {
		p.send(chatID, "❌ Caption belum tersedia. Jalankan `/pipeline ai` terlebih dahulu.")
		return
	}

	// Cari infografis terbaru
	var media models.GeneratedMedia
	if err := p.db.Where("price_id = ? AND media_type = ?", latestPrice.ID, models.MediaTypeImage).
		Order("id DESC").First(&media).Error; err != nil || media.PublicURL == "" {
		p.send(chatID, "❌ Infografis belum tersedia. Jalankan `/pipeline media` terlebih dahulu.")
		return
	}

	// Cari CTA image
	var ctaURL string
	var ctaMedia models.GeneratedMedia
	if err := p.db.Where("price_id = ? AND file_name LIKE ?", latestPrice.ID, "cta_%").
		Order("id DESC").First(&ctaMedia).Error; err == nil && ctaMedia.PublicURL != "" {
		ctaURL = ctaMedia.PublicURL
	}

	event := models.MediaGenerationCompletedEvent{
		PriceID:        latestPrice.ID,
		Date:           dateStr,
		Caption:        caption.ContentText,
		InfographicURL: media.PublicURL,
		CTAImageURL:    ctaURL,
	}

	preview := truncateText(caption.ContentText, 80)

	msg := fmt.Sprintf("⏳ Triggering publish ke sosmed untuk *%s*...\n\n📝 Caption: %s\n🖼️ Infografis: %s\n", event.Date, preview, event.InfographicURL)
	if ctaURL != "" {
		msg += fmt.Sprintf("🎯 CTA: %s", ctaURL)
	}
	p.send(chatID, msg)

	if err := p.q.Publish(queue.KeyMediaGenerationCompleted, event); err != nil {
		p.send(chatID, "❌ Gagal publish ke queue repliz: "+err.Error())
		return
	}

	p.send(chatID, fmt.Sprintf("✅ Repliz uploader triggered untuk *%s*. Posting ke sosmed dalam 10-30 detik.", event.Date))
}

func (p *PipelineHandler) showStatus(chatID int64) {
	var latestPrice models.GoldPrice
	if err := p.db.Order("date DESC, gram ASC").First(&latestPrice).Error; err != nil {
		p.send(chatID, "❌ Belum ada data harga di database.")
		return
	}

	dateStr := formatDate(latestPrice.Date)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 *Pipeline Status — %s*\n", dateStr))
	sb.WriteString(fmt.Sprintf("💰 Gram: %.0fg | Buy: Rp %s | Sell: Rp %s\n\n", latestPrice.Gram, formatPriceIDR(latestPrice.BuyPrice), formatPriceIDR(latestPrice.SellPrice)))

	totalSteps := 5
	completedSteps := 0

	// 1. Scrape (data exists)
	sb.WriteString("1️⃣ *Scrape*   ✅ Data tersedia\n")
	completedSteps++

	// 2. AI Caption
	var captionCount int64
	p.db.Model(&models.GeneratedContent{}).
		Where("price_id = ? AND content_type = ? AND platform = ?", latestPrice.ID, models.ContentCaption, models.PlatformGeneral).
		Count(&captionCount)
	if captionCount > 0 {
		sb.WriteString("2️⃣ *AI Caption*   ✅ Ready\n")
		completedSteps++
	} else {
		sb.WriteString("2️⃣ *AI Caption*   ⏳ Belum ada — jalankan `/pipeline ai`\n")
	}

	// 3. Threads
	var threadsCount int64
	p.db.Model(&models.GeneratedContent{}).
		Where("price_id = ? AND platform = ?", latestPrice.ID, models.PlatformThreads).
		Count(&threadsCount)
	if threadsCount > 0 {
		sb.WriteString(fmt.Sprintf("3️⃣ *Threads*   ✅ %d konten(s)\n", threadsCount))
		completedSteps++
	} else {
		sb.WriteString("3️⃣ *Threads*   ⏳ Belum ada — jalankan `/pipeline threads`\n")
	}

	// 4. Media
	var mediaCount int64
	p.db.Model(&models.GeneratedMedia{}).
		Where("price_id = ? AND media_type = ?", latestPrice.ID, models.MediaTypeImage).
		Count(&mediaCount)
	if mediaCount > 0 {
		sb.WriteString("4️⃣ *Infografis*   ✅ Ready\n")
		completedSteps++
	} else {
		sb.WriteString("4️⃣ *Infografis*   ⏳ Belum ada — jalankan `/pipeline media`\n")
	}

	// 5. CTA
	var ctaCount int64
	p.db.Model(&models.GeneratedMedia{}).
		Where("price_id = ? AND file_name LIKE ?", latestPrice.ID, "cta_%").
		Count(&ctaCount)
	if ctaCount > 0 {
		sb.WriteString("5️⃣ *CTA Image*   ✅ Ready\n")
		completedSteps++
	} else {
		sb.WriteString("5️⃣ *CTA Image*   ⏳ Belum ada\n")
	}

	sb.WriteString(fmt.Sprintf("\n📊 Progress: *%d / %d* steps ready\n", completedSteps, totalSteps))

	// Rekomendasi
	if completedSteps < totalSteps {
		missing := ""
		if captionCount == 0 {
			missing += " → `/pipeline ai`"
		}
		if mediaCount == 0 {
			missing += " → `/pipeline media`"
		}
		if ctaCount == 0 && mediaCount > 0 {
			missing += " → `/pipeline publish` (akan otomatis generate CTA saat media di-trigger)"
		}
		if completedSteps >= 4 { // All except publish ready
			missing += "\n🚀 Semua siap publish! Jalankan `/pipeline publish`"
		}
		sb.WriteString(fmt.Sprintf("\n💡 Langkah selanjutnya:%s\n", missing))
	} else {
		sb.WriteString("\n🚀 *Semua siap!* Jalankan `/pipeline publish` untuk posting ke sosmed.")
	}

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "Markdown"
	if _, err := p.bot.Send(msg); err != nil {
		log.Printf("[pipeline-handler] ⚠️ send error: %v", err)
	}
}

// truncateText returns truncated text with ellipsis
func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return s
}

// formatPriceIDR formats integer to IDR currency (e.g., 1750000 → 1.750.000)
func formatPriceIDR(price int64) string {
	s := fmt.Sprintf("%d", price)
	var result strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result.WriteRune('.')
		}
		result.WriteRune(c)
	}
	return result.String()
}

// registerBotCommands registers bot commands to Telegram API (setMyCommands).
// Only public commands and /admin (command listing) are registered.
// Individual admin commands (/scrape, /threads, /pipeline) are intentionally
// omitted so they stay hidden from the command menu.
func registerBotCommands(bot *tgbotapi.BotAPI) error {
	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Mulai bot"},
		{Command: "subscribe", Description: "Berlangganan update harian"},
		{Command: "unsubscribe", Description: "Berhenti berlangganan"},
		{Command: "status", Description: "Cek status langganan"},
		{Command: "help", Description: "Tampilkan bantuan"},
		{Command: "admin", Description: "Daftar command admin"},
	}

	config := tgbotapi.NewSetMyCommands(commands...)
	_, err := bot.Request(config)
	return err
}