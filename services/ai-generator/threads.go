package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"dnarmasid/shared/models"
)

// ThreadTypeForDay returns the thread content type based on day-of-week
func ThreadTypeForDay(t time.Time) models.ThreadType {
	switch t.Weekday() {
	case time.Monday:
		return models.ThreadPriceUpdate
	case time.Tuesday:
		return models.ThreadTip
	case time.Wednesday:
		return models.ThreadEngagement
	case time.Thursday:
		return models.ThreadFunFact
	case time.Friday:
		return models.ThreadInsight
	case time.Saturday:
		return models.ThreadMotivation
	case time.Sunday:
		return models.ThreadWeeklyRecap
	default:
		return models.ThreadPriceUpdate
	}
}

// GenerateThreads generates Threads-specific content and saves to DB
func (g *ContentGenerator) GenerateThreads(event *models.GoldScrapedEvent) error {
	threadType := ThreadTypeForDay(time.Now())
	log.Printf("[ai-generator] 🧵 Generating threads content: type=%s date=%s", threadType, event.Date)

	prompt := g.buildThreadsPrompt(event, threadType)

	var content string
	var err error

	switch g.cfg.AIProvider {
	case "gemini":
		content, err = g.callGemini(prompt)
	case "9router":
		content, err = g.callNineRouter(prompt)
	default:
		content, err = g.callOllama(prompt)
	}

	if err != nil {
		log.Printf("[ai-generator] ⚠️ Threads AI generation failed (%s): %v, using fallback", g.cfg.AIProvider, err)
		content = g.fallbackThreadsContent(event, threadType)
	}

	// Clean: strip markdown if AI sneaks it in
	content = cleanMarkdown(content)

	g.db.Create(&models.GeneratedContent{
		PriceID:     event.PriceID,
		Platform:    models.PlatformThreads,
		ContentType: models.ContentThread,
		ThreadType:  threadType,
		ContentText: content,
		Status:      "pending",
	})

	log.Printf("[ai-generator] ✅ Threads content saved: type=%s date=%s", threadType, event.Date)
	return nil
}

func (g *ContentGenerator) buildThreadsPrompt(event *models.GoldScrapedEvent, tt models.ThreadType) string {
	// Find 1 gram price
	var p1g models.GoldPrice
	for _, p := range event.Prices {
		if p.Gram == 1 {
			p1g = p
			break
		}
	}

	// Verbose mode override
	styleInstr := "Max 2-3 relevant hashtags at the end. Tone: conversational, engaging, human.\nDO NOT start with \"Harga Emas\" or any template-like opening. Be creative and varied.\n"
	if !g.cfg.AICavemanMode {
		styleInstr = "Write a DETAILED post (4-7 sentences). Be thorough and educational in your explanation.\nMax 4-5 relevant hashtags. Tone: conversational, insightful, human — like a market analyst friend.\nDO NOT start with \"Harga Emas\" or any template-like opening. Be creative and varied.\n"
	}

	base := fmt.Sprintf(`You are a social media content writer for Threads (Meta's text platform).
Write ONE post in INDONESIAN language. NO markdown formatting (no **bold**, no _italic_).
NO external links or URLs in the post body. If mentioning a product/service, say "cek bio" instead.
%s
DATA:
Tanggal: %s
Harga 1gr: Rp %s (%s Rp %s)
Buyback 1gr: Rp %s
Trend: %s
`, styleInstr, event.Date, formatRupiah(p1g.BuyPrice), trendEmoji(event.Trend),
		formatRupiah(event.ChangeAmt), formatRupiah(p1g.SellPrice), event.Trend)

	switch tt {
	case models.ThreadPriceUpdate:
		return base + `
TYPE: Price Update with Brief Analysis
Write about today's gold price movement. Include the price data naturally (not as a list).
Add 2-3 sentences of analysis about WHY it moved or what it means for investors.
End with a soft CTA: "follow buat update harian" or similar. DO NOT mention Telegram or bot.`

	case models.ThreadTip:
		return base + `
TYPE: Investment Tip / Education
Use today's price context (trend=` + event.Trend + `) to share ONE practical tip about gold investment.
Examples: when to buy, how to start, common mistakes, spread strategy.
Make it actionable and specific. End with an engagement hook (question or "save ini buat nanti").`

	case models.ThreadEngagement:
		return base + `
TYPE: Engagement / Poll Question
Create a thought-provoking question or mini-poll related to gold/investment.
Reference today's price trend as context. Make people WANT to reply.
Examples: "Kalau punya 10 juta sekarang, beli emas atau...", "Tim beli rutin vs nunggu turun?"
Keep it short and punchy (max 3-4 sentences).`

	case models.ThreadFunFact:
		return base + `
TYPE: Fun Fact / History
Share ONE interesting fact about gold — history, science, culture, or economics.
Connect it loosely to today's market or investing in general.
Make it surprising or "did you know" style. Keep it under 5 sentences.`

	case models.ThreadInsight:
		return base + `
TYPE: Market Insight / Deep Analysis
Provide a deeper market insight. Discuss global factors affecting gold (USD, geopolitics, inflation).
Use today's price as anchor but focus on the BIGGER picture.
End with forward-looking statement. Keep professional but accessible.`

	case models.ThreadMotivation:
		return base + `
TYPE: Investment Motivation / Quote
Share a motivational thought about investing, patience, or financial discipline.
Can be a real quote (Buffett, Munger, etc) or original thought.
Connect it to gold investing context. Keep it warm and encouraging. Short (2-4 sentences).`

	case models.ThreadWeeklyRecap:
		return base + `
TYPE: Weekly Recap
Summarize this week's gold price movement using today's data as reference point.
Mention the trend direction and what it means for the coming week.
End with engagement: ask followers about their week's investment moves.
Keep it conversational, not like a financial report.`

	default:
		return base + `TYPE: General gold investment post. Be creative.`
	}
}

func (g *ContentGenerator) fallbackThreadsContent(event *models.GoldScrapedEvent, tt models.ThreadType) string {
	p := getPrice(event, 1)
	switch tt {
	case models.ThreadPriceUpdate:
		return fmt.Sprintf("Emas Antam hari ini Rp %s/gr (%s). %s dari kemarin. Pantau terus pergerakan harga, follow buat update harian!\n\n#EmasAntam #Investasi", formatRupiah(p.BuyPrice), formatRupiah(event.ChangeAmt), event.Trend)
	case models.ThreadTip:
		return "Tips investasi emas: jangan tunggu harga turun sempurna. Beli rutin (DCA) lebih aman dari timing market. Mulai dari 1 gram aja udah bisa.\n\n#TipsInvestasi #Emas"
	case models.ThreadEngagement:
		return fmt.Sprintf("Emas hari ini %s ke Rp %s/gr. Kalau kamu punya budget 5 juta sekarang, beli emas langsung atau nunggu turun dulu? Reply ya!\n\n#EmasAntam", event.Trend, formatRupiah(p.BuyPrice))
	case models.ThreadFunFact:
		return "Fun fact: semua emas yang pernah ditambang manusia bisa muat dalam kubus 22 meter. Tapi nilainya? Triliunan dolar. Langka = berharga.\n\n#FaktaEmas"
	case models.ThreadInsight:
		return fmt.Sprintf("Trend emas %s: Rp %s/gr. Pergerakan ini dipengaruhi banyak faktor global — USD, geopolitik, inflasi. Emas bukan cuma logam, tapi safe haven.\n\n#MarketInsight #Emas", event.Trend, formatRupiah(p.BuyPrice))
	case models.ThreadMotivation:
		return "\"The best time to plant a tree was 20 years ago. The second best time is now.\" — sama kayak investasi emas. Mulai aja dulu.\n\n#MotivasiInvestasi"
	case models.ThreadWeeklyRecap:
		return fmt.Sprintf("Rekap minggu ini: emas %s ke Rp %s/gr. Minggu depan tetap pantau, volatilitas masih tinggi. Gimana strategi kamu minggu ini?\n\n#RekapMingguan #Emas", event.Trend, formatRupiah(p.BuyPrice))
	default:
		return fmt.Sprintf("Update emas %s: Rp %s/gr. Follow buat info investasi emas harian.\n\n#EmasAntam", event.Date, formatRupiah(p.BuyPrice))
	}
}

// cleanMarkdown strips common markdown formatting
func cleanMarkdown(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "##", "")
	s = strings.ReplaceAll(s, "###", "")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

func getPrice(event *models.GoldScrapedEvent, gram float64) models.GoldPrice {
	for _, p := range event.Prices {
		if p.Gram == gram {
			return p
		}
	}
	return models.GoldPrice{}
}
