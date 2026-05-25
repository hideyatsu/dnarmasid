package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dnarmasid/services/repliz-uploader/repliz"
	"dnarmasid/shared/config"
	"dnarmasid/shared/models"
	"dnarmasid/shared/queue"
)

func main() {
	log.Println("🚀 [repliz-uploader] Starting DnarMasID Repliz Uploader...")

	cfg := config.Load()
	q := queue.NewClient(cfg)
	client := repliz.NewClient(cfg)

	if cfg.ReplizTikTokAccountID == "" {
		log.Println("[repliz-uploader] ⚠️ Warning: REPLIZ_TIKTOK_ACCOUNT_ID is not configured")
	}

	log.Println("[repliz-uploader] ✅ Ready. Waiting for media.generation.completed events...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-quit:
			log.Println("[repliz-uploader] Shutting down...")
			return
		default:
			var event models.MediaGenerationCompletedEvent
			err := q.ConsumeJSON(queue.KeyMediaGenerationCompleted, 5*time.Second, &event)
			if err != nil {
				continue
			}

			log.Printf("[repliz-uploader] 📥 Event received for date: %s", event.Date)

			// Map dynamic data to Repliz API Payload
			scheduleTime := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)

			medias := []repliz.Media{}

			// Image 1: Infographic (pertama dalam album — tidak bisa custom thumbnail)
			if event.InfographicURL != "" {
				medias = append(medias, repliz.Media{
					Alt:             "Infografis Harga Emas",
					Type:            "image",
					Thumbnail:       event.InfographicURL,
					URL:             event.InfographicURL,
					CustomThumbnail: false,
				})
			}

			// Image 2: Scrape screenshot (Price)
			if event.ScreenshotPriceURL != "" {
				medias = append(medias, repliz.Media{
					Alt:       "Screenshot Harga Emas",
					Type:      "image",
					Thumbnail: event.ScreenshotPriceURL,
					URL:       event.ScreenshotPriceURL,
				})
			}

			// Image 3: CTA slide (template visual sama dengan halaman pertama, isi CTA)
			if event.CTAImageURL != "" {
				medias = append(medias, repliz.Media{
					Alt:       "Call to Action - DnarMasID",
					Type:      "image",
					Thumbnail: event.CTAImageURL,
					URL:       event.CTAImageURL,
				})
			}

			// // Image 3: Scrape screenshot (Buyback)
			// if event.ScreenshotBuybackURL != "" {
			// 	medias = append(medias, repliz.Media{
			// 		Alt:       "Screenshot Buyback Emas",
			// 		Type:      "image",
			// 		Thumbnail: event.ScreenshotBuybackURL,
			// 		URL:       event.ScreenshotBuybackURL,
			// 	})
			// }

			// Fallback description if AI caption is missing
			description := event.Caption
			if description == "" {
				description = fmt.Sprintf("Update Harga Emas Antam %s. Cek infografis untuk detailnya! #EmasAntam #DnarMasID", event.Date)
			}

			payload := repliz.Payload{
				Title:       fmt.Sprintf("Update Harga Emas Antam - %s", event.Date),
				Description: description,
				Topic:       "antamlogammulia",
				Type:        "album",
				Medias:      medias,
				Meta: repliz.Meta{
					Title:       "",
					Description: "",
					URL:         "",
				},
				AdditionalInfo: repliz.AdditionalInfo{
					IsAiGenerated: true,
					IsDraft:       false,
					Collaborators: []string{},
					Music: repliz.Music{
						ID:        "7637201849280711442",
						Artist:    "DnarMasID",
						Name:      "original sound - DnarMasID",
						Thumbnail: "",
					},
				},
				Replies:    []string{},
				AccountID:  cfg.ReplizTikTokAccountID,
				ScheduleAt: scheduleTime,
			}

			// Call Repliz API
			err = client.UploadPost(payload)
			if err != nil {
				log.Printf("[repliz-uploader] ❌ Failed to upload post to Repliz: %v", err)
			} else {
				log.Printf("[repliz-uploader] ✅ Successfully scheduled post to Repliz for date %s (Scheduled at: %s)", event.Date, scheduleTime)
			}
		}
	}
}
