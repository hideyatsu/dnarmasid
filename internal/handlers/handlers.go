package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"dnarmasid/internal/tasks"
	"dnarmasid/shared/queue"

	"github.com/hibiken/asynq"
)

// ShadowMode controls whether handlers execute or just log
var ShadowMode = true

// RedisQueue is the Redis client used in bridge mode (Phase 4 lightweight)
var RedisQueue *queue.Client

// HandleScrape processes scrape tasks
// In shadow mode: logs what it would do, no side effects
// In live mode: bridge to Redis BRPOP queue (existing scraper service)
func HandleScrape(ctx context.Context, t *asynq.Task) error {
	var p tasks.ScrapePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}

	if ShadowMode {
		log.Printf("[asynq:shadow] 👻 SCRAPE task received: source=%s triggered_at=%s", p.Source, p.TriggeredAt)
		log.Println("[asynq:shadow] Would execute: scraper.Run() → ai-generator → media-generator → telegram-bot")
		return nil // success, but no action
	}

	// Live mode (Phase 4 bridge): forward to Redis BRPOP for existing scraper
	log.Printf("[asynq:live] 🚀 SCRAPE task: bridging to Redis queue (source=%s)", p.Source)
	if RedisQueue == nil {
		return fmt.Errorf("RedisQueue not initialized")
	}
	payload := map[string]string{
		"source":       p.Source + "-asynq",
		"triggered_at": p.TriggeredAt,
	}
	if err := RedisQueue.Publish(queue.KeyJobScrape, payload); err != nil {
		return fmt.Errorf("bridge to redis: %w", err)
	}
	log.Println("[asynq:live] ✅ Bridged to Redis: scraper service will process")
	return nil
}

// HandleGenerateAI processes AI generation tasks
func HandleGenerateAI(ctx context.Context, t *asynq.Task) error {
	var p tasks.GenerateAIPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}

	if ShadowMode {
		log.Printf("[asynq:shadow] 👻 GENERATE_AI task received: event=%s provider=%s model=%s", p.PriceEventID, p.Provider, p.Model)
		log.Println("[asynq:shadow] Would execute: ai-generator.Generate()")
		return nil
	}

	// Live mode — TODO Phase 4
	log.Printf("[asynq:live] 🚀 GENERATE_AI task executing: event=%s", p.PriceEventID)
	return nil
}

// HandleGenerateMedia processes media generation tasks
func HandleGenerateMedia(ctx context.Context, t *asynq.Task) error {
	var p tasks.GenerateMediaPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}

	if ShadowMode {
		log.Printf("[asynq:shadow] 👻 GENERATE_MEDIA task received: event=%s template=%s", p.PriceEventID, p.Template)
		log.Println("[asynq:shadow] Would execute: media-generator.Generate()")
		return nil
	}

	// Live mode — TODO Phase 4
	log.Printf("[asynq:live] 🚀 GENERATE_MEDIA task executing: event=%s", p.PriceEventID)
	return nil
}

// HandleNotifyTelegram processes Telegram notification tasks
func HandleNotifyTelegram(ctx context.Context, t *asynq.Task) error {
	var p tasks.NotifyTelegramPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}

	if ShadowMode {
		log.Printf("[asynq:shadow] 👻 NOTIFY_TELEGRAM task received: event=%s type=%s", p.PriceEventID, p.MessageType)
		log.Println("[asynq:shadow] Would execute: telegram-bot.Send()")
		return nil
	}

	// Live mode — TODO Phase 4
	log.Printf("[asynq:live] 🚀 NOTIFY_TELEGRAM task executing: event=%s", p.PriceEventID)
	return nil
}

// HandleUpload processes upload tasks
func HandleUpload(ctx context.Context, t *asynq.Task) error {
	var p tasks.UploadPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}

	if ShadowMode {
		log.Printf("[asynq:shadow] 👻 UPLOAD task received: event=%s platforms=%v", p.PriceEventID, p.Platforms)
		log.Println("[asynq:shadow] Would execute: repliz-uploader.Upload()")
		return nil
	}

	// Live mode — TODO Phase 4
	log.Printf("[asynq:live] 🚀 UPLOAD task executing: event=%s", p.PriceEventID)
	return nil
}
