# Multi-Platform Auto-Publish via Repliz — Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Auto-publish ke Instagram (dan future: Facebook, dll) via Repliz API. Support single image dan album post type. Satu event → fan-out ke N platforms, masing-masing dengan config type sendiri.

**Architecture:** Extend existing `repliz-uploader`. Refactor jadi data-driven: list of `PlatformTarget` dari config. Same `media.generation.completed` event → loop platforms → POST each. No new container, no new queue.

**Tech Stack:** Go 1.24, Repliz REST API, Redis (existing queue)

---

## Repliz Supported Post Types (Reference)

| Post Type | Supported Platforms |
|-----------|-------------------|
| Text | Facebook, Threads |
| **Image** | **Facebook, Instagram, Threads, TikTok, LinkedIn** |
| Video | Facebook, Instagram, Threads, TikTok, YouTube, LinkedIn |
| Reels | Facebook |
| **Album** | **Facebook, Instagram, Threads, TikTok, LinkedIn** |
| Link | Facebook |
| Story | Facebook, Instagram |

**DnarMasID strategy:**
- TikTok → `album` (3 images: infografis + screenshot + CTA)
- Instagram → `image` (1 image: infografis only)
- Future platforms → pick type per platform as needed

---

## Current Flow (TikTok only, hardcoded album)

```
media-generator
  │ LPUSH media.generation.completed
  ▼
repliz-uploader (consume)
  │ Build album payload (hardcoded TikTok)
  │ POST api.repliz.com/public/schedule
  ▼
✅ TikTok scheduled
```

## Target Flow (Multi-Platform, data-driven)

```
media-generator
  │ LPUSH media.generation.completed
  ▼
repliz-uploader (consume)
  │
  │ Loop through configured platforms:
  │
  ├──→ TikTok     type=album  → 3 images + music → ✅ scheduled
  ├──→ Instagram  type=image  → 1 image (infografis) → ✅ scheduled
  └──→ Facebook   type=image  → 1 image (infografis) → ✅ scheduled (future)
```

---

## Reference: Repliz API Payload — Single Image (Instagram/Facebook)

```json
{
  "title": "",
  "description": "AI-generated caption...",
  "topic": "antamlogammulia",
  "type": "image",
  "medias": [
    {
      "alt": "",
      "customThumbnail": false,
      "type": "image",
      "thumbnail": "https://r2.dnarmas.id/infografis.png",
      "url": "https://r2.dnarmas.id/infografis.png"
    }
  ],
  "meta": { "title": "", "description": "", "url": "" },
  "additionalInfo": {
    "isAiGenerated": true,
    "isDraft": false,
    "collaborators": [],
    "music": { "id": "", "artist": "", "name": "", "thumbnail": "" },
    "products": [],
    "tags": []
  },
  "replies": [],
  "accountId": "680affa5ce12f2f72916f67e",
  "scheduleAt": "2026-05-30T09:10:00Z"
}
```

## Reference: Repliz API Payload — Album (TikTok existing)

```json
{
  "title": "Update Harga Emas Antam - 30 Mei 2026",
  "description": "AI-generated caption...",
  "topic": "antamlogammulia",
  "type": "album",
  "medias": [
    {
      "alt": "",
      "customThumbnail": false,
      "type": "image",
      "thumbnail": "https://r2.dnarmas.id/infografis.png",
      "url": "https://r2.dnarmas.id/infografis.png"
    },
    {
      "alt": "",
      "type": "image",
      "thumbnail": "https://r2.dnarmas.id/price-screenshot.jpeg",
      "url": "https://r2.dnarmas.id/price-screenshot.jpeg"
    },
    {
      "alt": "",
      "type": "image",
      "thumbnail": "https://r2.dnarmas.id/cta.png",
      "url": "https://r2.dnarmas.id/cta.png"
    }
  ],
  "meta": { "title": "", "description": "", "url": "" },
  "additionalInfo": {
    "isAiGenerated": true,
    "isDraft": false,
    "collaborators": [],
    "music": { "id": "7637201849280711442", "artist": "DnarMasID", "name": "original sound - DnarMasID", "thumbnail": "" },
    "products": [],
    "tags": []
  },
  "replies": [],
  "accountId": "<TIKTOK_ACCOUNT_ID>",
  "scheduleAt": "2026-05-30T09:10:00Z"
}
```

### Key Differences: Image vs Album

| Field | Album (TikTok) | Image (Instagram) |
|-------|---------------|-------------------|
| `type` | `"album"` | `"image"` |
| `title` | Filled | `""` (empty) |
| `medias` | 3 images (infografis+screenshot+CTA) | 1 image (infografis only) |
| `music` | DnarMasID sound (filled) | Empty strings |
| `customThumbnail` | Only on first media | On the single media |
| `products`, `tags` | `[]` | `[]` |
| `collaborators` | `[]` (correct spelling per official docs) | `[]` |

> **Confirmed:** Official Repliz docs use `collaborators` (correct spelling) for BOTH image and album. Existing DnarMasID code has typo `collaboratos` — **must be fixed**.

### Identical Structure

Both payloads share **exact same JSON structure**. Differences are only in field values:
- `type`: `"image"` vs `"album"`
- `medias`: 1 item vs N items
- `music`: empty vs filled (optional per platform)
- `title`: empty vs filled (optional)

---

### Task 1: Fix & Update Repliz Client Struct

**Objective:** Update `repliz/client.go` structs to match actual Repliz API. Fix typo, add missing fields.

**Files:**
- Modify: `services/repliz-uploader/repliz/client.go`

**Step 1: Update `AdditionalInfo` struct**

Replace existing `AdditionalInfo` (lines 39-44):

```go
// AdditionalInfo represents additional information for the post
type AdditionalInfo struct {
    IsAiGenerated  bool     `json:"isAiGenerated"`
    IsDraft        bool     `json:"isDraft"`
    Collaborators  []string `json:"collaborators"`
    Music          Music    `json:"music"`
    Products       []string `json:"products,omitempty"`
    Tags           []string `json:"tags,omitempty"`
}
```

Changes:
- Fix typo: `collaboratos` → `collaborators`
- Add `Products []string`
- Add `Tags []string`
- Add `omitempty` to products/tags (cleaner JSON when empty)

> ⚠️ **Risk:** TikTok existing code pakai typo `collaboratos`. Kalau Repliz API sudah fix ke `collaborators`, ini aman. Kalau belum, TikTok post bisa break. **Test TikTok setelah deploy.**

**Step 2: Commit**

```bash
git add services/repliz-uploader/repliz/client.go
git commit -m "fix(repliz): fix collaborators typo, add products/tags fields"
```

---

### Task 2: Add Instagram Config

**Objective:** New env var untuk Instagram account ID + post type.

**Files:**
- Modify: `shared/config/config.go`
- Modify: `.env.example`

**Step 1: Add config fields**

In `Config` struct, add after `ReplizTikTokAccountID`:

```go
ReplizInstagramAccountID string
```

In `Load()` return block:

```go
ReplizInstagramAccountID: getEnv("REPLIZ_INSTAGRAM_ACCOUNT_ID", ""),
```

**Step 2: Update .env.example**

Add after `REPLIZ_TIKTOK_ACCOUNT_ID`:

```env
REPLIZ_INSTAGRAM_ACCOUNT_ID=your_instagram_account_id
```

**Step 3: Commit**

```bash
git add shared/config/config.go .env.example
git commit -m "feat(config): add REPLIZ_INSTAGRAM_ACCOUNT_ID env var"
```

---

### Task 3: Create `platforms.go` — Platform Target Abstraction

**Objective:** Data-driven platform config. Each platform punya name, accountID, postType.

**Files:**
- Create: `services/repliz-uploader/platforms.go`

**Step 1: Create file**

```go
package main

import (
    "dnarmasid/shared/config"
)

// PostType defines how the content is published
type PostType string

const (
    PostTypeImage PostType = "image" // single image
    PostTypeAlbum PostType = "album" // multiple images (carousel)
)

// PlatformTarget represents a social media platform to publish to
type PlatformTarget struct {
    Name      string   // "tiktok", "instagram", "facebook"
    AccountID string   // Repliz account ID
    PostType  PostType // "image" or "album"
}

// getActivePlatforms returns list of platforms with configured account IDs
func getActivePlatforms(cfg *config.Config) []PlatformTarget {
    var platforms []PlatformTarget

    if cfg.ReplizTikTokAccountID != "" {
        platforms = append(platforms, PlatformTarget{
            Name:      "tiktok",
            AccountID: cfg.ReplizTikTokAccountID,
            PostType:  PostTypeAlbum,
        })
    }

    if cfg.ReplizInstagramAccountID != "" {
        platforms = append(platforms, PlatformTarget{
            Name:      "instagram",
            AccountID: cfg.ReplizInstagramAccountID,
            PostType:  PostTypeImage,
        })
    }

    // Future: Facebook, Threads, etc
    // if cfg.ReplizFacebookAccountID != "" {
    //     platforms = append(platforms, PlatformTarget{
    //         Name: "facebook", AccountID: cfg.ReplizFacebookAccountID, PostType: PostTypeImage,
    //     })
    // }

    return platforms
}
```

**Step 2: Commit**

```bash
git add services/repliz-uploader/platforms.go
git commit -m "feat(repliz): add platform target abstraction with post type"
```

---

### Task 4: Rewrite `main.go` — Multi-Platform Loop

**Objective:** Replace hardcoded TikTok logic dengan generic platform loop. Build payload based on platform `PostType`.

**Files:**
- Modify: `services/repliz-uploader/main.go`

**Step 1: Rewrite full file**

```go
package main

import (
    "fmt"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "dnarmasid/services/repliz-uploader/repliz"
    "dnarmasid/shared/config"
    "dnarmasid/shared/models"
    "dnarmasid/shared/queue"
)

func main() {
    log.Println("🚀 [repliz-uploader] Starting DnarMasID Repliz Uploader...")

    cfg := config.Load()
    q := queue.NewClient(cfg)
    client := repliz.NewClient(cfg)

    // Log active platforms
    platforms := getActivePlatforms(cfg)
    if len(platforms) == 0 {
        log.Println("[repliz-uploader] ⚠️ Warning: No platform account IDs configured")
    } else {
        for _, p := range platforms {
            log.Printf("[repliz-uploader] 📱 Platform active: %s (type=%s)", p.Name, p.PostType)
        }
    }

    log.Println("[repliz-uploader] ✅ Ready. Waiting for media.generation.completed events...")

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

    for {
        select {
        case <-quit:
            log.Println("[repliz-uploader] Shutting down...")
            return
        default:
            var event models.MediaGenerationCompletedEvent
            err := q.ConsumeJSON(queue.KeyMediaGenerationCompleted, 5*time.Second, &event)
            if err != nil {
                continue
            }

            log.Printf("[repliz-uploader] 📥 Event received for date: %s", event.Date)
            processEvent(client, cfg, platforms, event)
        }
    }
}

func processEvent(client *repliz.Client, cfg *config.Config, platforms []PlatformTarget, event models.MediaGenerationCompletedEvent) {
    if len(platforms) == 0 {
        log.Println("[repliz-uploader] ⚠️ No platforms configured, skipping")
        return
    }

    // Fallback caption
    description := event.Caption
    if description == "" {
        description = fmt.Sprintf("Update Harga Emas Antam %s. Cek infografis untuk detailnya! #EmasAntam #DnarMasID", event.Date)
    }

    scheduleTime := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)

    for _, p := range platforms {
        var medias []repliz.Media
        var postType string
        var title string

        switch p.PostType {
        case PostTypeAlbum:
            // Album: infografis + screenshot + CTA
            postType = "album"
            title = fmt.Sprintf("Update Harga Emas Antam - %s", event.Date)
            medias = buildAlbumMedias(event)

        case PostTypeImage:
            // Single image: infografis only
            postType = "image"
            title = ""
            medias = buildSingleImageMedias(event)
        }

        payload := repliz.Payload{
            Title:       title,
            Description: description,
            Topic:       "antamlogammulia",
            Type:        postType,
            Medias:      medias,
            Meta:        repliz.Meta{},
            AdditionalInfo: repliz.AdditionalInfo{
                IsAiGenerated: true,
                IsDraft:       false,
                Collaborators: []string{},
                Music: repliz.Music{
                    ID:        "7637201849280711442",
                    Artist:    "DnarMasID",
                    Name:      "original sound - DnarMasID",
                    Thumbnail: "",
                },
                Products: []string{},
                Tags:     []string{},
            },
            Replies:    []string{},
            AccountID:  p.AccountID,
            ScheduleAt: scheduleTime,
        }

        err := client.UploadPost(payload)
        if err != nil {
            log.Printf("[repliz-uploader] ❌ %s upload failed: %v", p.Name, err)
        } else {
            log.Printf("[repliz-uploader] ✅ %s (%s) scheduled for %s at %s", p.Name, p.PostType, event.Date, scheduleTime)
        }
    }
}

// buildAlbumMedias builds multi-image media array (TikTok carousel)
func buildAlbumMedias(event models.MediaGenerationCompletedEvent) []repliz.Media {
    var medias []repliz.Media

    if event.InfographicURL != "" {
        medias = append(medias, repliz.Media{
            Alt:             "Infografis Harga Emas",
            Type:            "image",
            Thumbnail:       event.InfographicURL,
            URL:             event.InfographicURL,
            CustomThumbnail: false,
        })
    }

    if event.ScreenshotPriceURL != "" {
        medias = append(medias, repliz.Media{
            Alt:       "Screenshot Harga Emas",
            Type:      "image",
            Thumbnail: event.ScreenshotPriceURL,
            URL:       event.ScreenshotPriceURL,
        })
    }

    if event.CTAImageURL != "" {
        medias = append(medias, repliz.Media{
            Alt:       "Call to Action - DnarMasID",
            Type:      "image",
            Thumbnail: event.CTAImageURL,
            URL:       event.CTAImageURL,
        })
    }

    return medias
}

// buildSingleImageMedias builds single-image media array (Instagram/Facebook)
func buildSingleImageMedias(event models.MediaGenerationCompletedEvent) []repliz.Media {
    var medias []repliz.Media

    // Single image: infografis only
    if event.InfographicURL != "" {
        medias = append(medias, repliz.Media{
            Alt:             "",
            Type:            "image",
            Thumbnail:       event.InfographicURL,
            URL:             event.InfographicURL,
            CustomThumbnail: false,
        })
    }

    return medias
}
```

**Step 2: Commit**

```bash
git add services/repliz-uploader/
git commit -m "feat(repliz): multi-platform publish with image/album support"
```

---

### Task 5: Build & Test Staging

**Objective:** Verify build passes, test end-to-end.

**Step 1: Build**

```bash
cd /mnt/staging/dnarmasid
go build ./services/repliz-uploader/...
```

Expected: No errors.

**Step 2: Rebuild container**

```bash
docker compose up -d --build repliz-uploader
```

**Step 3: Check startup logs**

```bash
docker compose logs repliz-uploader --tail=20
```

Expected:
```
🚀 [repliz-uploader] Starting DnarMasID Repliz Uploader...
[repliz-uploader] 📱 Platform active: tiktok (type=album)
[repliz-uploader] 📱 Platform active: instagram (type=image)
[repliz-uploader] ✅ Ready. Waiting for media.generation.completed events...
```

**Step 4: Trigger test scrape**

```bash
docker compose exec redis redis-cli LPUSH job.scrape '{"trigger":"manual"}'
```

Wait for full pipeline. Check logs:

```bash
docker compose logs repliz-uploader --tail=30
```

Expected:
```
[repliz-uploader] 📥 Event received for date: 30 Mei 2026
[repliz-uploader] ✅ tiktok (album) scheduled for 30 Mei 2026 at 2026-05-30T09:10:00Z
[repliz-uploader] ✅ instagram (image) scheduled for 30 Mei 2026 at 2026-05-30T09:10:00Z
```

**Step 5: Verify di Repliz dashboard**

- TikTok: should show album post (3 images) scheduled
- Instagram: should show single image post (infografis) scheduled

---

## 📋 Summary of Changes

| File | Change |
|------|--------|
| `services/repliz-uploader/repliz/client.go` | Fix `collaborators` typo, add `products`/`tags` fields |
| `shared/config/config.go` | Add `ReplizInstagramAccountID` |
| `.env.example` | Add `REPLIZ_INSTAGRAM_ACCOUNT_ID` |
| `services/repliz-uploader/platforms.go` | **NEW** — `PlatformTarget` struct + `getActivePlatforms()` |
| `services/repliz-uploader/main.go` | Rewrite: multi-platform loop, image/album support |

## 🔄 Adding New Platform (Future)

Only 3 changes:

1. Add config field + env var
2. Add block in `getActivePlatforms()` with desired `PostType`
3. Done — payload builder + API call reused

```go
// Example: Facebook
if cfg.ReplizFacebookAccountID != "" {
    platforms = append(platforms, PlatformTarget{
        Name: "facebook", AccountID: cfg.ReplizFacebookAccountID, PostType: PostTypeImage,
    })
}
```

## ⚠️ Pitfalls

1. **`collaborators` typo fix confirmed safe** — official Repliz docs use `collaborators` (correct) for both image and album. Existing DnarMasID code `collaboratos` is a bug. **Still test TikTok regression after deploy just in case.**
2. **Account ID dari Repliz dashboard** — connect social account dulu, copy accountId
3. **No retry logic** — fail = logged only. Future: add retry/queue-back
4. **Same schedule time** — semua platform now+10min. Future: per-platform offset
5. **Sequential posts** — not parallel. One fail doesn't block others

## 🔗 Prerequisites (User Action)

Before deploying:
1. Connect Instagram account di Repliz dashboard
2. Copy Instagram `accountId` dari Repliz
3. Set `REPLIZ_INSTAGRAM_ACCOUNT_ID=<id>` di `.env` staging/production
4. Test TikTok regression setelah `collaborators` typo fix
