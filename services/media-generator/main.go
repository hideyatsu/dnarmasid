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
	"dnarmasid/services/storage"
)

func main() {
	log.Println("🎨 [media-generator] Starting DnarMasID Media Generator...")

	cfg := config.Load()
	database := db.Connect(cfg)
	q := queue.NewClient(cfg)

	r2Uploader, err := storage.NewR2Uploader(cfg)
	if err != nil {
		log.Printf("[media-generator] ⚠️ R2 Storage not configured: %v", err)
	}

	database.AutoMigrate(&models.GeneratedMedia{})

	// Pastikan output dir ada
	os.MkdirAll(cfg.MediaOutputPath, 0755)

	generator := NewMediaGenerator(cfg, database, r2Uploader)

	log.Println("[media-generator] ✅ Ready. Waiting for gold.scraped events...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-quit:
			log.Println("[media-generator] Shutting down...")
			return
		default:
			var event models.GoldScrapedEvent
			err := q.ConsumeJSON(queue.KeyGoldScrapedMedia, 5*time.Second, &event)
			if err != nil {
				continue
			}

			log.Printf("[media-generator] 📥 Event received: date=%s", event.Date)

			// Generate gambar infografis
			imgEvent, err := generator.GenerateImage(&event)
			if err != nil {
				log.Printf("[media-generator] ❌ Image failed: %v", err)
			} else {
				if err := q.Publish(queue.KeyMediaReady, imgEvent); err != nil {
					log.Printf("[media-generator] ❌ Failed to publish image media.ready: %v", err)
				} else {
					log.Printf("[media-generator] ✅ Image media.ready published: %s", imgEvent.FileName)
				}
			}

			// Generate video/reels (placeholder — butuh FFmpeg)
			videoEvent, err := generator.GenerateVideo(&event)
			if err != nil {
				log.Printf("[media-generator] ⚠️ Video failed (expected in dev): %v", err)
			} else {
				if err := q.Publish(queue.KeyMediaReady, videoEvent); err != nil {
					log.Printf("[media-generator] ❌ Failed to publish video media.ready: %v", err)
				} else {
					log.Printf("[media-generator] ✅ Video media.ready published: %s", videoEvent.FileName)
				}
			}
		}
	}
}
