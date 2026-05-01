package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"dnarmasid/shared/models"
)

// GenerateVideo — placeholder, butuh FFmpeg di production
func (g *MediaGenerator) GenerateVideo(event *models.GoldScrapedEvent) (*models.MediaReadyEvent, error) {
	fileName := fmt.Sprintf("gold_%s.mp4.todo", event.Date)
	filePath := filepath.Join(g.cfg.MediaOutputPath, fileName)

	f, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}
	f.WriteString(fmt.Sprintf("Video placeholder for %s\nGenerated at: %s",
		event.Date, time.Now().Format(time.RFC3339)))
	f.Close()

	log.Printf("[media-generator] 🎬 Video placeholder created: %s", fileName)

	media := models.GeneratedMedia{
		PriceID:   event.PriceID,
		MediaType: models.MediaTypeVideo,
		FilePath:  filePath,
		FileName:  fileName,
		Status:    "pending",
	}
	g.db.Create(&media)

	return &models.MediaReadyEvent{
		PriceID:   event.PriceID,
		Date:      event.Date,
		MediaType: models.MediaTypeVideo,
		FilePath:  filePath,
		FileName:  fileName,
		ScreenshotPriceURL:   event.ScreenshotPriceURL,
		ScreenshotBuybackURL: event.ScreenshotBuybackURL,
	}, nil
}
