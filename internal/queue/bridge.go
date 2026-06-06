package queue

import (
	"fmt"
	"log"
	"time"

	"dnarmasid/internal/tasks"
	"dnarmasid/shared/config"

	"github.com/hibiken/asynq"
)

// Bridge provides dual-write capability: Redis List + Asynq
// When USE_ASYNQ=true, tasks are enqueued to both systems
// When USE_ASYNQ=false, only Redis List is used (current behavior)
type Bridge struct {
	redisQueue  RedisClient
	asynqClient *AsynqClient
	useAsynq    bool
}

// RedisClient wraps the existing shared/queue.Client interface
type RedisClient interface {
	Publish(key string, payload any) error
}

// NewBridge creates a bridge between Redis List and Asynq
func NewBridge(redisQueue RedisClient, cfg *config.Config) *Bridge {
	b := &Bridge{
		redisQueue: redisQueue,
		useAsynq:   cfg.UseAsynq,
	}

	if cfg.UseAsynq {
		redisAddr := fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort)
		b.asynqClient = NewAsynqClient(redisAddr)
		log.Println("[bridge] Dual-write mode ENABLED (Redis List + Asynq)")
	} else {
		log.Println("[bridge] Legacy mode (Redis List only)")
	}

	return b
}

// Close closes the Asynq client if active
func (b *Bridge) Close() {
	if b.asynqClient != nil {
		b.asynqClient.Close()
	}
}

// PublishScrape enqueues a scrape task
func (b *Bridge) PublishScrape(source string) error {
	// Always write to Redis List (backward compatible)
	payload := map[string]string{
		"source":       source,
		"triggered_at": time.Now().Format(time.RFC3339),
	}
	if err := b.redisQueue.Publish("job.scrape", payload); err != nil {
		return fmt.Errorf("redis list publish: %w", err)
	}

	// Dual-write to Asynq if enabled
	if b.useAsynq && b.asynqClient != nil {
		data, err := tasks.NewScrapePayload(source)
		if err != nil {
			log.Printf("[bridge] ⚠️ Asynq marshal failed: %v", err)
			return nil // non-fatal, Redis List already has it
		}
		if _, err := b.asynqClient.Enqueue(tasks.TypeScrape, data,
			asynq.Queue(tasks.QueueCritical),
			asynq.MaxRetry(3),
		); err != nil {
			log.Printf("[bridge] ⚠️ Asynq enqueue failed: %v", err)
			return nil // non-fatal
		}
		log.Println("[bridge] ✅ Dual-write: scrape task enqueued to Asynq")
	}

	return nil
}

// PublishGenerateAI enqueues an AI generation task
func (b *Bridge) PublishGenerateAI(key string, payload any) error {
	if err := b.redisQueue.Publish(key, payload); err != nil {
		return fmt.Errorf("redis list publish: %w", err)
	}

	if b.useAsynq && b.asynqClient != nil {
		// For now, just log — full Asynq payload in Phase 2
		log.Printf("[bridge] Asynq shadow: %s task noted", tasks.TypeGenerateAI)
	}

	return nil
}

// PublishGenerateMedia enqueues a media generation task
func (b *Bridge) PublishGenerateMedia(key string, payload any) error {
	if err := b.redisQueue.Publish(key, payload); err != nil {
		return fmt.Errorf("redis list publish: %w", err)
	}

	if b.useAsynq && b.asynqClient != nil {
		log.Printf("[bridge] Asynq shadow: %s task noted", tasks.TypeGenerateMedia)
	}

	return nil
}

// PublishNotify enqueues a notification task
func (b *Bridge) PublishNotify(key string, payload any) error {
	if err := b.redisQueue.Publish(key, payload); err != nil {
		return fmt.Errorf("redis list publish: %w", err)
	}

	if b.useAsynq && b.asynqClient != nil {
		log.Printf("[bridge] Asynq shadow: %s task noted", tasks.TypeNotifyTelegram)
	}

	return nil
}

// PublishUpload enqueues an upload task
func (b *Bridge) PublishUpload(key string, payload any) error {
	if err := b.redisQueue.Publish(key, payload); err != nil {
		return fmt.Errorf("redis list publish: %w", err)
	}

	if b.useAsynq && b.asynqClient != nil {
		log.Printf("[bridge] Asynq shadow: %s task noted", tasks.TypeUpload)
	}

	return nil
}
