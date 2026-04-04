package main

import (
	"fmt"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"

	"github.com/fogleman/gg"
	"gorm.io/gorm"
)

type MediaGenerator struct {
	cfg *config.Config
	db  *gorm.DB
}

func NewMediaGenerator(cfg *config.Config, db *gorm.DB) *MediaGenerator {
	return &MediaGenerator{cfg: cfg, db: db}
}

// GenerateImage membuat infografis harga emas (1080x1080 px — IG square)
func (g *MediaGenerator) GenerateImage(event *models.GoldScrapedEvent) (*models.MediaReadyEvent, error) {
	const (
		W = 1080
		H = 1080
	)

	dc := gg.NewContext(W, H)

	// Background gradient — gold theme
	drawBackground(dc, W, H)

	// Header
	drawHeader(dc, W, event.Date)

	// Price table
	drawPriceTable(dc, W, event.Prices)

	// Change indicator
	drawChangeIndicator(dc, W, event.ChangeAmt, event.ChangePct, event.Trend)

	// Branding
	drawBranding(dc, W, H)

	// Save file
	fileName := fmt.Sprintf("gold_%s.png", event.Date)
	filePath := filepath.Join(g.cfg.MediaOutputPath, fileName)

	if err := dc.SavePNG(filePath); err != nil {
		return nil, fmt.Errorf("save PNG error: %w", err)
	}

	log.Printf("[media-generator] 🖼️ Image saved: %s", filePath)

	// Simpan ke DB
	media := models.GeneratedMedia{
		PriceID:   event.PriceID,
		MediaType: models.MediaTypeImage,
		FilePath:  filePath,
		FileName:  fileName,
		Status:    "pending",
	}
	g.db.Create(&media)

	return &models.MediaReadyEvent{
		PriceID:   event.PriceID,
		Date:      event.Date,
		MediaType: models.MediaTypeImage,
		FilePath:  filePath,
		FileName:  fileName,
	}, nil
}

// ─────────────────────────────────────────
// Drawing helpers
// ─────────────────────────────────────────

func drawBackground(dc *gg.Context, w, h int) {
	// Dark background
	dc.SetColor(color.RGBA{R: 18, G: 18, B: 18, A: 255})
	dc.DrawRectangle(0, 0, float64(w), float64(h))
	dc.Fill()

	// Gold accent bar top
	dc.SetColor(color.RGBA{R: 212, G: 175, B: 55, A: 255}) // gold
	dc.DrawRectangle(0, 0, float64(w), 8)
	dc.Fill()

	// Gold accent bar bottom
	dc.DrawRectangle(0, float64(h)-8, float64(w), 8)
	dc.Fill()
}

func drawHeader(dc *gg.Context, w int, date string) {
	// Logo text DnarMasID
	dc.SetColor(color.RGBA{R: 212, G: 175, B: 55, A: 255})
	dc.DrawStringAnchored("DnarMasID", float64(w)/2, 60, 0.5, 0.5)

	// Title
	dc.SetColor(color.White)
	dc.DrawStringAnchored("📊 Update Harga Emas ANTAM", float64(w)/2, 110, 0.5, 0.5)

	// Date
	dc.SetColor(color.RGBA{R: 180, G: 180, B: 180, A: 255})
	dc.DrawStringAnchored(date, float64(w)/2, 150, 0.5, 0.5)

	// Divider
	dc.SetColor(color.RGBA{R: 212, G: 175, B: 55, A: 100})
	dc.DrawLine(60, 175, float64(w)-60, 175)
	dc.SetLineWidth(1)
	dc.Stroke()
}

func drawPriceTable(dc *gg.Context, w int, prices []models.GoldPrice) {
	startY := 210.0
	rowH := 70.0

	for i, p := range prices {
		y := startY + float64(i)*rowH

		// Alternating row background
		if i%2 == 0 {
			dc.SetColor(color.RGBA{R: 30, G: 30, B: 30, A: 255})
			dc.DrawRectangle(60, y-5, float64(w)-120, rowH-5)
			dc.Fill()
		}

		// Gram label
		dc.SetColor(color.RGBA{R: 212, G: 175, B: 55, A: 255})
		dc.DrawStringAnchored(fmt.Sprintf("%.1f gr", p.Gram), 130, y+30, 0.5, 0.5)

		// Buy price
		dc.SetColor(color.White)
		dc.DrawStringAnchored(fmt.Sprintf("Rp %s", formatRupiah(p.BuyPrice)), float64(w)/2, y+30, 0.5, 0.5)

		// Sell label
		dc.SetColor(color.RGBA{R: 150, G: 150, B: 150, A: 255})
		dc.DrawStringAnchored(fmt.Sprintf("Jual: %s", formatRupiah(p.SellPrice)), float64(w)-130, y+30, 0.5, 0.5)

		if i >= 7 { // Batasi tampil 8 baris
			break
		}
	}
}

func drawChangeIndicator(dc *gg.Context, w int, changeAmt int64, changePct float64, trend string) {
	y := 800.0

	// Background pill
	bgColor := color.RGBA{R: 39, G: 174, B: 96, A: 200} // green
	if trend == "down" {
		bgColor = color.RGBA{R: 231, G: 76, B: 60, A: 200} // red
	} else if trend == "stable" {
		bgColor = color.RGBA{R: 100, G: 100, B: 100, A: 200}
	}

	dc.SetColor(bgColor)
	dc.DrawRoundedRectangle(float64(w)/2-200, y-25, 400, 55, 28)
	dc.Fill()

	// Text
	sign := "+"
	if changeAmt < 0 {
		sign = ""
	}
	trendEmoji := "📈"
	if trend == "down" {
		trendEmoji = "📉"
	} else if trend == "stable" {
		trendEmoji = "➡️"
	}

	dc.SetColor(color.White)
	text := fmt.Sprintf("%s %sRp %s (%s%.2f%%)", trendEmoji, sign, formatRupiah(changeAmt), sign, changePct)
	dc.DrawStringAnchored(text, float64(w)/2, y+5, 0.5, 0.5)
}

func drawBranding(dc *gg.Context, w, h int) {
	dc.SetColor(color.RGBA{R: 212, G: 175, B: 55, A: 180})
	dc.DrawStringAnchored("@DnarMasID • Update Setiap Hari", float64(w)/2, float64(h)-40, 0.5, 0.5)
}

func formatRupiah(amount int64) string {
	s := fmt.Sprintf("%d", abs(amount))
	result := ""
	for i, c := range reverseStr(s) {
		if i > 0 && i%3 == 0 {
			result = "." + result
		}
		result = string(c) + result
	}
	return result
}

func reverseStr(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// GenerateVideo — placeholder, butuh FFmpeg di production
func (g *MediaGenerator) GenerateVideo(event *models.GoldScrapedEvent) (*models.MediaReadyEvent, error) {
	// TODO: Implementasi video generation dengan FFmpeg
	// Untuk sekarang, buat placeholder file txt sebagai marker
	fileName := fmt.Sprintf("gold_%s.mp4.todo", event.Date)
	filePath := filepath.Join(g.cfg.MediaOutputPath, fileName)

	f, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}
	f.WriteString(fmt.Sprintf("Video placeholder for %s - implement FFmpeg here\nGenerated at: %s",
		event.Date, time.Now().Format(time.RFC3339)))
	f.Close()

	log.Printf("[media-generator] 🎬 Video placeholder created: %s", fileName)
	log.Printf("[media-generator] ⚠️  Implement FFmpeg video generation for production!")

	media := models.GeneratedMedia{
		PriceID:   event.PriceID,
		MediaType: models.MediaTypeVideo,
		FilePath:  filePath,
		FileName:  fileName,
		Status:    "pending",
	}
	g.db.Create(&media)

	return &models.MediaReadyEvent{
		PriceID:   event.PriceID,
		Date:      event.Date,
		MediaType: models.MediaTypeVideo,
		FilePath:  filePath,
		FileName:  fileName,
	}, nil
}
