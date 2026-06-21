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
)

// NarratorAI generates video narration scripts using AI
type NarratorAI struct {
	cfg *config.Config
}

func NewNarratorAI(cfg *config.Config) * NarratorAI {
	return &NarratorAI{cfg: cfg}
}

// AIContentResponse holds BOTH slide text content AND narration from AI
type AIContentResponse struct {
	Slide struct {
		HookBadge     string `json:"hook_badge"`      // e.g. "📈"
		HookHeadline  string `json:"hook_headline"`   // e.g. "BULLISH RUN!"
		HookSubtitle  string `json:"hook_subtitle"`   // e.g. "Harga Emas Hari Ini"
		InsightBadge  string `json:"insight_badge"`   // e.g. "📊 ANALISIS"
		InsightHeadline string `json:"insight_headline"` // e.g. "Momentum Kuat Terbentuk"
		InsightDetail string `json:"insight_detail"`  // e.g. "3 hari berturut-turut naik, sinyal beli masih aktif"
	} `json:"slide"`
	Narration struct {
		Hook    string `json:"hook"`
		Price   string `json:"price"`
		Insight string `json:"insight"`
		CTA     string `json:"cta"`
	} `json:"narration"`
}

// ScriptSegments holds 4 AI-generated narration segments (for backward compat)
type ScriptSegments struct {
	Hook    string `json:"hook"`
	Price   string `json:"price"`
	Insight string `json:"insight"`
	CTA     string `json:"cta"`
}

// GenerateContent produces both slide content and narration from AI
func (n *NarratorAI) GenerateContent(data NarratorData) (*AIContentResponse, error) {
	prompt := n.buildContentPrompt(data)

	var content string
	var err error

	switch n.cfg.AIProvider {
	case "gemini":
		content, err = n.callGemini(prompt)
	case "ollama":
		content, err = n.callOllama(prompt)
	case "9router":
		fallthrough
	default:
		content, err = n.callNineRouter(prompt)
	}

	if err != nil {
		return nil, fmt.Errorf("AI provider (%s): %w", n.cfg.AIProvider, err)
	}

	resp, err := parseContentResponse(content)
	if err != nil {
		return nil, fmt.Errorf("parse content: %w", err)
	}

	// Validate narration segments
	for name, seg := range map[string]string{
		"hook":    resp.Narration.Hook,
		"price":   resp.Narration.Price,
		"insight": resp.Narration.Insight,
		"cta":     resp.Narration.CTA,
	} {
		cleaned := strings.TrimSpace(seg)
		if len(cleaned) < 10 {
			return nil, fmt.Errorf("AI segment '%s' too short (%d chars): %s", name, len(cleaned), cleaned)
		}
	}

	// Validate slide content
	if resp.Slide.HookBadge == "" {
		resp.Slide.HookBadge = "📊"
	}
	if resp.Slide.HookHeadline == "" {
		resp.Slide.HookHeadline = "UPDATE HARGA EMAS"
	}

	log.Printf("[ai_narrator] ✅ Generated content: hook=%d price=%d insight=%d cta=%d chars",
		len(resp.Narration.Hook), len(resp.Narration.Price), len(resp.Narration.Insight), len(resp.Narration.CTA))

	return resp, nil
}

// GenerateSegments produces 4 separate narration scripts (backward compat, wraps GenerateContent)
func (n *NarratorAI) GenerateSegments(data NarratorData) (*ScriptSegments, error) {
	resp, err := n.GenerateContent(data)
	if err != nil {
		return nil, err
	}
	return &ScriptSegments{
		Hook:    resp.Narration.Hook,
		Price:   resp.Narration.Price,
		Insight: resp.Narration.Insight,
		CTA:     resp.Narration.CTA,
	}, nil
}

// GenerateScript produces a single narration (legacy, kept for backward compat)
func (n *NarratorAI) GenerateScript(data NarratorData) (string, error) {
	segments, err := n.GenerateSegments(data)
	if err != nil {
		return "", err
	}
	return segments.Hook + " " + segments.Price + " " + segments.Insight + " " + segments.CTA, nil
}

// parseContentResponse extracts AIContentResponse from AI JSON response
func parseContentResponse(raw string) (*AIContentResponse, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.Trim(cleaned, "\"'`")
	if strings.HasPrefix(cleaned, "```") {
		lines := strings.Split(cleaned, "\n")
		if len(lines) >= 2 {
			start := 1
			end := len(lines)
			if strings.HasPrefix(lines[end-1], "```") {
				end--
			}
			cleaned = strings.Join(lines[start:end], "\n")
		}
	}
	cleaned = strings.TrimSpace(cleaned)

	var resp AIContentResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w\nRaw response: %s", err, raw)
	}
	return &resp, nil
}

// parseSegmentsResponse extracts ScriptSegments from AI JSON response (legacy parser)
func parseSegmentsResponse(raw string) (*ScriptSegments, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.Trim(cleaned, "\"'`")
	if strings.HasPrefix(cleaned, "```") {
		lines := strings.Split(cleaned, "\n")
		if len(lines) >= 2 {
			start := 1
			end := len(lines)
			if strings.HasPrefix(lines[end-1], "```") {
				end--
			}
			cleaned = strings.Join(lines[start:end], "\n")
		}
	}
	cleaned = strings.TrimSpace(cleaned)

	var segments ScriptSegments
	if err := json.Unmarshal([]byte(cleaned), &segments); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w\nRaw response: %s", err, raw)
	}

	return &segments, nil
}

// NarratorData holds all context needed for the AI prompt
type NarratorData struct {
	Date           string
	HargaJual      int64
	HargaBuyback   int64
	SpreadPct      float64
	DeltaJual      int64
	Trend7d        string // "up" | "down" | "sideways"
	Streak         int
	Condition      int
	ConditionLabel string
	Volatility     float64
	PriceHistory   string // "7 hari: 1.450.000 → 1.460.000 → 1.470.000"
}

func (n *NarratorAI) buildContentPrompt(d NarratorData) string {
	return fmt.Sprintf(`Buatkan konten visual slide DAN narasi voice-over untuk video pendek harga emas Antam hari ini dalam BAHASA INDONESIA yang natural dan santai.

ATURAN KRITIS:
1. Output HANYA JSON sesuai format di bawah.
2. Gaya bahasa narasi: seperti teman ngobrol, BUKAN pembaca berita.
3. Gunakan kata percakapan: "nih", "ya", "lho", "dong", "kok", "banget", "udah", "sih".
4. JANGAN gunakan tanda seru berlebihan (maksimal 1 per segmen).
5. Gunakan koma (,) untuk jeda natural, titik (.) HANYA di akhir kalimat.
6. Angka desimal pakai koma (contoh: "9,5 persen").
7. Angka ribuan pakai titik (contoh: "1.500.000").
8. JANGAN gunakan kata: "selamat datang", "halo", "hai", "pemirsa", "sobat", "oke", "baik", "nah", "jadi".
9. JANGAN gunakan kata penanda transisi eksplisit di awal kalimat.
10. Setiap segmen narasi harus berdiri sendiri tapi mengalir jika digabungkan.

BAGIAN 1 — KONTEN SLIDE (teks yang muncul di video):
- hook_badge: emoji tunggal yang menggambarkan kondisi market (contoh: "📈", "⬇️", "⚠️", "💎", "🔥")
- hook_headline: headline singkat 2-4 kata (contoh: "BULLISH RUN!", "AKUMULASI?", "SPREAD LEBAR")
- hook_subtitle: subtitle 2-4 kata (contoh: "Harga Emas Hari Ini")
- insight_badge: badge insight dengan emoji (contoh: "📊 MOMENTUM KUAT", "⚠️ HOLD DULU")
- insight_headline: headline insight 3-5 kata (contoh: "Tren Positif Terbentuk", "Spread Melebar, Tunggu Dulu")
- insight_detail: penjelasan detail 1-2 kalimat (contoh: "3 hari berturut-turut naik, sinyal beli masih aktif")
PENTING: Konten slide harus akurat sesuai data market, bukan generic filler.

BAGIAN 2 — NARASI VOICE-OVER:
PANJANG:
- hook: 20-30 kata (5-7 detik) — pembuka yang bikin penasaran
- price: 25-35 kata (7-10 detik) — baca harga dengan natural
- insight: 30-40 kata (8-12 detik) — edukasi strategi, rekomendasi actionable
- cta: 20-30 kata (5-7 detik) — ajakan pakai bot, JANGAN sebut "bot DNARMASID", cukup "bot kami"

KONTEKS NARASI PER SEGMENT:
- hook: Singgung kondisi market, bikin penasaran tanpa spoil detail
- price: Baca harga jual Rp %s/gram, buyback Rp %s/gram, spread %.1f%% dengan natural
- insight: Jelaskan strategi berdasarkan spread, trend, dan kondisi. Berikan rekomendasi (beli/jual/tunggu)
- cta: Ajak penonton menggunakan bot kami untuk update real-time, sebut link di bio

DATA HARI INI:
- Tanggal: %s
- Harga jual: Rp %s per gram
- Harga buyback: Rp %s per gram
- Spread: %.1f%%
- Perubahan dari kemarin: Rp %s (%s)
- Tren 7 hari: %s (streak %d hari)
- Kondisi pasar: %s
- Volatilitas: %.2f
- Riwayat harga 7 hari: %s

FORMAT OUTPUT:
{
  "slide": {
    "hook_badge": "emoji",
    "hook_headline": "HEADLINE",
    "hook_subtitle": "Subtitle Text",
    "insight_badge": "📊 BADGE TEXT",
    "insight_headline": "Headline Insight",
    "insight_detail": "Detail insight di sini"
  },
  "narration": {
    "hook": "narasi hook",
    "price": "narasi price",
    "insight": "narasi insight",
    "cta": "narasi cta"
  }
}

HANYA output JSON, tanpa penjelasan.`,
		formatRupiah(d.HargaJual), formatRupiah(d.HargaBuyback), d.SpreadPct,
		d.Date,
		formatRupiah(d.HargaJual),
		formatRupiah(d.HargaBuyback),
		d.SpreadPct,
		formatRupiah(abs64(d.DeltaJual)), deltaDir(d.DeltaJual),
		d.Trend7d, d.Streak,
		d.ConditionLabel,
		d.Volatility,
		d.PriceHistory,
	)
}

// buildPrompt builds the legacy single-paragraph prompt (kept for reference)
func (n *NarratorAI) buildPrompt(d NarratorData) string {
	return n.buildSegmentedPrompt(d)
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func deltaDir(delta int64) string {
	if delta > 0 {
		return "naik"
	} else if delta < 0 {
		return "turun"
	}
	return "stabil"
}

// ─── AI Provider Clients ───

func (n *NarratorAI) callNineRouter(prompt string) (string, error) {
	reqBody := map[string]any{
		"model": n.cfg.NineRouterModel,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens":  600,
		"temperature": 0.8,
		"stream":      false,
	}

	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", n.cfg.NineRouterHost+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	if n.cfg.NineRouterAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+n.cfg.NineRouterAPIKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("9router HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response from 9router")
	}

	return result.Choices[0].Message.Content, nil
}

func (n *NarratorAI) callGemini(prompt string) (string, error) {
	if n.cfg.GeminiAPIKey == "" {
		return "", fmt.Errorf("Gemini API Key is not configured")
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		n.cfg.GeminiModel, n.cfg.GeminiAPIKey)

	reqBody := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]string{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"temperature":    0.8,
			"maxOutputTokens": 600,
		},
	}

	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal: %w", err)
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from Gemini")
	}

	return result.Candidates[0].Content.Parts[0].Text, nil
}

func (n *NarratorAI) callOllama(prompt string) (string, error) {
	reqBody := map[string]any{
		"model":  n.cfg.OllamaModel,
		"prompt": prompt,
		"stream": false,
	}

	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", n.cfg.OllamaHost+"/api/generate", bytes.NewReader(body))
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
		return "", fmt.Errorf("unmarshal: %w", err)
	}

	if result.Response == "" {
		return "", fmt.Errorf("empty response from Ollama")
	}

	return result.Response, nil
}

// conditionLabel maps condition int to human-readable label
func conditionLabel(cond int) string {
	switch cond {
	case 1:
		return "bullish kuat"
	case 2:
		return "bullish moderat"
	case 3:
		return "bearish streak"
	case 4:
		return "bearish, harga rendah"
	case 5:
		return "spread lebar, volatilitas tinggi"
	case 6:
		return "spread lebar, pasar tenang"
	case 7:
		return "spread tipis, tren naik"
	case 8:
		return "spread tipis, tren turun"
	case 9:
		return "mendekati level tertinggi"
	case 10:
		return "pasar stabil"
	default:
		return "netral"
	}
}

// buildPriceHistory formats last 7 days prices for the prompt
func buildPriceHistory(prices []int64) string {
	if len(prices) == 0 {
		return "data tidak tersedia"
	}

	var parts []string
	for _, p := range prices {
		parts = append(parts, fmt.Sprintf("Rp %s", formatRupiah(p)))
	}

	return strings.Join(parts, " → ")
}
