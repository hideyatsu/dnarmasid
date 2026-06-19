package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"dnarmasid/services/storage"
	"dnarmasid/shared/models"
)

// Publisher handles uploading to R2
type Publisher struct {
	r2Uploader *storage.R2Uploader
}

func NewPublisher(r2Uploader *storage.R2Uploader) *Publisher {
	return &Publisher{r2Uploader: r2Uploader}
}

// UploadVideo uploads the final video to R2
func (p *Publisher) UploadVideo(videoPath string, event *models.GoldScrapedEvent) (string, error) {
	if p.r2Uploader == nil {
		return "", fmt.Errorf("R2 uploader not configured")
	}

	videoData, err := os.ReadFile(videoPath)
	if err != nil {
		return "", fmt.Errorf("read video file: %w", err)
	}

	date := event.Date
	r2Key := fmt.Sprintf("video-shorts/%s-%d.mp4", date, time.Now().Unix())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	publicURL, err := p.r2Uploader.UploadFile(ctx, r2Key, videoData, "video/mp4")
	if err != nil {
		return "", fmt.Errorf("R2 upload: %w", err)
	}

	log.Printf("[publish] ✅ Video uploaded: %s", publicURL)
	return publicURL, nil
}