package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"
	"dnarmasid/shared/queue"
)

func main() {
	cfg := config.Load()
	q := queue.NewClient(cfg)

	// Data dummy GoldScrapedEvent untuk men-trigger ai-generator
	scrapedEvent := models.GoldScrapedEvent{
		Date:       time.Now().Format("2006-01-02"),
		UpdateTime: time.Now().Format("02 Jan 2006 15:04:05"),
		PriceID:    1,
		Prices: []models.GoldPrice{
			{Gram: 1, BuyPrice: 1350000, SellPrice: 1240000},
			{Gram: 0.5, BuyPrice: 725000, SellPrice: 620000},
			{Gram: 2, BuyPrice: 2650000, SellPrice: 2450000},
		},
		ChangePct:        0.8,
		ChangeAmt:        10000,
		Trend:            "up",
		BuybackChangeAmt: 5000,
		BuybackTrend:     "up",
	}

	err := q.Publish(queue.KeyGoldScrapedAI, scrapedEvent)
	if err != nil {
		log.Fatalf("❌ Failed to publish to %s: %v", queue.KeyGoldScrapedAI, err)
	}

	data, _ := json.MarshalIndent(scrapedEvent, "", "  ")
	fmt.Printf("📥 Pushed to %s:\n%s\n\n", queue.KeyGoldScrapedAI, string(data))
	log.Println("✅ Dummy gold.scraped.ai pushed to Redis.")
}
