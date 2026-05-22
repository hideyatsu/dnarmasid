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

// Generate membuat satu caption tunggal sesuai template
func (g *ContentGenerator) Generate(event *models.GoldScrapedEvent) (*models.ContentReadyEvent, error) {
	log.Printf("[ai-generator] Generating unified caption for %s...", event.Date)

	// Ambil data 1 gram
	var p1g models.GoldPrice
	for _, p := range event.Prices {
		if p.Gram == 1 {
			p1g = p
			break
		}
	}

	if p1g.BuyPrice == 0 {
		return nil, fmt.Errorf("data harga 1 gram tidak ditemukan")
	}

	// Hitung Spread
	spreadAmt := p1g.BuyPrice - p1g.SellPrice
	spreadPct := (float64(spreadAmt) / float64(p1g.BuyPrice)) * 100

	// Bangun prompt dengan data yang sudah ada
	prompt := g.buildUnifiedPrompt(event, p1g, spreadAmt, spreadPct)

	// Call AI Provider
	var content string
	var err error

	switch g.cfg.AIProvider {
	case "gemini":
		content, err = g.callGemini(prompt)
	case "ollama":
		fallthrough
	default:
		content, err = g.callOllama(prompt)
	}

	if err != nil {
		log.Printf("[ai-generator] ⚠️ AI Generation failed (%s): %v", g.cfg.AIProvider, err)
		content = g.fallbackUnifiedContent(event, p1g, spreadAmt, spreadPct)
	}

	// Simpan ke DB (sebagai general)
	g.db.Create(&models.GeneratedContent{
		PriceID:     event.PriceID,
		Platform:    models.PlatformGeneral,
		ContentType: models.ContentCaption,
		ContentText: content,
		Status:      "pending",
	})

	return &models.ContentReadyEvent{
		PriceID:  event.PriceID,
		Date:     event.Date,
		Contents: map[models.Platform]string{models.PlatformGeneral: content},
		Analysis: "Unified content generated",
	}, nil
}

func (g *ContentGenerator) buildUnifiedPrompt(event *models.GoldScrapedEvent, p1g models.GoldPrice, spread int64, pct float64) string {
	tEmoji := trendEmoji(event.Trend)
	bbTEmoji := trendEmoji(event.BuybackTrend)

	return fmt.Sprintf(`Generate an ENGAGING and EDUCATIONAL Instagram/Social Media caption about Antam gold prices in INDONESIAN language.

CRITICAL INSTRUCTIONS:
1. Use INDONESIAN language for the entire output.
2. DO NOT use any Markdown formatting (no bold **, no italics _, no separators ***). Use plain text only.
3. Strictly follow the provided template structure.

DATA:
Date: %s
Price: Rp %s / gr (%s Rp %s)
Buyback: Rp %s / gr (%s Rp %s)
Spread: Rp %s (%.2f%%)
Trend: %s %s

MANDATORY TEMPLATE (Must be in INDONESIAN, strictly no bold):
Harga Emas Antam Hari Ini

Tanggal: [Date]
Harga: Rp [Price] / gr ([Trend Triangle] Rp [Change Amount])
Buyback: Rp [Buyback] / gr ([Trend Triangle] Rp [Change Amount])
Spread: [Spread]

Trend: [Provide a brief Indonesian market trend summary with emojis]

[Provide 2-3 sentences of INSIGHT/ANALYSIS in INDONESIAN about whether it is a good time to buy/sell based on the data above]

[Create a creative and persuasive Call to Action in INDONESIAN, encouraging users to use our Telegram bot for real-time updates and price alerts by clicking the link in bio]

[Add maximum 5 relevant hashtags in Indonesian]

Tone: Professional, persuasive, and easy to understand.`,
		event.Date,
		formatRupiah(p1g.BuyPrice), tEmoji, formatRupiah(event.ChangeAmt),
		formatRupiah(p1g.SellPrice), bbTEmoji, formatRupiah(event.BuybackChangeAmt),
		formatRupiah(spread), pct, event.Trend, tEmoji)
}

func (g *ContentGenerator) fallbackUnifiedContent(event *models.GoldScrapedEvent, p1g models.GoldPrice, spread int64, pct float64) string {
	tEmoji := trendEmoji(event.Trend)
	return fmt.Sprintf(`Harga Emas Antam Hari Ini

Tanggal: %s
Harga: Rp %s / gr (%s)
Buyback: Rp %s / gr
Spread: Rp %s (%.2f%%)
Trend: %s %s

Harga emas hari ini menunjukkan pergerakan %s. Pantau terus untuk mendapatkan harga terbaik.

Butuh update harga real-time?
Klik link di bio untuk menggunakan bot kami dan pasang Alert Harga agar tidak ketinggalan momentum pasar.

#HargaEmas #Antam #DnarMasID #AntamLogamMulia #HargaEmasHariIni`,
		event.Date, formatRupiah(p1g.BuyPrice), formatChange(event.ChangeAmt, event.ChangePct, event.Trend),
		formatRupiah(p1g.SellPrice), formatRupiah(spread), pct, event.Trend, tEmoji, event.Trend)
}

// callOllama calls the local Ollama API for generating content
func (g *ContentGenerator) callOllama(prompt string) (string, error) {
	reqBody := map[string]any{
		"model":  g.cfg.OllamaModel,
		"prompt": prompt,
		"stream": false,
	}

	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", g.cfg.OllamaHost+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Response string `json:"response"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal error: %w", err)
	}

	if result.Response == "" {
		return "", fmt.Errorf("empty response from Ollama")
	}

	return result.Response, nil
}

// callGemini calls Google Gemini API for generating content
func (g *ContentGenerator) callGemini(prompt string) (string, error) {
	if g.cfg.GeminiAPIKey == "" {
		return "", fmt.Errorf("Gemini API Key is not configured")
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		g.cfg.GeminiModel, g.cfg.GeminiAPIKey)

	reqBody := map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]any{
						"text": prompt,
					},
				},
			},
		},
	}

	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gemini api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("unmarshal error: %w", err)
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from Gemini")
	}

	return result.Candidates[0].Content.Parts[0].Text, nil
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
	return fmt.Sprintf("%s Rp %s", trendEmoji(trend), formatRupiah(amt))
}

func trendEmoji(trend string) string {
	switch strings.ToLower(trend) {
	case "up":
		return "▲"
	case "naik":
		return "▲"
	case "down":
		return "▼"
	case "turun":
		return "▼"
	default:
		return "▬"
	}
}
