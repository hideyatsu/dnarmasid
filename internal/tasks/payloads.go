package tasks

import (
	"encoding/json"
	"time"
)

// ScrapePayload — trigger scrape pipeline
type ScrapePayload struct {
	Source      string `json:"source"`
	TriggeredAt string `json:"triggered_at"`
}

func NewScrapePayload(source string) ([]byte, error) {
	return json.Marshal(ScrapePayload{
		Source:      source,
		TriggeredAt: time.Now().Format(time.RFC3339),
	})
}

// GenerateAIPayload — trigger AI content generation
type GenerateAIPayload struct {
	PriceEventID string  `json:"price_event_id"`
	Provider     string  `json:"provider"` // ollama, gemini
	Model        string  `json:"model"`
	CurrentPrice float64 `json:"current_price"`
	PrevPrice    float64 `json:"prev_price"`
	PriceTrend   string  `json:"price_trend"` // up, down, stable
}

func NewGenerateAIPayload(eventID, provider, model string, current, prev float64, trend string) ([]byte, error) {
	return json.Marshal(GenerateAIPayload{
		PriceEventID: eventID,
		Provider:     provider,
		Model:        model,
		CurrentPrice: current,
		PrevPrice:    prev,
		PriceTrend:   trend,
	})
}

// GenerateMediaPayload — trigger media/image generation
type GenerateMediaPayload struct {
	PriceEventID string `json:"price_event_id"`
	Template     string `json:"template"`
	OutputPath   string `json:"output_path"`
}

func NewGenerateMediaPayload(eventID, template, output string) ([]byte, error) {
	return json.Marshal(GenerateMediaPayload{
		PriceEventID: eventID,
		Template:     template,
		OutputPath:   output,
	})
}

// NotifyTelegramPayload — send notification to Telegram
type NotifyTelegramPayload struct {
	PriceEventID string `json:"price_event_id"`
	MessageType  string `json:"message_type"` // price_update, error, info
	Caption      string `json:"caption"`
	MediaURL     string `json:"media_url,omitempty"`
}

func NewNotifyTelegramPayload(eventID, msgType, caption, mediaURL string) ([]byte, error) {
	return json.Marshal(NotifyTelegramPayload{
		PriceEventID: eventID,
		MessageType:  msgType,
		Caption:      caption,
		MediaURL:     mediaURL,
	})
}

// UploadPayload — upload media to platforms
type UploadPayload struct {
	PriceEventID string   `json:"price_event_id"`
	MediaPath    string   `json:"media_path"`
	Platforms    []string `json:"platforms"` // tiktok, instagram, etc
	Caption      string   `json:"caption"`
}

func NewUploadPayload(eventID, mediaPath string, platforms []string, caption string) ([]byte, error) {
	return json.Marshal(UploadPayload{
		PriceEventID: eventID,
		MediaPath:    mediaPath,
		Platforms:    platforms,
		Caption:      caption,
	})
}
