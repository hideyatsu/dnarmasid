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
	log.Printf("[scraper] Scraping Antam: %s", s.cfg.AntamURL)

	// Gunakan lokasi Asia/Jakarta secara konsisten
	loc, _ := time.LoadLocation("Asia/Jakarta")
	today := time.Now().In(loc).Truncate(24 * time.Hour)

	// 1. Ambil update time dari landing page (sebagai gate)
	updateTime, err := s.scrapeUpdateTime()
	if err != nil {
		log.Printf("[scraper] ⚠️ Gagal mengambil update time: %v. Lanjut tanpa gate.", err)
	} else {
		log.Printf("[scraper] 🕐 Website update-time: %v", updateTime.Format("2006-01-02 15:04:05"))
	}

	// 2. Jalankan scraping detail harga
	parsedDate, prices, err := s.scrape(today)
	if err != nil {
		return nil, fmt.Errorf("scrape error: %w", err)
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices found")
	}

	// 3. Simpan atau Update ke MySQL (Option A)
	var didChange bool
	for i := range prices {
		prices[i].SourceUpdateTime = updateTime

		var existing models.GoldPrice
		result := s.db.Where("date = ? AND gram = ?", parsedDate, prices[i].Gram).First(&existing)

		if result.Error == gorm.ErrRecordNotFound {
			// Data baru untuk hari ini
			if err := s.db.Create(&prices[i]).Error; err != nil {
				log.Printf("[scraper] ❌ Failed to create gram %.1f: %v", prices[i].Gram, err)
			} else {
				didChange = true
			}
		} else if result.Error == nil {
			// Sudah ada data hari ini, cek apakah perlu update
			isSameTime := false
			if existing.SourceUpdateTime != nil && updateTime != nil && existing.SourceUpdateTime.Equal(*updateTime) {
				isSameTime = true
			}

			if !isSameTime {
				// Ada update baru di hari yang sama (misal update sore)
				existing.BuyPrice = prices[i].BuyPrice
				existing.SellPrice = prices[i].SellPrice
				existing.SourceUpdateTime = updateTime
				if err := s.db.Save(&existing).Error; err != nil {
					log.Printf("[scraper] ❌ Failed to update gram %.1f: %v", prices[i].Gram, err)
				} else {
					prices[i].ID = existing.ID // Penting untuk reference event
					didChange = true
				}
			}
		}
	}

	if !didChange {
		log.Printf("[scraper] ℹ️ Tidak ada perubahan (update time tetap). Skip pipeline.")
		return nil, fmt.Errorf("no update since last scrape")
	}

	// 4. Hitung perubahan vs kemarin (gram 1)
	changePct, changeAmt, trend, bbChangeAmt, bbTrend := s.calcChange(parsedDate, prices)

	updateTimeStr := ""
	if updateTime != nil {
		// Konversi kembali dari May -> Mei dsb agar enak dibaca user Indo
		updateTimeStr = updateTime.Format("02 Jan 2006 15:04:05")
		updateTimeStr = strings.ReplaceAll(updateTimeStr, "May", "Mei")
		updateTimeStr = strings.ReplaceAll(updateTimeStr, "Aug", "Agt")
		updateTimeStr = strings.ReplaceAll(updateTimeStr, "Oct", "Okt")
		updateTimeStr = strings.ReplaceAll(updateTimeStr, "Dec", "Des")
	}

	event := &models.GoldScrapedEvent{
		Date:             parsedDate.Format("2006-01-02"),
		UpdateTime:       updateTimeStr,
		PriceID:          prices[0].ID,
		Prices:           prices,
		ChangePct:        changePct,
		ChangeAmt:        changeAmt,
		Trend:            trend,
		BuybackChangeAmt: bbChangeAmt,
		BuybackTrend:     bbTrend,
	}

	return event, nil
}

// scrapeUpdateTime mengambil info "Perubahan terakhir" dari landing page
func (s *AntamScraper) scrapeUpdateTime() (*time.Time, error) {
	var updateTime *time.Time

	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (compatible; DnarMasID-Bot/1.0)"),
	)
	c.SetRequestTimeout(time.Duration(s.cfg.ScrapeTimeoutSeconds) * time.Second)

	// URL landing page biasanya basis dari AntamURL
	landingURL := s.cfg.AntamURL
	if strings.HasSuffix(landingURL, "/harga-emas-hari-ini") {
		landingURL = strings.ReplaceAll(landingURL, "/harga-emas-hari-ini", "")
	}

	c.OnHTML("body", func(e *colly.HTMLElement) {
		// Cari pattern text di body
		text := e.Text
		searchKey := "Perubahan terakhir:"
		idx := strings.Index(text, searchKey)
		if idx != -1 {
			// Extract string setela key (contoh: " 05 Apr 2026 07:31:00")
			raw := strings.TrimSpace(text[idx+len(searchKey) : idx+len(searchKey)+25])
			// Bersihkan potential noise di akhir
			parts := strings.Split(raw, "\n")
			dateStr := strings.TrimSpace(parts[0])

			// Normalisasi bulan
			dateStr = strings.ReplaceAll(dateStr, "Mei", "May")
			dateStr = strings.ReplaceAll(dateStr, "Agt", "Aug")
			dateStr = strings.ReplaceAll(dateStr, "Okt", "Oct")
			dateStr = strings.ReplaceAll(dateStr, "Des", "Dec")

			// Load lokasi WIB/Jakarta
			loc, _ := time.LoadLocation("Asia/Jakarta")

			// Layout: 02 Jan 2006 15:04:05
			if t, err := time.ParseInLocation("02 Jan 2006 15:04:05", dateStr, loc); err == nil {
				updateTime = &t
			}
		}
	})

	err := c.Visit(landingURL)
	if err != nil {
		return nil, err
	}

	if updateTime == nil {
		return nil, fmt.Errorf("pattern 'Perubahan terakhir' not found")
	}

	return updateTime, nil
}

// scrape mengambil data harga dari logammulia.com
func (s *AntamScraper) scrape(defaultDate time.Time) (time.Time, []models.GoldPrice, error) {
	var prices []models.GoldPrice
	scrapedDate := defaultDate

	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (compatible; DnarMasID-Bot/1.0)"),
	)

	c.SetRequestTimeout(time.Duration(s.cfg.ScrapeTimeoutSeconds) * time.Second)

	c.OnHTML("h2.ngc-title", func(e *colly.HTMLElement) {
		text := strings.TrimSpace(e.Text)
		parts := strings.Split(text, ",")
		if len(parts) > 1 {
			dateStr := strings.TrimSpace(parts[1])
			dateStr = strings.ReplaceAll(dateStr, "Mei", "May")
			dateStr = strings.ReplaceAll(dateStr, "Agt", "Aug")
			dateStr = strings.ReplaceAll(dateStr, "Okt", "Oct")
			dateStr = strings.ReplaceAll(dateStr, "Des", "Dec")
			loc, _ := time.LoadLocation("Asia/Jakarta")
			if t, err := time.ParseInLocation("02 Jan 2006", dateStr, loc); err == nil {
				scrapedDate = t
			}
		}
	})

	// ⚠️ Selector ini perlu disesuaikan dengan struktur HTML logammulia.com
	var isEmasBatangan bool
	c.OnHTML("table tbody tr", func(e *colly.HTMLElement) {
		thText := strings.TrimSpace(e.ChildText("th"))
		if thText != "" {
			if thText == "Emas Batangan" {
				isEmasBatangan = true
			} else if thText == "Emas Batangan Gift Series" || thText == "Emas Batangan Selamat Idul Fitri" || thText == "Emas Batangan Imlek" || thText == "Emas Batangan Batik Seri III" || thText == "Perak Murni" || thText == "Perak Heritage" {
				isEmasBatangan = false
			}
		}

		if !isEmasBatangan {
			return
		}

		cols := e.ChildTexts("td")
		if len(cols) < 2 {
			return
		}

		gram := parseGram(cols[0])
		buyPrice := parsePrice(cols[1])
		sellPrice := int64(0) // Tidak ada buyback price di tabel ini

		if gram <= 0 || buyPrice <= 0 {
			return
		}

		prices = append(prices, models.GoldPrice{
			Date:      scrapedDate,
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
		return scrapedDate, nil, err
	}

	// Scrape Harga Buyback dari url terpisah berdasarkan nilai valBasePrice (1 gram)
	var baseBuyback int64
	cBuyback := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (compatible; DnarMasID-Bot/1.0)"),
	)
	cBuyback.SetRequestTimeout(time.Duration(s.cfg.ScrapeTimeoutSeconds) * time.Second)

	cBuyback.OnHTML("input#valBasePrice", func(e *colly.HTMLElement) {
		val := e.Attr("value")
		// Format value misal: "2577000.00"
		parts := strings.Split(val, ".")
		if len(parts) > 0 {
			if parsed, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
				baseBuyback = parsed
			}
		}
	})

	cBuyback.Visit("https://www.logammulia.com/id/sell/gold")

	// Terapkan baseBuyback (harga buyback per gram) secara proporsional ke semua item
	if baseBuyback > 0 {
		for i := range prices {
			prices[i].SellPrice = int64(prices[i].Gram * float64(baseBuyback))
		}
	}

	// Fallback: jika scrape gagal dapat data, gunakan data dummy untuk dev
	if len(prices) == 0 {
		log.Println("[scraper] ⚠️ No data from scrape, using dev fallback data")
		prices = devFallbackPrices(scrapedDate)
	}

	return scrapedDate, prices, nil
}

// calcChange menghitung perubahan harga vs kemarin (gram 1)
func (s *AntamScraper) calcChange(today time.Time, todayPrices []models.GoldPrice) (float64, int64, string, int64, string) {
	yesterday := today.AddDate(0, 0, -1)

	var yesterdayPrice models.GoldPrice
	result := s.db.Where("date = ? AND gram = 1", yesterday.Format("2006-01-02")).First(&yesterdayPrice)
	if result.Error != nil {
		return 0, 0, "stable", 0, "stable"
	}

	// Cari harga 1 gram hari ini
	var todayPrice1g int64
	var todaySellPrice1g int64
	for _, p := range todayPrices {
		if p.Gram == 1 {
			todayPrice1g = p.BuyPrice
			todaySellPrice1g = p.SellPrice
			break
		}
	}

	if todayPrice1g == 0 || yesterdayPrice.BuyPrice == 0 {
		return 0, 0, "stable", 0, "stable"
	}

	changeAmt := todayPrice1g - yesterdayPrice.BuyPrice
	changePct := float64(changeAmt) / float64(yesterdayPrice.BuyPrice) * 100

	trend := "stable"
	if changeAmt > 0 {
		trend = "up"
	} else if changeAmt < 0 {
		trend = "down"
	}

	bbChangeAmt := todaySellPrice1g - yesterdayPrice.SellPrice
	bbTrend := "stable"
	if bbChangeAmt > 0 {
		bbTrend = "up"
	} else if bbChangeAmt < 0 {
		bbTrend = "down"
	}

	return changePct, changeAmt, trend, bbChangeAmt, bbTrend
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
