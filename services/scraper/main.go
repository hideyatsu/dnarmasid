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
	log.Println("🔍 [scraper] Starting DnarMasID Scraper...")

	cfg := config.Load()
	database := db.Connect(cfg)
	q := queue.NewClient(cfg)

	// Auto migrate table
	database.AutoMigrate(&models.GoldPrice{}, &models.PipelineLog{})

	scraper := NewAntamScraper(cfg, database)

	log.Println("[scraper] ✅ Ready. Waiting for job.scrape events...")

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-quit:
			log.Println("[scraper] Shutting down...")
			return
		default:
			// Blocking consume — tunggu job dari scheduler
			var job map[string]string
			err := q.ConsumeJSON(queue.KeyJobScrape, 5*time.Second, &job)
			if err != nil {
				// Timeout normal, lanjut loop
				continue
			}

			log.Printf("[scraper] 📥 Job received: %v", job)

			// Jalankan scraping
			event, err := scraper.Run()
			if err != nil {
				log.Printf("[scraper] ❌ Scrape failed: %v", err)
				continue
			}

			// Publish hasil ke Redis → ai, media, telegram (Fan-out)
			// if err := q.Publish(queue.KeyGoldScrapedAI, event); err != nil {
			// 	log.Printf("[scraper] ❌ Failed to publish to ai: %v", err)
			// }
			// if err := q.Publish(queue.KeyGoldScrapedMedia, event); err != nil {
			// 	log.Printf("[scraper] ❌ Failed to publish to media: %v", err)
			// }
			if err := q.Publish(queue.KeyGoldScrapedBot, event); err != nil {
				log.Printf("[scraper] ❌ Failed to publish to telegram: %v", err)
			}

			log.Printf("[scraper] ✅ gold.scraped published | Date: %s | Trend: %s | Change: %+.2f%%",
				event.Date, event.Trend, event.ChangePct)
		}
	}
}
