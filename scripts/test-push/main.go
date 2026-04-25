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

	// 1. Push ContentReadyEvent
	contentEvent := models.ContentReadyEvent{
		PriceID: 1,
		Date:    time.Now().Format("2006-01-02"),
		Contents: map[models.Platform]string{
			models.PlatformInstagram: "Harga emas Antam hari ini naik lagi nih! 🚀\n\nGram: 1g\nHarga: Rp 1.350.000\n\nYuk investasi emas sekarang! #emas #antam #investasi",
			models.PlatformTwitter:   "Harga emas Antam hari ini: Rp 1.350.000 (+Rp 10.000). Tren: Naik 📈 #EmasAntam #HargaEmas",
			models.PlatformGeneral:   "Harga emas hari ini menunjukkan tren positif dengan kenaikan signifikan pada pecahan 1 gram.",
		},
		Analysis: "Harga sedang dalam tren Bullish. Cocok untuk hold jangka panjang.",
	}

	publish(q, queue.KeyContentReady, contentEvent)

	// 2. Push MediaReadyEvent
	mediaEvent := models.MediaReadyEvent{
		PriceID:   1,
		Date:      time.Now().Format("2006-01-02"),
		MediaType: models.MediaTypeImage,
		FilePath:  "/app/outputs/dummy.png",
		FileName:  "dummy.png",
	}
	publish(q, queue.KeyMediaReady, mediaEvent)

	// 3. Push GoldScrapedEvent
	scrapedEvent := models.GoldScrapedEvent{
		Date:       time.Now().Format("2006-01-02"),
		UpdateTime: time.Now().Format("02 Jan 2006 15:04:05"),
		PriceID:    1,
		Prices: []models.GoldPrice{
			{Gram: 1, BuyPrice: 1350000, SellPrice: 1240000},
			{Gram: 0.5, BuyPrice: 725000, SellPrice: 620000},
		},
		ChangePct:        0.75,
		ChangeAmt:        10000,
		Trend:            "up",
		BuybackChangeAmt: 5000,
		BuybackTrend:     "up",
	}
	publish(q, queue.KeyGoldScrapedBot, scrapedEvent)

	log.Println("✅ All dummy events pushed to Redis.")
}

func publish(q *queue.Client, key string, payload any) {
	err := q.Publish(key, payload)
	if err != nil {
		log.Fatalf("❌ Failed to publish to %s: %v", key, err)
	}

	data, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Printf("📥 Pushed to %s:\n%s\n\n", key, string(data))
}
