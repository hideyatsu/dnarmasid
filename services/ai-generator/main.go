package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/db"
	"dnarmasid/shared/models"
	"dnarmasid/shared/queue"
)

func main() {
	log.Println("🤖 [ai-generator] Starting DnarMasID AI Generator...")

	cfg := config.Load()
	database := db.Connect(cfg)
	q := queue.NewClient(cfg)

	database.AutoMigrate(&models.GeneratedContent{})

	generator := NewContentGenerator(cfg, database)

	log.Printf("[ai-generator] ✅ Ready. Waiting for %s events...", queue.KeyGoldScrapedAI)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Consumer 1: General caption (existing)
	go func() {
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
}
