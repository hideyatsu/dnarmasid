package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"dnarmasid/services/storage"
	"dnarmasid/shared/config"
	"dnarmasid/shared/db"
	"dnarmasid/shared/models"
	"dnarmasid/shared/queue"
	"gorm.io/gorm"
)

func main() {
	log.Println("🎬 [video-short] Starting DnarMasID Video Short Generator...")

	cfg := config.Load()
	database := db.Connect(cfg)
	q := queue.NewClient(cfg)

	r2Uploader, err := storage.NewR2Uploader(cfg)
	if err != nil {
		log.Printf("[video-short] ⚠️ R2 Storage not configured: %v", err)
	}

	database.AutoMigrate(&models.GeneratedMedia{})

	// Dirs
	templateDir := "templates"
	outputDir := "/app/volumes/video"
	ensureDir(outputDir)

	renderer := NewTemplateRenderer(templateDir, outputDir)
	tts := NewTTSGenerator(outputDir)
	stitcher := NewVideoStitcher(outputDir)
	publisher := NewPublisher(r2Uploader)

	log.Printf("[video-short] ✅ Ready. Waiting for %s events...", queue.KeyVideoShortTrigger)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-quit:
			log.Println("[video-short] Shutting down...")
			return
		default:
			var event models.GoldScrapedEvent
			err := q.ConsumeJSON(queue.KeyVideoShortTrigger, 5*time.Second, &event)
			if err != nil {
				continue
			}

			log.Printf("[video-short] 📥 Event received: date=%s price_id=%d", event.Date, event.PriceID)

			// Process video
			if err := processVideoShort(cfg, database, q, renderer, tts, stitcher, publisher, &event, outputDir); err != nil {
				log.Printf("[video-short] ❌ Pipeline failed: %v", err)
				continue
			}

			log.Printf("[video-short] ✅ Pipeline complete for %s", event.Date)
		}
	}
}

func processVideoShort(cfg *config.Config, database *gorm.DB, q *queue.Client,
	renderer *TemplateRenderer, tts *TTSGenerator, stitcher *VideoStitcher, publisher *Publisher,
	event *models.GoldScrapedEvent, outputDir string) error {

	// ── STEP 1: Market Analysis ──
	log.Println("[video-short] 📊 Step 1: Market Analysis...")
	analysis, err := AnalyzeMarket(event, database)
	if err != nil {
		return fmt.Errorf("analysis: %w", err)
	}
	log.Printf("[video-short] 📊 Condition: %d (%s)", analysis.Condition, analysis.HookVisual)

	// ── STEP 2: Get prices ──
	var hargaJual, hargaBuyback int64
	for _, p := range event.Prices {
		if p.Gram == 1.0 {
			hargaJual = p.SellPrice
			hargaBuyback = p.BuyPrice
			break
		}
	}
	if hargaJual == 0 && len(event.Prices) > 0 {
		hargaJual = event.Prices[0].SellPrice
		hargaBuyback = event.Prices[0].BuyPrice
	}

	// ── STEP 3: Render Frames ──
	log.Println("[video-short] 🎨 Step 2: Rendering frames...")

	hookPath, err := renderer.RenderHook(
		event.Date,
		analysis.VisualBadge,
		analysis.HookVisual,
		"Harga Emas Hari Ini",
	)
	if err != nil {
		return fmt.Errorf("render hook: %w", err)
	}

	pricePath, err := renderer.RenderPrice(event, hargaJual, hargaBuyback, analysis.SpreadPct)
	if err != nil {
		return fmt.Errorf("render price: %w", err)
	}

	ctaPath, err := renderer.RenderCTA()
	if err != nil {
		return fmt.Errorf("render cta: %w", err)
	}

	// ── STEP 4: Generate TTS ──
	log.Println("[video-short] 🎙️ Step 3: Generating TTS...")
	script := buildTTSScript(event, hargaJual, hargaBuyback, analysis)
	audioPath, captionPath, err := tts.GenerateTTS(sanitizeTTSNumbers(script), fmt.Sprintf("narration-%s", event.Date), int(analysis.Condition))
	if err != nil {
		return fmt.Errorf("tts: %w", err)
	}

	// Get audio duration
	duration, err := GetAudioDuration(audioPath)
	if err != nil {
		duration = 18.0
		log.Printf("[video-short] ⚠️ Could not get audio duration, using default: 18s")
	}

	// ── STEP 5: Stitch Video ──
	log.Println("[video-short] 🎬 Step 4: Stitching video...")
	outputName := fmt.Sprintf("video-short-%s.mp4", event.Date)
	videoPath, err := stitcher.Stitch(StitchOptions{
		HookFrame:   hookPath,
		PriceFrame:  pricePath,
		CTAFrame:    ctaPath,
		AudioPath:   audioPath,
		CaptionPath: captionPath,
		Duration:    duration,
		OutputName:  outputName,
	})
	if err != nil {
		return fmt.Errorf("stitch: %w", err)
	}

	// ── STEP 6: Upload to R2 ──
	log.Println("[video-short] ☁️ Step 5: Uploading to R2...")
	publicURL, err := publisher.UploadVideo(videoPath, event)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	// ── STEP 7: Save media record to DB ──
	log.Println("[video-short] 💾 Step 6: Saving media record...")
	media := models.GeneratedMedia{
		PriceID:   event.PriceID,
		MediaType: models.MediaTypeVideo,
		FilePath:  videoPath,
		FileName:  outputName,
		PublicURL: publicURL,
		Status:    "sent",
	}
	if err := database.Create(&media).Error; err != nil {
		log.Printf("[video-short] ⚠️ Failed to save media record: %v", err)
	}

	log.Printf("[video-short] ✅ Video ready: %s", publicURL)
	return nil
}

// buildTTSScript assembles the narration script (ID: titik → koma untuk desimal)
func buildTTSScript(event *models.GoldScrapedEvent, hargaJual, hargaBuyback int64, analysis *MarketAnalysis) string {
	// Hook
	script := analysis.HookTTS + " "

	// Bridge
	script += "Cek harga hari ini. "

	// Price info
	script += fmt.Sprintf("Harga jual %s rupiah per gram. ", formatRupiah(hargaJual))
	script += fmt.Sprintf("Buyback %s rupiah. ", formatRupiah(hargaBuyback))
	script += fmt.Sprintf("Spread %s persen. ", formatDecimal(analysis.SpreadPct))

	// Delta
	if analysis.DeltaJual > 0 {
		script += fmt.Sprintf("Naik %s rupiah dari kemarin. ", formatRupiah(abs(analysis.DeltaJual)))
	} else if analysis.DeltaJual < 0 {
		script += fmt.Sprintf("Turun %s rupiah dari kemarin. ", formatRupiah(abs(analysis.DeltaJual)))
	} else {
		script += "Stabil dari kemarin. "
	}

	// Historical insight
	switch analysis.Trend7d {
	case "up":
		script += "Tren 7 hari terakhir menunjukkan kenaikan. "
	case "down":
		script += "Tren 7 hari terakhir menunjukkan penurunan. "
	}

	// CTA
	script += "Update instan? Klik link di bio."

	return script
}

// formatDecimal formats float with comma (ID locale) for TTS: 9.52 → "9,52"
func formatDecimal(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.Replace(s, ".", ",", 1)
}

// sanitizeTTSNumbers replaces decimal dots with commas for Indonesian TTS.
// Only matches decimal patterns (1-2 digits after dot), NOT thousand separators.
// e.g., "9.5 persen" → "9,5 persen", "0.17" → "0,17", "1.250.000" stays untouched
func sanitizeTTSNumbers(text string) string {
	// Match digit.digit{1,2} where it's NOT part of a thousand-separator group (3 digits after dot)
	// Uses word boundary + negative lookahead for 3rd digit
	re := regexp.MustCompile(`(\d+)\.(\d{1,2})\b`)
	return re.ReplaceAllString(text, "$1,$2")
}

// Helper functions are in analysis.go
