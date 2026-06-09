package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dnarmasid/services/storage"
	"dnarmasid/shared/config"
	"dnarmasid/shared/db"
	"dnarmasid/shared/models"
	"dnarmasid/shared/queue"
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

	log.Printf("[media-generator] ✅ Ready. Waiting for %s events...", queue.KeyGoldScrapedMedia)

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

			// Generate CTA image (independent dari infografis utama)
			var ctaURL string
			if url, err := generator.GenerateCTAImage(&event); err != nil {
				log.Printf("[media-generator] ⚠️ CTA generation failed (non-blocking): %v", err)
			} else {
				ctaURL = url
				log.Printf("[media-generator] ✅ CTA image ready: %s", ctaURL)
			}

			// Generate gambar infografis
			imgEvent, err := generator.GenerateImage(&event)
			if err != nil {
				log.Printf("[media-generator] ❌ Image failed: %v", err)
			} else {
				if err := q.Publish(queue.KeyMediaReady, imgEvent); err != nil {
					log.Printf("[media-generator] ❌ Failed to publish image media.ready: %v", err)
				} else {
					log.Printf("[media-generator] ✅ Image media.ready published: %s", imgEvent.FileName)

					// Generate new slides for 7-slide carousel (hero, bridging, features)
					var heroSlideURL, bridgingSlideURL, featureHargaURL, featureStokAlertURL, featureStokButikURL string

					if url, err := generator.GenerateHeroScreenshot(event.ScreenshotPriceURL, event.Date); err != nil {
						log.Printf("[media-generator] ⚠️ Hero screenshot failed (non-blocking): %v", err)
					} else {
						heroSlideURL = url
					}

					if url, err := generator.GenerateBridgingSlide(event.Date); err != nil {
						log.Printf("[media-generator] ⚠️ Bridging slide failed (non-blocking): %v", err)
					} else {
						bridgingSlideURL = url
					}

					if cfg.SlideHargaNotifURL != "" {
						if url, err := generator.GenerateFeatureScreenshot(cfg.SlideHargaNotifURL, "Notifikasi Harga Emas", "Dapatkan update harga emas otomatis langsung ke Telegram kamu."); err != nil {
							log.Printf("[media-generator] ⚠️ Feature harga slide failed (non-blocking): %v", err)
						} else {
							featureHargaURL = url
						}
					}

					if cfg.SlideStokAlertURL != "" {
						if url, err := generator.GenerateFeatureScreenshot(cfg.SlideStokAlertURL, "Notifikasi Stok Emas", "Pantau stok emas Antam di semua butik secara real-time."); err != nil {
							log.Printf("[media-generator] ⚠️ Feature stok alert slide failed (non-blocking): %v", err)
						} else {
							featureStokAlertURL = url
						}
					}

					if cfg.SlideStokButikURL != "" {
						if url, err := generator.GenerateFeatureScreenshot(cfg.SlideStokButikURL, "Info Stok Butik", "Cek ketersediaan stok di butik Antam terdekat."); err != nil {
							log.Printf("[media-generator] ⚠️ Feature stok butik slide failed (non-blocking): %v", err)
						} else {
							featureStokButikURL = url
						}
					}

					// Trigger Repliz Uploader Event with Polling for AI Caption
					go func(priceID uint, date string, imgEvt *models.MediaReadyEvent, screenshotPriceURL string, screenshotBuybackURL string, ctaImageURL string, heroSlideURL string, bridgingSlideURL string, featureHargaURL string, featureStokAlertURL string, featureStokButikURL string) {
						var caption string
						// Poll for max 60 seconds (20 retries * 3s)
						for i := 0; i < 20; i++ {
							var content models.GeneratedContent
							if err := database.Where("price_id = ? AND content_type = ?", priceID, models.ContentCaption).First(&content).Error; err == nil && content.ContentText != "" {
								caption = content.ContentText
								break
							}
							time.Sleep(3 * time.Second)
						}

						if caption == "" {
							log.Printf("[media-generator] ⚠️ Could not fetch AI caption for Repliz event after polling")
						}

						replizEvent := models.MediaGenerationCompletedEvent{
							PriceID:                    priceID,
							Date:                       date,
							Caption:                    caption,
							InfographicURL:             imgEvt.PublicURL,
							CTAImageURL:                ctaImageURL,
							ScreenshotPriceURL:         screenshotPriceURL,
							ScreenshotBuybackURL:       screenshotBuybackURL,
							HeroScreenshotSlideURL:     heroSlideURL,
							BridgingSlideURL:           bridgingSlideURL,
							FeatureHargaSlideURL:       featureHargaURL,
							FeatureStokAlertSlideURL:   featureStokAlertURL,
							FeatureStokButikSlideURL:   featureStokButikURL,
						}

						if err := q.Publish(queue.KeyMediaGenerationCompleted, replizEvent); err != nil {
							log.Printf("[media-generator] ❌ Failed to publish media.generation.completed: %v", err)
						} else {
							log.Printf("[media-generator] ✅ Repliz event published for date %s", date)
						}
					}(event.PriceID, event.Date, imgEvent, event.ScreenshotPriceURL, event.ScreenshotBuybackURL, ctaURL, heroSlideURL, bridgingSlideURL, featureHargaURL, featureStokAlertURL, featureStokButikURL)
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
