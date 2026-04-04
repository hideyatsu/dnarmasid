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

	log.Println("[ai-generator] ✅ Ready. Waiting for gold.scraped events...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-quit:
			log.Println("[ai-generator] Shutting down...")
			return
		default:
			var event models.GoldScrapedEvent
			err := q.ConsumeJSON(queue.KeyGoldScraped, 5*time.Second, &event)
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
	}
}
