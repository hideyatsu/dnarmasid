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
	log.Printf("[video-short] 📝 TTS Script: %s", script)
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

// buildTTSScript assembles the narration script — natural storytelling style
// Target: 60-80 kata ≈ 20-25 detik voice over, smooth flow with bridging
func buildTTSScript(event *models.GoldScrapedEvent, hargaJual, hargaBuyback int64, analysis *MarketAnalysis) string {
	var b strings.Builder

	// 1. Hook — retensi tinggi, langsung dari AI
	b.WriteString(analysis.HookTTS)
	b.WriteString(", ")

	// 2. Bridging ke harga — natural transition
	b.WriteString("oke, ")

	// 3. Harga — conversational dengan flow
	switch analysis.Condition {
	case 1, 2: // Bullish
		b.WriteString(fmt.Sprintf("harga jual sekarang di %s per gram ya, buyback %s",
			formatRupiah(hargaJual), formatRupiah(hargaBuyback)))
	case 3, 4: // Bearish
		b.WriteString(fmt.Sprintf("hari ini jual di %s per gram, buyback-nya %s",
			formatRupiah(hargaJual), formatRupiah(hargaBuyback)))
	case 5, 6: // High spread
		b.WriteString(fmt.Sprintf("harga jual %s, buyback %s per gram",
			formatRupiah(hargaJual), formatRupiah(hargaBuyback)))
	default:
		b.WriteString(fmt.Sprintf("jual %s per gram, buyback %s",
			formatRupiah(hargaJual), formatRupiah(hargaBuyback)))
	}

	// 4. Spread — direct, no period yet
	b.WriteString(fmt.Sprintf(", spread %s persen", formatDecimal(analysis.SpreadPct)))

	// 5. Delta — natural connector
	if analysis.DeltaJual > 10000 {
		b.WriteString(fmt.Sprintf(", naik %s dari kemarin", formatRupiah(abs(analysis.DeltaJual))))
	} else if analysis.DeltaJual < -10000 {
		b.WriteString(fmt.Sprintf(", turun %s dari kemarin nih", formatRupiah(abs(analysis.DeltaJual))))
	}

	// 6. Bridging ke trend insight
	b.WriteString(". ")

	// 7. Trend insight — lebih personal & actionable
	switch analysis.Trend7d {
	case "up":
		if analysis.Streak >= 3 {
			b.WriteString(fmt.Sprintf("Udah %d hari naik terus, momentum kuat nih", analysis.Streak))
		} else if analysis.Streak >= 2 {
			b.WriteString("Dua hari berturut-turut naik, tren positif mulai terbentuk")
		} else {
			b.WriteString("Tren minggu ini masih positif, tapi belum kuat")
		}
	case "down":
		if analysis.Streak >= 3 {
			b.WriteString(fmt.Sprintf("Udah %d hari turun berturut-turut, mungkin waktu yang tepat buat akumulasi", analysis.Streak))
		} else if analysis.Streak >= 2 {
			b.WriteString("Dua hari turun, masih wait and see dulu")
		} else {
			b.WriteString("Minggu ini agak melemah, tapi belum ada sinyal kuat")
		}
	case "sideways":
		b.WriteString("Pasar masih sideways, hold dulu sambil pantau pergerakan")
	}

	// 8. Bridging ke CTA
	b.WriteString(". ")

	// 9. CTA — friendly dengan urgency
	b.WriteString("Buat update harga real-time, cek link di bio ya, jangan ketinggalan!")

	return b.String()
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
