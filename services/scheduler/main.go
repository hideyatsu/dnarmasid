package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	internalQueue "dnarmasid/internal/queue"
	"dnarmasid/internal/tasks"
	"dnarmasid/shared/config"
	"dnarmasid/shared/queue"

	"github.com/hibiken/asynq"
	"github.com/robfig/cron/v3"
)

func main() {
	log.Println("🕐 [scheduler] Starting DnarMasID Scheduler...")

	cfg := config.Load()

	// Redis queue (legacy mode or fallback)
	var q *queue.Client
	if !cfg.UseAsynq {
		q = queue.NewClient(cfg)
	}

	// Asynq client (modern mode)
	var asynqClient *internalQueue.AsynqClient
	if cfg.UseAsynq {
		redisAddr := fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort)
		asynqClient = internalQueue.NewAsynqClient(redisAddr)
		defer asynqClient.Close()
		log.Println("[scheduler] ✅ Asynq mode ENABLED (Redis List disabled)")
	} else {
		log.Println("[scheduler] ⚠️ Legacy mode (Redis List only, no Asynq)")
	}

	c := cron.New(cron.WithLocation(time.FixedZone("WIB", 7*60*60)))

	// Trigger scrape pipeline sesuai jadwal .env (default: 08:00 WIB)
	_, err := c.AddFunc(cfg.ScheduleCron, func() {
		log.Println("[scheduler] ⏰ Triggering daily scrape pipeline...")

		if cfg.UseAsynq {
			// Asynq mode: enqueue to Asynq only
			payload, _ := tasks.NewScrapePayload("scheduler")
			info, err := asynqClient.Enqueue(tasks.TypeScrape, payload,
				asynq.Queue(tasks.QueueCritical),
				asynq.MaxRetry(cfg.AsynqRetryMax),
			)
			if err != nil {
				log.Printf("[scheduler] ❌ Asynq enqueue failed: %v", err)
				return
			}
			log.Printf("[scheduler] ✅ Asynq task enqueued: id=%s queue=%s", info.ID, info.Queue)
		} else {
			// Legacy mode: Redis List
			if err := q.Publish(queue.KeyJobScrape, map[string]string{
				"triggered_at": time.Now().Format(time.RFC3339),
				"source":       "scheduler",
			}); err != nil {
				log.Printf("[scheduler] ❌ Failed to publish job.scrape: %v", err)
				return
			}
			log.Println("[scheduler] ✅ job.scrape published to Redis")
		}
	})
	if err != nil {
		log.Fatalf("[scheduler] Failed to add cron job: %v", err)
	}

	// Trigger scrape pipeline sore hari (EVENING)
	_, err = c.AddFunc(cfg.ScheduleCronEvening, func() {
		log.Println("[scheduler] ⏰ Triggering evening scrape pipeline...")

		if cfg.UseAsynq {
			// Asynq mode: enqueue to Asynq only
			payload, _ := tasks.NewScrapePayload("scheduler-evening")
			info, err := asynqClient.Enqueue(tasks.TypeScrape, payload,
				asynq.Queue(tasks.QueueCritical),
				asynq.MaxRetry(cfg.AsynqRetryMax),
			)
			if err != nil {
				log.Printf("[scheduler] ❌ Asynq enqueue failed: %v", err)
				return
			}
			log.Printf("[scheduler] ✅ Asynq evening task enqueued: id=%s queue=%s", info.ID, info.Queue)
		} else {
			// Legacy mode: Redis List
			if err := q.Publish(queue.KeyJobScrape, map[string]string{
				"triggered_at": time.Now().Format(time.RFC3339),
				"source":       "scheduler",
				"session":      "evening",
			}); err != nil {
				log.Printf("[scheduler] ❌ Failed to publish job.scrape: %v", err)
				return
			}
			log.Println("[scheduler] ✅ evening job.scrape published to Redis")
		}
	})
	if err != nil {
		log.Fatalf("[scheduler] Failed to add evening cron job: %v", err)
	}

	c.Start()
	log.Printf("[scheduler] ✅ Running. Morning: %s | Evening: %s (WIB)", cfg.ScheduleCron, cfg.ScheduleCronEvening)
	if cfg.UseAsynq {
		log.Println("[scheduler] 📡 Mode: Asynq (asynq-worker required)")
	} else {
		log.Println("[scheduler] 📡 Mode: Legacy Redis List")
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[scheduler] Shutting down...")
	ctx := c.Stop()
	<-ctx.Done()
	log.Println("[scheduler] Stopped.")
}
