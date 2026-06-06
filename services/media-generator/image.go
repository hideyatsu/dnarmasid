package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"
	"dnarmasid/services/storage"
	"dnarmasid/shared/utils"

	"github.com/chromedp/chromedp"
	"gorm.io/gorm"
)

type MediaGenerator struct {
	cfg     *config.Config
	db      *gorm.DB
	storage *storage.R2Uploader
}

func NewMediaGenerator(cfg *config.Config, db *gorm.DB, r2Uploader *storage.R2Uploader) *MediaGenerator {
	return &MediaGenerator{cfg: cfg, db: db, storage: r2Uploader}
}

// GenerateImage membuat infografis harga emas memakai chromedp & HTML template
func (g *MediaGenerator) GenerateImage(event *models.GoldScrapedEvent) (*models.MediaReadyEvent, error) {
	// Temukan path template (sesuaikan dengan environment lokal & docker)
	templatePath := filepath.Join("templates", "priceTemplate.html")
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		templatePath = filepath.Join("services", "media-generator", "templates", "priceTemplate.html")
	}

	htmlContent, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read template %s: %w", templatePath, err)
	}
	htmlStr := string(htmlContent)

	// Cari harga 1 Gram
	var price1g models.GoldPrice
	for _, p := range event.Prices {
		if p.Gram == 1.0 {
			price1g = p
			break
		}
	}
	if price1g.Gram == 0 && len(event.Prices) > 0 {
		price1g = event.Prices[0] // Fallback index 0
	}

	// Helper visual trend
	getTrendIcon := func(trend string) string {
		switch trend {
		case "up":
			return `<svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg"><path d="M4 17l8-9 8 9H4z"/></svg>`
		case "down":
			return `<svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg"><path d="M4 8l8 9 8-9H4z"/></svg>`
		default:
			return `<svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg"><path d="M19 13H5v-2h14v2z"/></svg>`
		}
	}
	getTrendClass := func(trend string) string {
		switch trend {
		case "up":
			return "diff-up"
		case "down":
			return "diff-down"
		default:
			return "diff-neutral"
		}
	}

	// Substitusi placeholder
	replacements := map[string]string{
		"{{title}}":            "Update Harga Emas ANTAM",
		"{{date}}":             formatDate(event.Date),
		"{{price}}":            formatRupiah(price1g.BuyPrice),
		"{{priceDiffClass}}":   getTrendClass(event.Trend),
		"{{priceDiffIcon}}":    getTrendIcon(event.Trend),
		"{{priceDiffText}}":    formatRupiah(abs(event.ChangeAmt)),
		"{{buyback}}":          formatRupiah(price1g.SellPrice),
		"{{buybackDiffClass}}": getTrendClass(event.BuybackTrend),
		"{{buybackDiffIcon}}":  getTrendIcon(event.BuybackTrend),
		"{{buybackDiffText}}":  formatRupiah(abs(event.BuybackChangeAmt)),
	}

	for k, v := range replacements {
		htmlStr = strings.ReplaceAll(htmlStr, k, v)
	}

	// Tulis output ke file html temp agar bisa dibuka chromedp
	if err := os.MkdirAll(g.cfg.MediaOutputPath, 0755); err != nil {
		return nil, fmt.Errorf("failed creating output dir: %w", err)
	}
	tempHtmlPath := filepath.Join(g.cfg.MediaOutputPath, fmt.Sprintf("temp_%s.html", event.Date))
	if err := os.WriteFile(tempHtmlPath, []byte(htmlStr), 0644); err != nil {
		return nil, fmt.Errorf("failed to write resolved html: %w", err)
	}
	defer os.Remove(tempHtmlPath) // hapus selagi function exit

	absHtmlPath, _ := filepath.Abs(tempHtmlPath)
	fileURL := "file://" + absHtmlPath

	fileName := fmt.Sprintf("gold_%s.jpeg", event.Date)
	filePath := filepath.Join(g.cfg.MediaOutputPath, fileName)

	// Setup ExecAllocator untuk menginzinkan flag sandboxing no-sandbox saat di Docker (Alpine linux + Root)
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("binary", "/usr/bin/chromium-browser"),
		chromedp.Flag("extra-chromium-args", "--headless=new --disable-gpu"),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Setting Timeout 30 Detik
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Mulai tugas rendering resolusi IG post 1080x1080
	var buf []byte
	err = chromedp.Run(ctx,
		chromedp.EmulateViewport(1080, 1080),
		chromedp.Navigate(fileURL),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second), // Load font Montserrat external CDN dari internet
		chromedp.CaptureScreenshot(&buf),
	)
	if err != nil {
		return nil, fmt.Errorf("chromedp capture error: %w", err)
	}

	jpegBuf, err := utils.ConvertPNGToJPEG(buf)
	if err != nil {
		return nil, fmt.Errorf("failed converting png to jpeg: %w", err)
	}

	if err := os.WriteFile(filePath, jpegBuf, 0644); err != nil {
		return nil, fmt.Errorf("failed exporting jpeg: %w", err)
	}

	log.Printf("[media-generator] 🖼️ Image saved via chromedp: %s", filePath)

	// Upload ke R2 jika tersedia
	var publicURL string
	if g.storage != nil {
		content, err := os.ReadFile(filePath)
		if err == nil {
			url, err := g.storage.UploadFile(context.Background(), fileName, content, "image/jpeg")
			if err != nil {
				log.Printf("[media-generator] ❌ R2 upload failed: %v", err)
			} else {
				log.Printf("[media-generator] ☁️ Infographic uploaded to R2: %s", url)
				publicURL = url
				// Hapus file lokal setelah berhasil upload
				os.Remove(filePath)
				log.Printf("[media-generator] 🗑️ Local file removed: %s", filePath)
			}
		}
	}

	// Simpan ke DB
	finalPath := filePath
	if publicURL != "" {
		finalPath = publicURL
	}

	media := models.GeneratedMedia{
		PriceID:   event.PriceID,
		MediaType: models.MediaTypeImage,
		FilePath:  finalPath,
		FileName:  fileName,
		PublicURL: publicURL,
		Status:    "pending",
	}
	g.db.Create(&media)

	return &models.MediaReadyEvent{
		PriceID:   event.PriceID,
		Date:      event.Date,
		MediaType: models.MediaTypeImage,
		FilePath:  finalPath,
		FileName:  fileName,
		PublicURL: publicURL,
		ScreenshotPriceURL:   event.ScreenshotPriceURL,
		ScreenshotBuybackURL: event.ScreenshotBuybackURL,
	}, nil
}

// GenerateCTAImage membuat gambar CTA slide menggunakan ctaTemplate.html
func (g *MediaGenerator) GenerateCTAImage(event *models.GoldScrapedEvent) (string, error) {
	// Temukan path template
	templatePath := filepath.Join("templates", "ctaTemplate.html")
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		templatePath = filepath.Join("services", "media-generator", "templates", "ctaTemplate.html")
	}

	htmlContent, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to read CTA template %s: %w", templatePath, err)
	}
	htmlStr := string(htmlContent)

	// Substitusi placeholder CTA
	replacements := map[string]string{
		"{{title}}":        g.cfg.CTATitle,
		"{{date}}":         formatDate(event.Date),
		"{{cta_headline}}": g.cfg.CTAHeadline,
		"{{cta_subtext}}":  g.cfg.CTASubtext,
		"{{cta_handle}}":   g.cfg.CTAHandle,
	}

	for k, v := range replacements {
		htmlStr = strings.ReplaceAll(htmlStr, k, v)
	}

	// Tulis temp HTML
	if err := os.MkdirAll(g.cfg.MediaOutputPath, 0755); err != nil {
		return "", fmt.Errorf("failed creating output dir: %w", err)
	}
	tempHtmlPath := filepath.Join(g.cfg.MediaOutputPath, fmt.Sprintf("temp_cta_%s.html", event.Date))
	if err := os.WriteFile(tempHtmlPath, []byte(htmlStr), 0644); err != nil {
		return "", fmt.Errorf("failed to write CTA html: %w", err)
	}
	defer os.Remove(tempHtmlPath)

	absHtmlPath, _ := filepath.Abs(tempHtmlPath)
	fileURL := "file://" + absHtmlPath

	fileName := fmt.Sprintf("cta_%s.jpeg", event.Date)
	filePath := filepath.Join(g.cfg.MediaOutputPath, fileName)

	// Chromedp render
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("binary", "/usr/bin/chromium-browser"),
		chromedp.Flag("extra-chromium-args", "--headless=new --disable-gpu"),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var buf []byte
	err = chromedp.Run(ctx,
		chromedp.EmulateViewport(1080, 1080),
		chromedp.Navigate(fileURL),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.CaptureScreenshot(&buf),
	)
	if err != nil {
		return "", fmt.Errorf("chromedp CTA capture error: %w", err)
	}

	jpegBuf, err := utils.ConvertPNGToJPEG(buf)
	if err != nil {
		return "", fmt.Errorf("failed converting CTA png to jpeg: %w", err)
	}

	if err := os.WriteFile(filePath, jpegBuf, 0644); err != nil {
		return "", fmt.Errorf("failed exporting CTA jpeg: %w", err)
	}

	log.Printf("[media-generator] 🖼️ CTA image saved: %s", filePath)

	// Upload ke R2
	var publicURL string
	if g.storage != nil {
		content, err := os.ReadFile(filePath)
		if err == nil {
			url, err := g.storage.UploadFile(context.Background(), fileName, content, "image/jpeg")
			if err != nil {
				log.Printf("[media-generator] ❌ R2 CTA upload failed: %v", err)
			} else {
				log.Printf("[media-generator] ☁️ CTA uploaded to R2: %s", url)
				publicURL = url
				os.Remove(filePath)
			}
		}
	}

	if publicURL == "" {
		publicURL = filePath
	}

	return publicURL, nil
}

