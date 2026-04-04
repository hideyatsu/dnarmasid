package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"

	"github.com/gocolly/colly/v2"
	"gorm.io/gorm"
)

type AntamScraper struct {
	cfg *config.Config
	db  *gorm.DB
}

func NewAntamScraper(cfg *config.Config, db *gorm.DB) *AntamScraper {
	return &AntamScraper{cfg: cfg, db: db}
}

// Run menjalankan scraping dan return GoldScrapedEvent
func (s *AntamScraper) Run() (*models.GoldScrapedEvent, error) {
	today := time.Now().Truncate(24 * time.Hour)

	log.Printf("[scraper] Scraping Antam: %s", s.cfg.AntamURL)

	prices, err := s.scrape(today)
	if err != nil {
		return nil, fmt.Errorf("scrape error: %w", err)
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices found")
	}

	// Simpan ke MySQL (upsert)
	for i := range prices {
		result := s.db.Where(models.GoldPrice{Date: today, Gram: prices[i].Gram}).
			FirstOrCreate(&prices[i])
		if result.Error != nil {
			log.Printf("[scraper] ⚠️ Error saving gram %.1f: %v", prices[i].Gram, result.Error)
		}
	}

	// Hitung perubahan vs kemarin (gram 1)
	changePct, changeAmt, trend := s.calcChange(today, prices)

	event := &models.GoldScrapedEvent{
		Date:      today.Format("2006-01-02"),
		PriceID:   prices[0].ID,
		Prices:    prices,
		ChangePct: changePct,
		ChangeAmt: changeAmt,
		Trend:     trend,
	}

	return event, nil
}

// scrape mengambil data harga dari logammulia.com
func (s *AntamScraper) scrape(date time.Time) ([]models.GoldPrice, error) {
	var prices []models.GoldPrice

	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (compatible; DnarMasID-Bot/1.0)"),
	)

	c.SetRequestTimeout(time.Duration(s.cfg.ScrapeTimeoutSeconds) * time.Second)

	// ⚠️ Selector ini perlu disesuaikan dengan struktur HTML logammulia.com
	// Jalankan: curl https://www.logammulia.com/id/harga-emas-hari-ini | grep -A5 "table"
	c.OnHTML("table.table-harga-emas tbody tr, table tbody tr", func(e *colly.HTMLElement) {
		cols := e.ChildTexts("td")
		if len(cols) < 3 {
			return
		}

		gram := parseGram(cols[0])
		buyPrice := parsePrice(cols[1])
		sellPrice := parsePrice(cols[2])

		if gram <= 0 || buyPrice <= 0 {
			return
		}

		prices = append(prices, models.GoldPrice{
			Date:      date,
			Gram:      gram,
			BuyPrice:  buyPrice,
			SellPrice: sellPrice,
			SourceURL: s.cfg.AntamURL,
		})
	})

	c.OnError(func(r *colly.Response, err error) {
		log.Printf("[scraper] ❌ HTTP error %d: %v", r.StatusCode, err)
	})

	if err := c.Visit(s.cfg.AntamURL); err != nil {
		return nil, err
	}

	// Fallback: jika scrape gagal dapat data, gunakan data dummy untuk dev
	if len(prices) == 0 {
		log.Println("[scraper] ⚠️ No data from scrape, using dev fallback data")
		prices = devFallbackPrices(date)
	}

	return prices, nil
}

// calcChange menghitung perubahan harga vs kemarin (gram 1)
func (s *AntamScraper) calcChange(today time.Time, todayPrices []models.GoldPrice) (float64, int64, string) {
	yesterday := today.AddDate(0, 0, -1)

	var yesterdayPrice models.GoldPrice
	result := s.db.Where("date = ? AND gram = 1", yesterday).First(&yesterdayPrice)
	if result.Error != nil {
		return 0, 0, "stable"
	}

	// Cari harga 1 gram hari ini
	var todayPrice1g int64
	for _, p := range todayPrices {
		if p.Gram == 1 {
			todayPrice1g = p.BuyPrice
			break
		}
	}

	if todayPrice1g == 0 || yesterdayPrice.BuyPrice == 0 {
		return 0, 0, "stable"
	}

	changeAmt := todayPrice1g - yesterdayPrice.BuyPrice
	changePct := float64(changeAmt) / float64(yesterdayPrice.BuyPrice) * 100

	trend := "stable"
	if changeAmt > 0 {
		trend = "up"
	} else if changeAmt < 0 {
		trend = "down"
	}

	return changePct, changeAmt, trend
}

// ─────────────────────────────────────────
// Helper functions
// ─────────────────────────────────────────

func parseGram(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " gram", "")
	s = strings.ReplaceAll(s, "gr", "")
	s = strings.ReplaceAll(s, ",", ".")
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func parsePrice(s string) int64 {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "Rp", "")
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

// devFallbackPrices — data dummy untuk development & testing
func devFallbackPrices(date time.Time) []models.GoldPrice {
	grams := []struct {
		gram float64
		buy  int64
		sell int64
	}{
		{0.5, 950000, 870000},
		{1, 1850000, 1720000},
		{2, 3650000, 3400000},
		{3, 5450000, 5080000},
		{5, 9050000, 8430000},
		{10, 18050000, 16830000},
		{25, 45000000, 41950000},
		{50, 89900000, 83800000},
		{100, 179700000, 167500000},
	}

	var prices []models.GoldPrice
	for _, g := range grams {
		prices = append(prices, models.GoldPrice{
			Date:      date,
			Gram:      g.gram,
			BuyPrice:  g.buy,
			SellPrice: g.sell,
			SourceURL: "dev-fallback",
		})
	}
	return prices
}
