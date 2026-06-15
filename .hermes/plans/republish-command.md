# Republish Command Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Add a single `/republish` bot command that re-publishes Antam 1g gold price data end-to-end — fetches latest price + changes from DB, triggers AI caption → media generation → social media upload in one serial flow.

**Architecture:** One new command handler in `pipeline.go`, enhanced `getLatestEvent()` that calculates changes from DB. Leverages existing serial pipeline (`gold.scraped.ai` → AI → `gold.processed` → Media → `media.generation.completed` → Repliz) — no new queue keys needed.

**Key insight:** The replay command reuses the existing `triggerAI` flow but with a properly constructed event (change calculations + 1g-specific data). Existing pipeline handles the rest.

**Tech Stack:** Go, GORM, Redis (LPUSH/BRPOP), Telegram Bot API

---

## Background: What's Missing Today

Current `getLatestEvent()` in `pipeline.go` has gaps:
1. **No change calculation** — `Trend` is hardcoded `"stable"`
2. **No buyback changes** — `BuybackChangeAmt` and `BuybackTrend` are zeroed
3. **No screenshots** — `ScreenshotPriceURL` / `ScreenshotBuybackURL` are empty
4. **All grams** — fetches all gram weights, not only 1g Antam

When `/pipeline ai` is triggered manually, AI generates caption without the actual change data (always says "stable"), making the caption generic.

## What `/republish` Does

```
/republish → getLatestPrice(1g) → getPreviousPrice(1g) → calcChange()
           → buildGoldScrapedEvent (with changes, screenshots from DB if available)
           → publish gold.scraped.ai
           → [Serial Pipeline: AI → Media → Repliz]
           → done
```

### Pipeline Steps (automatic after publish to gold.scraped.ai):
- **AI Generator** picks up event → generates caption with real change data → saves to DB → publishes `gold.processed`
- **Media Generator** picks up `gold.processed` → renders infographic → CTA → hero screenshot → bridging → publishes `media.generation.completed`
- **Repliz Uploader** picks up → posts to Instagram/Facebook

---

### Task 1: Enhance `getLatestEvent()` with change calculation

**Objective:** Add proper change calculation (trend, change_amt, change_pct, buyback_change_amt, buyback_trend) to the event builder.

**Files:**
- Modify: `services/telegram-bot/pipeline.go`

**Step 1: Add `getPreviousPrice` helper**

Add after `formatDate` (line 111) a function to fetch yesterday's 1g price:

```go
// getPreviousPrice fetches the 1g price from the previous day
func (p *PipelineHandler) getPreviousPrice(today time.Time) (*models.GoldPrice, error) {
    var prev models.GoldPrice
    err := p.db.Where("date < ? AND gram = ?", today, 1.0).
        Order("date DESC").
        First(&prev).Error
    if err != nil {
        return nil, err
    }
    return &prev, nil
}
```

**Step 2: Add `calcChange` helper**

Add a change calculation function:

```go
// calcChange computes change metrics between today and yesterday 1g prices
func calcChange(today models.GoldPrice, yesterday *models.GoldPrice) (changePct float64, changeAmt int64, trend string, bbChangeAmt int64, bbTrend string) {
    if yesterday == nil || yesterday.BuyPrice == 0 {
        return 0, 0, "stable", 0, "stable"
    }
    
    changeAmt = today.BuyPrice - yesterday.BuyPrice
    changePct = float64(changeAmt) / float64(yesterday.BuyPrice) * 100
    
    trend = "stable"
    if changeAmt > 0 {
        trend = "up"
    } else if changeAmt < 0 {
        trend = "down"
    }
    
    bbChangeAmt = today.SellPrice - yesterday.SellPrice
    bbTrend = "stable"
    if bbChangeAmt > 0 {
        bbTrend = "up"
    } else if bbChangeAmt < 0 {
        bbTrend = "down"
    }
    
    return
}
```

**Step 3: Enhance `getLatestEvent`**

Modify `getLatestEvent()` to calculate changes:

```go
func (p *PipelineHandler) getLatestEvent() (*models.GoldScrapedEvent, error) {
    // Get today's 1g Antam price
    var today models.GoldPrice
    if err := p.db.Where("gram = ?", 1.0).Order("date DESC").First(&today).Error; err != nil {
        return nil, fmt.Errorf("tidak ada data harga 1g Antam di database: %w", err)
    }
    
    // Get all grams for today
    var prices []models.GoldPrice
    p.db.Where("date = ?", today.Date).Order("gram ASC").Find(&prices)
    
    // Get yesterday's 1g price for change calculation
    yesterday, _ := p.getPreviousPrice(today.Date)
    
    changePct, changeAmt, trend, bbChangeAmt, bbTrend := calcChange(today, yesterday)
    
    dateStr := formatDate(today.Date)
    var updateTimeStr string
    if today.SourceUpdateTime != nil {
        updateTimeStr = formatDate(*today.SourceUpdateTime) + " " + today.SourceUpdateTime.Format("15:04:05")
    }
    
    // Try to get screenshot URLs from R2 if they exist
    screenshotPriceURL := ""
    screenshotBuybackURL := ""
    // Check if screenshots exist in generated_media from previous pipeline run
    var heroMedia models.GeneratedMedia
    if err := p.db.Where("price_id = ? AND file_name LIKE ?", today.ID, "hero_screenshot_%").First(&heroMedia).Error; err == nil {
        screenshotPriceURL = heroMedia.PublicURL
    }
    
    return &models.GoldScrapedEvent{
        Date:                dateStr,
        UpdateTime:          updateTimeStr,
        PriceID:             today.ID,
        Prices:              prices,
        ChangePct:           changePct,
        ChangeAmt:           changeAmt,
        Trend:               trend,
        BuybackChangeAmt:    bbChangeAmt,
        BuybackTrend:        bbTrend,
        ScreenshotPriceURL:  screenshotPriceURL,
        ScreenshotBuybackURL: screenshotBuybackURL,
    }, nil
}
```

**Step 4: Update `triggerAI` message**

In `triggerAI()`, add change info to the user notification:

```go
func (p *PipelineHandler) triggerAI(chatID int64) {
    event, err := p.getLatestEvent()
    // ... existing error handling ...
    
    trendEmoji := "➡️"
    if event.Trend == "up" { trendEmoji = "🟢" }
    if event.Trend == "down" { trendEmoji = "🔴" }
    
    p.send(chatID, fmt.Sprintf(
        "⏳ Triggering AI generator untuk tanggal *%s* ...\n\n"+
        "💰 Buy: Rp %s | Sell: Rp %s\n"+
        "%s Trend: %s | Change: Rp %s (%.2f%%)",
        event.Date,
        formatPriceIDR(event.Prices[0].BuyPrice),
        formatPriceIDR(event.Prices[0].SellPrice),
        trendEmoji, event.Trend,
        formatPriceIDR(event.ChangeAmt),
        event.ChangePct,
    ))
    
    // ... rest of publish logic ...
}
```

**Verification:**
- Run `/pipeline ai` — verify change data appears in the response message
- Check AI caption in DB contains correct trend/change

---

### Task 2: Add `/republish` command handler

**Objective:** Create a single command that triggers the full republish pipeline.

**Files:**
- Modify: `services/telegram-bot/pipeline.go`

**Step 1: Register `/republish` route**

In `Handle()` switch, add case:

```go
case "republish":
    p.triggerRepublish(chatID)
```

**Step 2: Implement `triggerRepublish`**

```go
func (p *PipelineHandler) triggerRepublish(chatID int64) {
    event, err := p.getLatestEvent()
    if err != nil {
        p.send(chatID, "❌ Gagal mengambil data harga: "+err.Error())
        return
    }
    
    trendEmoji := "➡️"
    if event.Trend == "up" { trendEmoji = "🟢" }
    if event.Trend == "down" { trendEmoji = "🔴" }
    
    p.send(chatID, fmt.Sprintf(
        "🔄 *Republish Pipeline Started*\n\n"+
        "📅 Tanggal: *%s*\n"+
        "💰 1g Antam: Buy Rp %s | Sell Rp %s\n"+
        "%s Trend: *%s* | Change: Rp %s (%.2f%%)\n\n"+
        "▸ Step 1/3: AI Caption generating...\n"+
        "▸ Step 2/3: Infografis rendering...\n"+
        "▸ Step 3/3: Posting ke sosmed...\n\n"+
        "_Pipeline akan berjalan otomatis, pantau notifikasi berikutnya._",
        event.Date,
        formatPriceIDR(event.Prices[0].BuyPrice),
        formatPriceIDR(event.Prices[0].SellPrice),
        trendEmoji, event.Trend,
        formatPriceIDR(event.ChangeAmt),
        event.ChangePct,
    ))
    
    // Trigger AI generator (which triggers media, then repliz via serial pipeline)
    if err := p.q.Publish(queue.KeyGoldScrapedAI, event); err != nil {
        p.send(chatID, "❌ Gagal publish ke queue AI: "+err.Error())
        return
    }
    
    log.Printf("[pipeline-handler] 🔄 Republish triggered for %s (price_id=%d)", event.Date, event.PriceID)
}
```

**Step 3: Update help text**

In `showHelp()`, add the republish command:

```go
"`/republish` — Republish pipeline lengkap (AI + Media + Posting)\n"
```

**Step 4: Update subscriber help**

In `subscriber.go`, add to the admin command listing:

```go
"`/republish` — Republish full pipeline (AI → Media → Posting)\n"
```

**Verification:**
- Run `/republish` — verify the message shows correct price + change data
- Verify AI generator picks up the event (check logs)
- Verify media generator auto-triggers (check logs)
- Verify repliz uploader receives the event (check logs, expected: credentials error in staging)

---

### Task 3: Build, test, and verify

**Objective:** Deploy to staging and verify the full pipeline flow.

**Step 1: Build and restart staging**

```bash
cd /mnt/staging/dnarmasid
docker compose up -d --build
```

**Step 2: Verify logs**

```bash
docker compose -p dnarmasid-stg logs --tail=5 ai-generator media-generator repliz-uploader
```

Expected:
- AI: "Waiting for gold.scraped.ai events..."
- Media: "Waiting for gold.processed events..."
- Repliz: "Waiting for media.generation.completed events..."

**Step 3: Test `/republish`**

Trigger the command from Telegram, then verify:

```bash
# AI log
docker compose -p dnarmasid-stg logs --tail=20 ai-generator | grep -E "Event received|gold.processed"

# Media log
docker compose -p dnarmasid-stg logs --tail=30 media-generator | grep -E "Event received|Repliz event"

# Repliz log
docker compose -p dnarmasid-stg logs --tail=10 repliz-uploader
```

Expected flow:
1. AI: "📥 Event received: date=X trend=up/down/stable" ← NOT hardcoded "stable"
2. AI: "✅ gold.processed published" (auto-triggers media)
3. Media: "📥 Event received: date=X"
4. Media: "✅ Repliz event published for date X"
5. Repliz upload fails with credentials error (expected in staging)

**Step 4: Commit**

```bash
git add services/telegram-bot/pipeline.go services/telegram-bot/subscriber.go
git commit -m "feat: add /republish command with change calculation"
```

---

## Files Summary

| File | Action | Lines |
|------|--------|-------|
| `services/telegram-bot/pipeline.go` | Modify | ~80 lines added (helpers, calcChange, triggerRepublish) |
| `services/telegram-bot/subscriber.go` | Modify | ~2 lines (help text) |
| **Total** | | ~82 lines |

## No New Dependencies Required

All changes use existing:
- GORM queries
- Redis queue client
- Telegram Bot API
- Existing pipeline infrastructure

## Risks & Mitigations

1. **Risk:** Previous day data may not exist (fresh DB)
   **Mitigation:** `getPreviousPrice` returns nil gracefully, `calcChange` defaults to "stable"

2. **Risk:** No screenshots in DB (hero_screenshot not found)
   **Mitigation:** Media generator already handles missing screenshots gracefully — logs warning, skips hero slide

3. **Risk:** Multiple `/republish` calls could duplicate content
   **Mitigation:** This is intentional — republish means "re-post". Each run generates fresh content.

4. **Risk:** `Trend: "stable"` hardcoded bug persists in old code paths
   **Mitigation:** Fixed in `getLatestEvent()`. Old callers (`triggerMedia`, `triggerThreads`) benefit automatically.