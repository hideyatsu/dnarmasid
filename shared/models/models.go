package models

import (
	"time"
)

// ─────────────────────────────────────────
// GoldPrice — data harga emas dari Antam
// ─────────────────────────────────────────

type GoldPrice struct {
	ID               uint       `gorm:"primarykey" json:"id"`
	Date             time.Time  `gorm:"uniqueIndex:uq_date_gram" json:"date"`
	Gram             float64    `gorm:"uniqueIndex:uq_date_gram" json:"gram"`
	BuyPrice         int64      `json:"buy_price"`
	SellPrice        int64      `json:"sell_price"`
	SourceURL        string     `json:"source_url"`
	SourceUpdateTime *time.Time `json:"source_update_time"`
	CreatedAt        time.Time  `json:"created_at"`
}

// GoldScrapedEvent — payload Redis: scraper → ai-generator & media-generator
type GoldScrapedEvent struct {
	Date       string       `json:"date"`        // "04 Apr 2026"
	UpdateTime string       `json:"update_time"` // "05 Apr 2026 07:31:00"
	PriceID    uint         `json:"price_id"`    // ID dari gold_prices
	Prices    []GoldPrice  `json:"prices"`      // semua gram hari ini
	ChangePct float64      `json:"change_pct"`  // % perubahan vs kemarin
	ChangeAmt        int64        `json:"change_amt"`  // nominal perubahan (Rp)
	Trend            string       `json:"trend"`       // "up" | "down" | "stable"
	BuybackChangeAmt int64        `json:"buyback_change_amt"`
	BuybackTrend     string       `json:"buyback_trend"`
	ScreenshotPriceURL   string   `json:"screenshot_price_url"`
	ScreenshotBuybackURL string   `json:"screenshot_buyback_url"`
}

// ScrapeFailedEvent — payload Redis: scraper → telegram-bot (saat gagal)
type ScrapeFailedEvent struct {
	Date    string `json:"date"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

// ─────────────────────────────────────────
// GeneratedContent — hasil AI generator
// ─────────────────────────────────────────

type Platform string

const (
	PlatformInstagram Platform = "instagram"
	PlatformTwitter   Platform = "twitter"
	PlatformFacebook  Platform = "facebook"
	PlatformThreads   Platform = "threads"
	PlatformYouTube   Platform = "youtube"
	PlatformTikTok    Platform = "tiktok"
	PlatformGeneral   Platform = "general"
)

type ContentType string

const (
	ContentCaption     ContentType = "caption"
	ContentThread      ContentType = "thread"
	ContentDescription ContentType = "description"
	ContentAnalysis    ContentType = "analysis"
)

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

// ContentReadyEvent — payload Redis: ai-generator → telegram-bot
type ContentReadyEvent struct {
	PriceID  uint                        `json:"price_id"`
	Date     string                      `json:"date"`
	Contents map[Platform]string         `json:"contents"`  // platform → teks
	Analysis string                      `json:"analysis"`  // insight harga
}

// ─────────────────────────────────────────
// GeneratedMedia — file gambar & video
// ─────────────────────────────────────────

type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVideo MediaType = "video"
)

type GeneratedMedia struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	PriceID   uint      `json:"price_id"`
	MediaType MediaType `json:"media_type"`
	FilePath  string    `json:"file_path"`
	FileName  string    `json:"file_name"`
	PublicURL string    `json:"public_url"`
	Status    string    `gorm:"default:pending" json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// MediaReadyEvent — payload Redis: media-generator → telegram-bot
type MediaReadyEvent struct {
	PriceID   uint      `json:"price_id"`
	Date      string    `json:"date"`
	MediaType MediaType `json:"media_type"`
	FilePath  string    `json:"file_path"`
	FileName  string    `json:"file_name"`
	PublicURL string    `json:"public_url"`
	ScreenshotPriceURL   string `json:"screenshot_price_url"`
	ScreenshotBuybackURL string `json:"screenshot_buyback_url"`
}

// MediaGenerationCompletedEvent — payload Redis: media-generator → repliz-uploader
type MediaGenerationCompletedEvent struct {
	PriceID                      uint   `json:"price_id"`
	Date                         string `json:"date"`
	Caption                      string `json:"caption"`                          // AI generated caption
	InfographicURL               string `json:"infographic_url"`                  // Slide 1: infografis harga emas
	CTAImageURL                  string `json:"cta_image_url"`                    // Slide 7: CTA penutup
	ScreenshotPriceURL           string `json:"screenshot_price_url"`             // Raw screenshot dari scraper
	ScreenshotBuybackURL         string `json:"screenshot_buyback_url"`           // Raw buyback screenshot
	HeroScreenshotSlideURL       string `json:"hero_screenshot_slide_url"`        // Slide 2: hero price (wrapped in phone mockup)
	BridgingSlideURL             string `json:"bridging_slide_url"`               // Slide 3: bridging fitur
	FeatureHargaSlideURL         string `json:"feature_harga_slide_url"`          // Slide 4: notifikasi harga emas
	FeatureStokAlertSlideURL     string `json:"feature_stok_alert_slide_url"`     // Slide 5: notifikasi stok emas
	FeatureStokButikSlideURL     string `json:"feature_stok_butik_slide_url"`     // Slide 6: informasi stok emas
}

// ─────────────────────────────────────────
// Subscriber — user Telegram
// ─────────────────────────────────────────

type Subscriber struct {
	ID           uint      `gorm:"primarykey" json:"id"`
	ChatID       int64     `gorm:"uniqueIndex" json:"chat_id"`
	Username     string    `json:"username"`
	FirstName    string    `json:"first_name"`
	Status       string    `gorm:"default:active" json:"status"`
	SubscribedAt time.Time `json:"subscribed_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ─────────────────────────────────────────
// PipelineLog — log per stage per hari
// ─────────────────────────────────────────

type PipelineStage string

const (
	StageScrape        PipelineStage = "scrape"
	StageAIGenerate    PipelineStage = "ai_generate"
	StageMediaGenerate PipelineStage = "media_generate"
	StageTelegramSend  PipelineStage = "telegram_send"
)

type PipelineLog struct {
	ID         uint          `gorm:"primarykey"`
	Date       time.Time
	Stage      PipelineStage
	Status     string
	Message    string
	StartedAt  time.Time
	FinishedAt *time.Time
}
