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

	// Log active platforms
	platforms := getActivePlatforms(cfg)
	if len(platforms) == 0 {
		log.Println("[repliz-uploader] ⚠️ Warning: No platform account IDs configured")
	} else {
		for _, p := range platforms {
			log.Printf("[repliz-uploader] 📱 Platform active: %s (type=%s)", p.Name, p.PostType)
		}
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
			processEvent(client, platforms, event)
		}
	}
}

func processEvent(client *repliz.Client, platforms []PlatformTarget, event models.MediaGenerationCompletedEvent) {
	if len(platforms) == 0 {
		log.Println("[repliz-uploader] ⚠️ No platforms configured, skipping")
		return
	}

	// Fallback caption
	description := event.Caption
	if description == "" {
		log.Printf("[repliz-uploader] ⚠️ Caption empty, using enriched fallback for date %s", event.Date)
		description = fmt.Sprintf(`Harga Emas Antam Hari Ini

Tanggal: %s

Cek infografis untuk detail harga jual dan buyback hari ini.

Pantau terus pergerakan harga emas agar tidak ketinggalan momentum investasi terbaik!

Dapatkan update harga real-time dan alert otomatis lewat bot Telegram kami. Klik link di bio untuk mulai!

#investasiemas #hargaemas #logammulia #emasantam #tipskeuangan`, event.Date)
	}

	scheduleTime := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)

	for _, p := range platforms {
		var medias []repliz.Media
		var postType string
		var title string

		switch p.PostType {
		case PostTypeAlbum:
			// Album: infografis + screenshot + CTA
			postType = "album"
			title = fmt.Sprintf("Update Harga Emas Antam - %s", event.Date)
			medias = buildAlbumMedias(event)

		case PostTypeImage:
			// Single image: infografis only
			postType = "image"
			title = ""
			medias = buildSingleImageMedias(event)
		}

		payload := repliz.Payload{
			Title:       title,
			Description: description,
			Topic:       "antamlogammulia",
			Type:        postType,
			Medias:      medias,
			Meta:        repliz.Meta{},
			AdditionalInfo: repliz.AdditionalInfo{
				IsAiGenerated: false,
				IsDraft:       false,
				Collaborators: []string{},
				Music: repliz.Music{
					ID:        "",
					Artist:    "",
					Name:      "",
					Thumbnail: "",
				},
				Products: []string{},
				Tags:     []string{},
				Mentions: []string{},
				Link:     "",
			},
			Replies:    []string{},
			AccountID:  p.AccountID,
			ScheduleAt: scheduleTime,
		}

		err := client.UploadPost(payload)
		if err != nil {
			log.Printf("[repliz-uploader] ❌ %s upload failed: %v", p.Name, err)
		} else {
			log.Printf("[repliz-uploader] ✅ %s (%s) scheduled for %s at %s", p.Name, p.PostType, event.Date, scheduleTime)
		}
	}
}

// buildAlbumMedias builds multi-image media array (TikTok carousel)
func buildAlbumMedias(event models.MediaGenerationCompletedEvent) []repliz.Media {
	medias := make([]repliz.Media, 0)

	if event.InfographicURL != "" {
		medias = append(medias, repliz.Media{
			Alt:             "Infografis Harga Emas",
			Type:            "image",
			Thumbnail:       event.InfographicURL,
			URL:             event.InfographicURL,
			CustomThumbnail: false,
		})
	}

	if event.ScreenshotPriceURL != "" {
		medias = append(medias, repliz.Media{
			Alt:       "Screenshot Harga Emas",
			Type:      "image",
			Thumbnail: event.ScreenshotPriceURL,
			URL:       event.ScreenshotPriceURL,
		})
	}

	if event.CTAImageURL != "" {
		medias = append(medias, repliz.Media{
			Alt:       "Call to Action - DnarMasID",
			Type:      "image",
			Thumbnail: event.CTAImageURL,
			URL:       event.CTAImageURL,
		})
	}

	return medias
}

// buildSingleImageMedias builds single-image media array (Instagram/Facebook)
func buildSingleImageMedias(event models.MediaGenerationCompletedEvent) []repliz.Media {
	var medias []repliz.Media

	// Single image: infografis only
	if event.InfographicURL != "" {
		medias = append(medias, repliz.Media{
			Alt:             "",
			Type:            "image",
			Thumbnail:       event.InfographicURL,
			URL:             event.InfographicURL,
			CustomThumbnail: false,
		})
	}

	return medias
}
