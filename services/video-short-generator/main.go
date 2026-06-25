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
	narratorAI := NewNarratorAI(cfg)

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
			if err := processVideoShort(cfg, database, q, renderer, tts, stitcher, publisher, narratorAI, &event, outputDir); err != nil {
				log.Printf("[video-short] ❌ Pipeline failed: %v", err)

				// Publish error summary to admin
				failEvent := models.VideoShortDoneEvent{
					PriceID: event.PriceID,
					Date:    event.Date,
					Error:   err.Error(),
				}
				if pubErr := q.Publish(queue.KeyVideoShortDone, failEvent); pubErr != nil {
					log.Printf("[video-short] ⚠️ Failed to publish video.short.done (error): %v", pubErr)
				}
				continue
			}

			log.Printf("[video-short] ✅ Pipeline complete for %s", event.Date)
		}
	}
}

func processVideoShort(cfg *config.Config, database *gorm.DB, q *queue.Client,
	renderer *TemplateRenderer, tts *TTSGenerator, stitcher *VideoStitcher, publisher *Publisher,
	narratorAI *NarratorAI, event *models.GoldScrapedEvent, outputDir string) error {

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

	// ── STEP 3: Generate AI Content (slide text + narration) ──
	log.Println("[video-short] 🤖 Step 2: Generating AI content...")

	narratorData := NarratorData{
		Date:           event.Date,
		HargaJual:      hargaJual,
		HargaBuyback:   hargaBuyback,
		SpreadPct:      analysis.SpreadPct,
		DeltaJual:      analysis.DeltaJual,
		Trend7d:        analysis.Trend7d,
		Streak:         analysis.Streak,
		Condition:      int(analysis.Condition),
		ConditionLabel: conditionLabel(int(analysis.Condition)),
		Volatility:     analysis.Volatility,
		PriceHistory:   buildPriceHistory(analysis.PriceHistory7d),
	}

	// Try AI first, fallback to template
	var aiContent *AIContentResponse
	if resp, err := narratorAI.GenerateContent(narratorData); err == nil {
		aiContent = resp
		log.Printf("[video-short] 🤖 AI Content generated — slide: %s/%s, nar: hook=%d price=%d insight=%d cta=%d chars",
			aiContent.Slide.HookBadge, aiContent.Slide.HookHeadline,
			len(aiContent.Narration.Hook), len(aiContent.Narration.Price),
			len(aiContent.Narration.Insight), len(aiContent.Narration.CTA))
	} else {
		log.Printf("[video-short] ⚠️ AI Content failed (%v), using fallback", err)
		aiContent = buildFallbackContent(event, hargaJual, hargaBuyback, analysis)
	}

	log.Printf("[video-short] 📝 TTS — Hook: %s | Price: %s | Insight: %s | CTA: %s",
		truncate(aiContent.Narration.Hook, 50), truncate(aiContent.Narration.Price, 50),
		truncate(aiContent.Narration.Insight, 50), truncate(aiContent.Narration.CTA, 50))

	// ── STEP 4: Render Frames with AI content ──
	log.Println("[video-short] 🎨 Step 3: Rendering frames...")

	hookPath, err := renderer.RenderHook(
		event.Date,
		aiContent.Slide.HookBadge,
		aiContent.Slide.HookHeadline,
		aiContent.Slide.HookSubtitle,
	)
	if err != nil {
		return fmt.Errorf("render hook: %w", err)
	}

	pricePath, err := renderer.RenderPrice(event, hargaJual, hargaBuyback, analysis.SpreadPct)
	if err != nil {
		return fmt.Errorf("render price: %w", err)
	}

	insightPath, err := renderer.RenderInsight(
		event.Date,
		getInsightBadgeClass(analysis.Condition),
		aiContent.Slide.InsightBadge,
		aiContent.Slide.InsightHeadline,
		aiContent.Slide.InsightDetail,
		formatDecimal(analysis.SpreadPct),
		getTrendClass(analysis.Trend7d),
		getTrendLabel(analysis.Trend7d),
	)
	if err != nil {
		return fmt.Errorf("render insight: %w", err)
	}

	ctaPath, err := renderer.RenderCTA()
	if err != nil {
		return fmt.Errorf("render cta: %w", err)
	}

	// ── STEP 5: Generate 4-segment TTS ──
	log.Println("[video-short] 🎙️ Step 4: Generating segmented TTS...")

	dateStr := safeDate(event.Date)
	segmentScripts := []string{
		sanitizeTTSNumbers(aiContent.Narration.Hook),
		sanitizeTTSNumbers(aiContent.Narration.Price),
		sanitizeTTSNumbers(aiContent.Narration.Insight),
		sanitizeTTSNumbers(aiContent.Narration.CTA),
	}
	segmentNames := []string{"hook", "price", "insight", "cta"}

	var audioPaths []string
	var captionPaths []string
	for i, script := range segmentScripts {
		audioPath, captionPath, err := tts.GenerateTTS(
			script,
			fmt.Sprintf("seg-%s-%s", segmentNames[i], dateStr),
			int(analysis.Condition),
		)
		if err != nil {
			return fmt.Errorf("tts segment %s: %w", segmentNames[i], err)
		}
		audioPaths = append(audioPaths, audioPath)
		captionPaths = append(captionPaths, captionPath)
	}

	// Concatenate 4 audio files into 1
	mergedAudio, err := tts.ConcatAudio(audioPaths, fmt.Sprintf("narration-%s", dateStr))
	if err != nil {
		return fmt.Errorf("concat audio: %w", err)
	}

	// Merge 4 caption files with time offsets
	mergedCaption, err := tts.MergeCaptions(captionPaths, audioPaths, fmt.Sprintf("caption-%s", dateStr))
	if err != nil {
		return fmt.Errorf("merge captions: %w", err)
	}

	// Get audio duration
	duration, err := GetAudioDuration(mergedAudio)
	if err != nil {
		duration = 25.0
		log.Printf("[video-short] ⚠️ Could not get audio duration, using default: 25s")
	}

	// ── STEP 6: Stitch Video (4 frames) ──
	log.Println("[video-short] 🎬 Step 5: Stitching video (4 frames)...")
	outputName := fmt.Sprintf("video-short-%s.mp4", safeDate(event.Date))
	videoPath, err := stitcher.Stitch(StitchOptions{
		HookFrame:    hookPath,
		PriceFrame:   pricePath,
		InsightFrame: insightPath,
		CTAFrame:     ctaPath,
		AudioPath:    mergedAudio,
		CaptionPath:  mergedCaption,
		Duration:     duration,
		OutputName:   outputName,
	})
	if err != nil {
		return fmt.Errorf("stitch: %w", err)
	}

	// ── STEP 7: Upload to R2 ──
	log.Println("[video-short] ☁️ Step 6: Uploading to R2...")
	publicURL, err := publisher.UploadVideo(videoPath, event)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	// ── STEP 8: Save media record to DB ──
	log.Println("[video-short] 💾 Step 7: Saving media record...")
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

	// ── STEP 9: Publish summary to admin ──
	doneEvent := models.VideoShortDoneEvent{
		PriceID:      event.PriceID,
		Date:         event.Date,
		PublicURL:    publicURL,
		Condition:    int(analysis.Condition),
		ConditionLbl: conditionLabel(int(analysis.Condition)),
		HookVisual:   analysis.HookVisual,
		HargaJual:    hargaJual,
		HargaBuyback: hargaBuyback,
		SpreadPct:    analysis.SpreadPct,
		DurationSec:  duration,
	}
	if err := q.Publish(queue.KeyVideoShortDone, doneEvent); err != nil {
		log.Printf("[video-short] ⚠️ Failed to publish video.short.done: %v", err)
	} else {
		log.Printf("[video-short] ✅ video.short.done published for %s", event.Date)
	}

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
	switch analysis.Condition {
	case 1, 2: // Bullish
		b.WriteString("kabar baiknya, ")
	case 3, 4: // Bearish
		b.WriteString("saat ini, ")
	default:
		b.WriteString("dan, ")
	}

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

// safeDate converts "20 Jun 2026" → "2026-06-20" for filename safety (no spaces)
func safeDate(dateStr string) string {
	t, err := time.Parse("2 Jan 2006", dateStr)
	if err != nil {
		// Fallback: just replace spaces with hyphens
		return strings.ReplaceAll(dateStr, " ", "-")
	}
	return t.Format("2006-01-02")
}

// truncate shortens text for logging
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// buildFallbackContent generates fallback AIContentResponse when AI fails
func buildFallbackContent(event *models.GoldScrapedEvent, hargaJual, hargaBuyback int64, analysis *MarketAnalysis) *AIContentResponse {
	resp := &AIContentResponse{}

	// Slide content from analysis
	resp.Slide.HookBadge = analysis.VisualBadge
	resp.Slide.HookHeadline = analysis.HookVisual
	resp.Slide.HookSubtitle = "Harga Emas Hari Ini"

	// Insight from hook templates
	if hook, ok := hookTemplates[analysis.Condition]; ok {
		resp.Slide.InsightBadge = hook.Badge
		resp.Slide.InsightHeadline = hook.Visual
	} else {
		resp.Slide.InsightBadge = "⚖️"
		resp.Slide.InsightHeadline = "STABIL"
	}

	resp.Slide.InsightDetail = fmt.Sprintf("Spread %.1f%%, tren %s %d hari", analysis.SpreadPct, analysis.Trend7d, analysis.Streak)

	// Narration from buildTTSScript as single block
	fullScript := buildTTSScript(event, hargaJual, hargaBuyback, analysis)
	resp.Narration.Hook = analysis.HookTTS
	resp.Narration.Price = fmt.Sprintf("Harga jual sekarang %s per gram, buyback %s, spread %s persen",
		formatRupiah(hargaJual), formatRupiah(hargaBuyback), formatDecimal(analysis.SpreadPct))
	resp.Narration.Insight = resp.Slide.InsightDetail
	resp.Narration.CTA = "Buat update harga real-time, cek link di bio ya, jangan ketinggalan!"

	// Ensure no empty
	if fullScript != "" {
		_ = fullScript // used for logging context only
	}

	return resp
}

// Helper functions are in analysis.go
