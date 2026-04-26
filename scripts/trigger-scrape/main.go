package main

import (
	"log"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/queue"
)

func main() {
	log.Println("🚀 [trigger] Sending manual scrape job...")

	cfg := config.Load()
	q := queue.NewClient(cfg)

	job := map[string]string{
		"triggered_at": time.Now().Format(time.RFC3339),
		"source":       "manual_trigger",
	}

	err := q.Publish(queue.KeyJobScrape, job)
	if err != nil {
		log.Fatalf("❌ Failed to trigger scrape: %v", err)
	}

	log.Println("✅ Job sent! Check scraper logs for progress.")
}
