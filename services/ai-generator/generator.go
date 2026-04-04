package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"

	"gorm.io/gorm"
)

type ContentGenerator struct {
	cfg *config.Config
	db  *gorm.DB
}

func NewContentGenerator(cfg *config.Config, db *gorm.DB) *ContentGenerator {
	return &ContentGenerator{cfg: cfg, db: db}
}

// Generate membuat semua caption untuk semua platform
func (g *ContentGenerator) Generate(event *models.GoldScrapedEvent) (*models.ContentReadyEvent, error) {
	contents := make(map[models.Platform]string)

	platforms := []models.Platform{
		models.PlatformInstagram,
		models.PlatformTwitter,
		models.PlatformFacebook,
		models.PlatformThreads,
		models.PlatformYouTube,
		models.PlatformTikTok,
	}

	for _, platform := range platforms {
		log.Printf("[ai-generator] Generating content for %s...", platform)

		content, err := g.generateForPlatform(platform, event)
		if err != nil {
			log.Printf("[ai-generator] ⚠️ Failed for %s: %v", platform, err)
			content = g.fallbackContent(platform, event)
		}

		contents[platform] = content

		// Simpan ke DB
		g.db.Create(&models.GeneratedContent{
			PriceID:     event.PriceID,
			Platform:    platform,
			ContentType: models.ContentCaption,
			ContentText: content,
			Status:      "pending",
		})

		time.Sleep(500 * time.Millisecond) // rate limit API
	}

	// Generate analisis
	analysis, err := g.generateAnalysis(event)
	if err != nil {
		analysis = g.fallbackAnalysis(event)
	}

	return &models.ContentReadyEvent{
		PriceID:  event.PriceID,
		Date:     event.Date,
		Contents: contents,
		Analysis: analysis,
	}, nil
}

// generateForPlatform memanggil Anthropic API untuk generate caption
func (g *ContentGenerator) generateForPlatform(platform models.Platform, event *models.GoldScrapedEvent) (string, error) {
	prompt := g.buildPrompt(platform, event)
	return g.callAnthropic(prompt)
}

// buildPrompt membuat prompt spesifik per platform
func (g *ContentGenerator) buildPrompt(platform models.Platform, event *models.GoldScrapedEvent) string {
	priceTable := formatPriceTable(event.Prices)
	trendEmoji := trendEmoji(event.Trend)
	changeStr := formatChange(event.ChangeAmt, event.ChangePct, event.Trend)

	baseInfo := fmt.Sprintf(`
Data Harga Emas Antam Hari Ini (%s):
%s

Perubahan vs kemarin: %s %s
Tren: %s
Akun sosmed: @DnarMasID
`, event.Date, priceTable, trendEmoji, changeStr, event.Trend)

	switch platform {
	case models.PlatformInstagram:
		return fmt.Sprintf(`Buat caption Instagram dalam Bahasa Indonesia yang menarik dan informatif tentang harga emas Antam.
Format: caption pendek-sedang (max 2200 karakter), emoji yang relevan, 15-20 hashtag populer di akhir.
Gaya: edukasi investasi, friendly, modern.
Sertakan CTA: "Follow @DnarMasID untuk update harga emas setiap hari".
%s`, baseInfo)

	case models.PlatformTwitter:
		return fmt.Sprintf(`Buat thread Twitter/X dalam Bahasa Indonesia tentang harga emas Antam.
Format: 5-7 tweet, setiap tweet max 280 karakter, numbering 1/7, 2/7, dst.
Tweet pertama: hook menarik dengan data utama.
Tweet terakhir: CTA follow @DnarMasID.
%s`, baseInfo)

	case models.PlatformFacebook:
		return fmt.Sprintf(`Buat post Facebook dalam Bahasa Indonesia yang informatif dan engaging tentang harga emas Antam.
Format: paragraf panjang (500-800 kata), bisa pakai emoji, tabel data, analisis singkat.
Gaya: edukatif, cocok untuk semua umur.
Akhiri dengan: "Ikuti halaman DnarMasID untuk info investasi emas harian."
%s`, baseInfo)

	case models.PlatformThreads:
		return fmt.Sprintf(`Buat post Threads dalam Bahasa Indonesia yang singkat dan menarik tentang harga emas Antam.
Format: 3-5 post berantai, setiap post max 500 karakter, casual dan conversational.
Sertakan emoji yang relevan.
%s`, baseInfo)

	case models.PlatformYouTube:
		return fmt.Sprintf(`Buat title dan deskripsi YouTube dalam Bahasa Indonesia untuk video update harga emas Antam.
Format:
TITLE: (max 100 karakter, SEO-friendly, clickbait positif)
DESKRIPSI:
- Paragraf intro (2-3 kalimat)
- Tabel harga lengkap
- Analisis singkat
- Timestamps (00:00 Intro, 00:30 Harga Emas, dst)
- Subscribe @DnarMasID
- Hashtag: #HargaEmas #Antam #InvestasiEmas
%s`, baseInfo)

	case models.PlatformTikTok:
		return fmt.Sprintf(`Buat caption TikTok dalam Bahasa Indonesia yang singkat dan viral tentang harga emas Antam.
Format: max 300 karakter, energik, pakai trending emoji, 5-8 hashtag TikTok populer.
Gaya: Gen Z friendly, FOMO marketing positif.
%s`, baseInfo)
	}

	return ""
}

func (g *ContentGenerator) generateAnalysis(event *models.GoldScrapedEvent) (string, error) {
	priceTable := formatPriceTable(event.Prices)
	prompt := fmt.Sprintf(`Berikan analisis singkat harga emas Antam dalam Bahasa Indonesia (max 300 kata).
Sertakan: interpretasi tren, faktor yang mungkin mempengaruhi, saran singkat untuk investor.
Gaya: profesional tapi mudah dipahami.

Data: %s
Perubahan: %+.2f%% (Rp %+d)
Tren: %s`, priceTable, event.ChangePct, event.ChangeAmt, event.Trend)

	return g.callAnthropic(prompt)
}

// callAnthropic memanggil Anthropic API
func (g *ContentGenerator) callAnthropic(prompt string) (string, error) {
	reqBody := map[string]any{
		"model":      g.cfg.AnthropicModel,
		"max_tokens": 1024,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", g.cfg.AnthropicAPIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal error: %w", err)
	}

	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response from Anthropic")
	}

	return result.Content[0].Text, nil
}

// ─────────────────────────────────────────
// Fallback content jika API gagal
// ─────────────────────────────────────────

func (g *ContentGenerator) fallbackContent(platform models.Platform, event *models.GoldScrapedEvent) string {
	trendEmoji := trendEmoji(event.Trend)
	return fmt.Sprintf("📊 Update Harga Emas Antam %s\n%s\nPerubahan: %s\n\nFollow @DnarMasID",
		event.Date, trendEmoji, formatChange(event.ChangeAmt, event.ChangePct, event.Trend))
}

func (g *ContentGenerator) fallbackAnalysis(event *models.GoldScrapedEvent) string {
	return fmt.Sprintf("Harga emas Antam pada %s menunjukkan tren %s dengan perubahan %+.2f%%.",
		event.Date, event.Trend, event.ChangePct)
}

// ─────────────────────────────────────────
// Helper formatting
// ─────────────────────────────────────────

func formatPriceTable(prices []models.GoldPrice) string {
	var sb strings.Builder
	for _, p := range prices {
		sb.WriteString(fmt.Sprintf("• %.1f gram: Rp %s (jual: Rp %s)\n",
			p.Gram, formatRupiah(p.BuyPrice), formatRupiah(p.SellPrice)))
	}
	return sb.String()
}

func formatRupiah(amount int64) string {
	s := fmt.Sprintf("%d", amount)
	result := ""
	for i, c := range reverse(s) {
		if i > 0 && i%3 == 0 {
			result = "." + result
		}
		result = string(c) + result
	}
	return result
}

func reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func formatChange(amt int64, pct float64, trend string) string {
	sign := "+"
	if amt < 0 {
		sign = ""
	}
	return fmt.Sprintf("%sRp %s (%s%.2f%%)", sign, formatRupiah(amt), sign, pct)
}

func trendEmoji(trend string) string {
	switch trend {
	case "up":
		return "📈"
	case "down":
		return "📉"
	default:
		return "➡️"
	}
}
