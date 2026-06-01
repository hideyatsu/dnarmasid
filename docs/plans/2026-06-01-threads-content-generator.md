# Threads Content Generator — Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Generate varied, anti-ban-safe Threads content from gold price data and save to DB for review. No auto-publish in Phase 1.

**Architecture:** Extend existing `ai-generator` service to produce Threads-specific content alongside the unified caption. Content type rotates by day-of-week to ensure variety (anti-spam strategy). All content saved to `generated_contents` table with `platform=threads`. Phase 2 (auto-publish) extends this later.

**Tech Stack:** Go, GORM, Redis queue, existing Ollama/Gemini AI providers

---

## Context & Anti-Ban Strategy

Based on research (Meta enforcement patterns, developer reports):
- **Repetitive content** = instant spam flag
- **Fixed timing** = bot fingerprint
- **No engagement activity** = behavioral bot signal
- **External links in every post** = reach reduction + spam signal

**Content rotation by day-of-week:**

| Day | Type | Description |
|-----|------|-------------|
| Monday | `price_update` | Harga + analisis singkat tren mingguan |
| Tuesday | `tip` | Tips investasi emas / edukasi |
| Wednesday | `engagement` | Poll/pertanyaan untuk audience |
| Thursday | `fun_fact` | Fakta menarik sejarah emas |
| Friday | `insight` | Deep market insight + CTA soft |
| Saturday | `motivation` | Quote/motivasi investasi |
| Sunday | `weekly_recap` | Rekap mingguan + engagement question |

**Key rules embedded in AI prompts:**
- No external links in post body (CTA → "cek bio")
- Varied opening hooks (no template repetition)
- Max 2-3 hashtags (not 10+)
- Conversational tone, not robotic
- Each post self-contained (no "update harga" template)

---

## Task 1: Add `ThreadType` constants and extend model

**Objective:** Define thread content type constants and add `thread_type` column to `GeneratedContent` model for categorizing Threads posts.

**Files:**
- Modify: `shared/models/models.go`

**Step 1: Add ThreadType constants**

In `shared/models/models.go`, after the `ContentType` constants block (after line 67), add:

```go
// ThreadType — kategori konten Threads (rotasi harian anti-ban)
type ThreadType string

const (
	ThreadPriceUpdate ThreadType = "price_update"
	ThreadTip         ThreadType = "tip"
	ThreadEngagement  ThreadType = "engagement"
	ThreadFunFact     ThreadType = "fun_fact"
	ThreadInsight     ThreadType = "insight"
	ThreadMotivation  ThreadType = "motivation"
	ThreadWeeklyRecap ThreadType = "weekly_recap"
)
```

**Step 2: Add `ThreadType` field to `GeneratedContent`**

In `GeneratedContent` struct, add after `ContentType` field:

```go
ThreadType  ThreadType  `json:"thread_type,omitempty"`
```

Full struct becomes:

```go
type GeneratedContent struct {
	ID          uint        `gorm:"primarykey" json:"id"`
	PriceID     uint        `json:"price_id"`
	Platform    Platform    `json:"platform"`
	ContentType ContentType `json:"content_type"`
	ThreadType  ThreadType  `json:"thread_type,omitempty"`
	ContentText string      `gorm:"type:longtext" json:"content_text"`
	Status      string      `gorm:"default:pending" json:"status"`
	CreatedAt   time.Time   `json:"created_at"`
}
```

**Step 3: Verify compilation**

Run: `cd /mnt/staging/dnarmasid && go build ./shared/models/`
Expected: No errors

**Step 4: Commit**

```bash
cd /mnt/staging/dnarmasid
git add shared/models/models.go
git commit -m "feat(threads): add ThreadType constants and extend GeneratedContent model"
```

---

## Task 2: Add Threads-specific Redis queue key

**Objective:** Add dedicated Redis queue key for Threads content generation, so scraper can trigger it separately.

**Files:**
- Modify: `shared/queue/redis.go`

**Step 1: Add queue key constant**

In `shared/queue/redis.go`, add after `KeyMediaGenerationCompleted` (line 25):

```go
KeyGoldScrapedThreads = "gold.scraped.threads" // scraper → ai-generator (threads)
```

**Step 2: Verify compilation**

Run: `cd /mnt/staging/dnarmasid && go build ./shared/queue/`
Expected: No errors

**Step 3: Commit**

```bash
git add shared/queue/redis.go
git commit -m "feat(threads): add gold.scraped.threads queue key"
```

---

## Task 3: Extend scraper to publish to threads queue

**Objective:** After successful scrape, also publish `GoldScrapedEvent` to `gold.scraped.threads` queue.

**Files:**
- Modify: `services/scraper/main.go`

**Step 1: Find the publish section**

In scraper main.go, find where `gold.scraped.ai`, `gold.scraped.media`, and `gold.scraped.telegram` are published. Add one more publish call:

```go
// Publish to threads content generator
if err := q.Publish(queue.KeyGoldScrapedThreads, scrapedEvent); err != nil {
    log.Printf("[scraper] ❌ Failed to publish to %s: %v", queue.KeyGoldScrapedThreads, err)
}
```

**Step 2: Verify compilation**

Run: `cd /mnt/staging/dnarmasid && go build ./services/scraper/`
Expected: No errors

**Step 3: Commit**

```bash
git add services/scraper/main.go
git commit -m "feat(threads): scraper publishes to gold.scraped.threads queue"
```

---

## Task 4: Create Threads content generator module

**Objective:** Create `threads.go` in ai-generator with day-of-week rotation logic and varied AI prompts for each thread type.

**Files:**
- Create: `services/ai-generator/threads.go`

**Step 1: Create threads.go**

```go
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

	base := fmt.Sprintf(`You are a social media content writer for Threads (Meta's text platform).
Write ONE post in INDONESIAN language. NO markdown formatting (no **bold**, no _italic_).
NO external links or URLs in the post body. If mentioning a product/service, say "cek bio" instead.
Max 2-3 relevant hashtags at the end. Tone: conversational, engaging, human.
DO NOT start with "Harga Emas" or any template-like opening. Be creative and varied.

DATA:
Tanggal: %s
Harga 1gr: Rp %s (%s Rp %s)
Buyback 1gr: Rp %s
Trend: %s
`, event.Date, formatRupiah(p1g.BuyPrice), trendEmoji(event.Trend),
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
	switch tt {
	case models.ThreadPriceUpdate:
		return fmt.Sprintf("Emas Antam hari ini Rp %s/gr (%s). %s dari kemarin. Pantau terus pergerakan harga, follow buat update harian!\n\n#EmasAntam #Investasi", formatRupiah(getPrice(event, 1).BuyPrice), formatRupiah(event.ChangeAmt), event.Trend)
	case models.ThreadTip:
		return "Tips investasi emas: jangan tunggu harga turun sempurna. Beli rutin (DCA) lebih aman dari timing market. Mulai dari 1 gram aja udah bisa.\n\n#TipsInvestasi #Emas"
	case models.ThreadEngagement:
		return fmt.Sprintf("Emas hari ini %s ke Rp %s/gr. Kalau kamu punya budget 5 juta sekarang, beli emas langsung atau nunggu turun dulu? Reply ya!\n\n#EmasAntam", event.Trend, formatRupiah(getPrice(event, 1).BuyPrice))
	case models.ThreadFunFact:
		return "Fun fact: semua emas yang pernah ditambang manusia bisa muat dalam kubus 22 meter. Tapi nilainya? Triliunan dolar. Langka = berharga.\n\n#FaktaEmas"
	case models.ThreadInsight:
		return fmt.Sprintf("Trend emas %s: Rp %s/gr. Pergerakan ini dipengaruhi banyak faktor global — USD, geopolitik, inflasi. Emas bukan cuma logam, tapi safe haven.\n\n#MarketInsight #Emas", event.Trend, formatRupiah(getPrice(event, 1).BuyPrice))
	case models.ThreadMotivation:
		return "\"The best time to plant a tree was 20 years ago. The second best time is now.\" — sama kayak investasi emas. Mulai aja dulu.\n\n#MotivasiInvestasi"
	case models.ThreadWeeklyRecap:
		return fmt.Sprintf("Rekap minggu ini: emas %s ke Rp %s/gr. Minggu depan tetap pantau, volatilitas masih tinggi. Gimana strategi kamu minggu ini?\n\n#RekapMingguan #Emas", event.Trend, formatRupiah(getPrice(event, 1).BuyPrice))
	default:
		return fmt.Sprintf("Update emas %s: Rp %s/gr. Follow buat info investasi emas harian.\n\n#EmasAntam", event.Date, formatRupiah(getPrice(event, 1).BuyPrice))
	}
}

// cleanMarkdown strips common markdown formatting
func cleanMarkdown(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "##", "")
	s = strings.ReplaceAll(s, "###", "")
	// Remove leading/trailing whitespace per line
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
```

**Step 2: Verify compilation**

Run: `cd /mnt/staging/dnarmasid && go build ./services/ai-generator/`
Expected: No errors

**Step 3: Commit**

```bash
git add services/ai-generator/threads.go
git commit -m "feat(threads): add threads content generator with day-of-week rotation"
```

---

## Task 5: Wire threads generator into ai-generator main loop

**Objective:** Make ai-generator consume `gold.scraped.threads` events and call `GenerateThreads()`.

**Files:**
- Modify: `services/ai-generator/main.go`

**Step 1: Add second goroutine consumer**

In `services/ai-generator/main.go`, after the existing for-loop, restructure to run both consumers concurrently:

Replace the main for-loop (lines 32-59) with:

```go
	// Consumer 1: General caption (existing)
	go func() {
		log.Printf("[ai-generator] ✅ Ready. Waiting for %s events...", queue.KeyGoldScrapedAI)
		for {
			var event models.GoldScrapedEvent
			err := q.ConsumeJSON(queue.KeyGoldScrapedAI, 5*time.Second, &event)
			if err != nil {
				continue
			}

			log.Printf("[ai-generator] 📥 Event received: date=%s trend=%s", event.Date, event.Trend)

			contentEvent, err := generator.Generate(&event)
			if err != nil {
				log.Printf("[ai-generator] ❌ Generate failed: %v", err)
				continue
			}

			if err := q.Publish(queue.KeyContentReady, contentEvent); err != nil {
				log.Printf("[ai-generator] ❌ Failed to publish content.ready: %v", err)
				continue
			}

			log.Printf("[ai-generator] ✅ content.ready published for %s", event.Date)
		}
	}()

	// Consumer 2: Threads content (new)
	go func() {
		log.Printf("[ai-generator] 🧵 Ready. Waiting for %s events...", queue.KeyGoldScrapedThreads)
		for {
			var event models.GoldScrapedEvent
			err := q.ConsumeJSON(queue.KeyGoldScrapedThreads, 5*time.Second, &event)
			if err != nil {
				continue
			}

			log.Printf("[ai-generator] 🧵 Threads event received: date=%s trend=%s", event.Date, event.Trend)

			if err := generator.GenerateThreads(&event); err != nil {
				log.Printf("[ai-generator] ❌ Threads generate failed: %v", err)
				continue
			}
		}
	}()

	// Block until shutdown signal
	<-quit
	log.Println("[ai-generator] Shutting down...")
```

**Step 2: Verify compilation**

Run: `cd /mnt/staging/dnarmasid && go build ./services/ai-generator/`
Expected: No errors

**Step 3: Commit**

```bash
git add services/ai-generator/main.go
git commit -m "feat(threads): wire threads consumer into ai-generator main loop"
```

---

## Task 6: Add Telegram bot command to review threads content

**Objective:** Add `/threads` admin-only command to list pending threads content and preview individual posts.

**Files:**
- Modify: `services/telegram-bot/subscriber.go`

**Step 1: Add `handleThreads` method to CommandHandler**

In `subscriber.go`, add these two methods after `handleScrape` (after line 183):

```go
func (h *CommandHandler) handleThreads(chatID int64, args string) {
	if chatID != h.cfg.TelegramAdminChatID {
		h.send(chatID, "❌ Maaf, command ini hanya untuk Admin.")
		return
	}

	// If args is a number, show detail
	if args != "" {
		num, err := strconv.Atoi(args)
		if err == nil {
			h.handleThreadsDetail(chatID, num)
			return
		}
	}

	// List pending threads
	var contents []models.GeneratedContent
	h.db.Where("platform = ? AND status = ?", models.PlatformThreads, "pending").
		Order("created_at DESC").
		Limit(10).
		Find(&contents)

	if len(contents) == 0 {
		h.send(chatID, "🧵 Belum ada konten Threads pending.")
		return
	}

	var sb strings.Builder
	sb.WriteString("🧵 *Konten Threads Pending:*\n\n")

	for i, c := range contents {
		date := c.CreatedAt.Format("02 Jan")
		preview := c.ContentText
		if len([]rune(preview)) > 60 {
			preview = string([]rune(preview)[:60]) + "..."
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] *%s*\n   %s\n\n", i+1, date, c.ThreadType, preview))
	}

	sb.WriteString("Ketik `/threads <nomor>` untuk lihat full konten.")

	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = "Markdown"
	h.bot.Send(msg)
}

func (h *CommandHandler) handleThreadsDetail(chatID int64, num int) {
	var contents []models.GeneratedContent
	h.db.Where("platform = ? AND status = ?", models.PlatformThreads, "pending").
		Order("created_at DESC").
		Limit(10).
		Find(&contents)

	if num < 1 || num > len(contents) {
		h.send(chatID, "❌ Nomor tidak valid.")
		return
	}

	c := contents[num-1]
	text := fmt.Sprintf("🧵 *Threads [%s]* — %s\n\n%s\n\n---\nStatus: %s | Created: %s",
		c.ThreadType, c.CreatedAt.Format("02 Jan 2006 15:04"),
		c.ContentText, c.Status, c.CreatedAt.Format("02 Jan 2006"))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	h.bot.Send(msg)
}
```

**Step 2: Add imports**

Ensure `subscriber.go` imports include `"fmt"`, `"strings"`, `"strconv"` (add to existing imports).

**Step 3: Wire command into message router**

In `handleMessage` switch block (line 59-76), add before `default`:

```go
case "threads":
    h.handleThreads(chatID, msg.CommandArguments())
```

**Step 4: Add to help text**

In `handleHelp`, add to admin commands section:

```go
text += "/threads — [Admin] Review pending Threads content\n"
```

**Step 5: Verify compilation**

Run: `cd /mnt/staging/dnarmasid && go build ./services/telegram-bot/`
Expected: No errors

**Step 6: Commit**

```bash
git add services/telegram-bot/subscriber.go
git commit -m "feat(threads): add /threads admin command to review pending content"
```

---

## Task 7: Manual test with dummy data

**Objective:** Verify end-to-end flow by triggering pipeline with dummy data and checking DB + Telegram bot.

**Step 1: Rebuild staging**

```bash
cd /mnt/staging/dnarmasid
docker compose up -d --build
```

**Step 2: Trigger dummy pipeline**

```bash
docker exec dnarmasid-redis redis-cli LPUSH "job.scrape" '{"mode":"dummy"}'
```

**Step 3: Verify threads content in DB**

```bash
MYSQL_PASS=$(docker exec dnarmasid-scraper printenv MYSQL_PASSWORD)
docker exec dnarmasid-mysql mysql -h 127.0.0.1 -u dnarmasid -p"$MYSQL_PASS" dnarmasid_db -e "
  SELECT id, platform, content_type, thread_type, LEFT(content_text, 80) as preview, status, created_at
  FROM generated_contents
  WHERE platform = 'threads'
  ORDER BY created_at DESC
  LIMIT 10;" 2>&1 | grep -v Warning
```

Expected: 1 new row with platform=threads, thread_type matching today's weekday

**Step 4: Verify ai-generator logs**

```bash
docker logs --tail 30 dnarmasid-ai-generator 2>&1 | grep -i threads
```

Expected: `🧵 Generating threads content: type=...` and `✅ Threads content saved`

**Step 5: Test Telegram bot `/threads` command**

Send `/threads` to the bot and verify it lists the pending content.

**Step 6: Commit (if any fixes needed)**

---

## Task 8: Update dnarmasid-pipeline skill

**Objective:** Document the new threads pipeline stage and queue key in the skill.

**Files:**
- Update: skill `dnarmasid-pipeline`

After implementation, patch the skill with new queue key and threads flow.

---

## Phase 2 Checklist (Future — After Account Warm-up)

When Tuan is ready to auto-publish:

- [ ] Add `scheduled_at` and `published_at` timestamps to `generated_contents`
- [ ] Add `threads_account_id` to config
- [ ] Add Repliz Threads platform target (or direct Threads API)
- [ ] Implement timing variation (±30-60min random from base time)
- [ ] Add publish status tracking (pending → scheduled → published → failed)
- [ ] Add retry logic for failed publishes
- [ ] Add account rotation support (multiple Threads accounts)
- [ ] Limit link posts: only 1 in 5-7 posts includes CTA link

---

## Rollback

If issues arise, revert the scraper publish (Task 3) to stop threads content generation without affecting existing pipeline.
