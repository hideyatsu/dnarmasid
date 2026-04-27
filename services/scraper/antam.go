package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/models"

	"github.com/chromedp/chromedp"
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
func (s *AntamScraper) Run(forceDummy bool) (*models.GoldScrapedEvent, error) {
	if forceDummy {
		return s.runDummy()
	}
	log.Printf("[scraper] Scraping Antam: %s", s.cfg.AntamURL)

	// Gunakan lokasi Asia/Jakarta secara konsisten
	loc, _ := time.LoadLocation("Asia/Jakarta")
	today := time.Now().In(loc).Truncate(24 * time.Hour)

	// 1. Jalankan scraping menggunakan chromedp (bypass anti-bot)
	updateTime, prices, err := s.scrapeWithChromedp(today)
	if err != nil {
		return nil, fmt.Errorf("scrape error: %w", err)
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices found")
	}

	// 2. Cek apakah update time sudah ada di DB (untuk gram 1)
	// Kita cek record terbaru berdasarkan source_update_time
	var latestRecord models.GoldPrice
	result := s.db.Where("gram = 1").Order("source_update_time desc").First(&latestRecord)
	if result.Error == nil && latestRecord.SourceUpdateTime != nil && latestRecord.SourceUpdateTime.Equal(updateTime) {
		log.Printf("[scraper] ℹ️ Jam update sama (%v). Skip pipeline.", updateTime.Format("15:04:05"))
		return nil, fmt.Errorf("no update since last scrape")
	}

	parsedDate := updateTime.Truncate(24 * time.Hour)

	// 3. Simpan atau Update ke MySQL
	var didChange bool
	for i := range prices {
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
			// Sudah ada data hari ini, cek apakah perlu update jam
			if existing.SourceUpdateTime == nil || !existing.SourceUpdateTime.Equal(updateTime) {
				// Ada update baru di jam yang berbeda
				existing.BuyPrice = prices[i].BuyPrice
				existing.SellPrice = prices[i].SellPrice
				existing.SourceUpdateTime = &updateTime
				if err := s.db.Save(&existing).Error; err != nil {
					log.Printf("[scraper] ❌ Failed to update gram %.1f: %v", prices[i].Gram, err)
				} else {
					prices[i].ID = existing.ID
					didChange = true
				}
			}
		}
	}

	if !didChange {
		log.Printf("[scraper] ℹ️ Tidak ada perubahan data. Skip pipeline.")
		return nil, fmt.Errorf("no change in price")
	}

	// 4. Hitung perubahan vs kemarin (gram 1)
	changePct, changeAmt, trend, bbChangeAmt, bbTrend := s.calcChange(parsedDate, prices)

	updateTimeStr := updateTime.Format("02 Jan 2006 15:04:05")
	updateTimeStr = strings.ReplaceAll(updateTimeStr, "May", "Mei")
	updateTimeStr = strings.ReplaceAll(updateTimeStr, "Aug", "Agt")
	updateTimeStr = strings.ReplaceAll(updateTimeStr, "Oct", "Okt")
	updateTimeStr = strings.ReplaceAll(updateTimeStr, "Dec", "Des")

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

func (s *AntamScraper) runDummy() (*models.GoldScrapedEvent, error) {
	log.Println("[scraper] 🧪 Running in FORCE DUMMY mode")
	loc, _ := time.LoadLocation("Asia/Jakarta")
	today := time.Now().In(loc).Truncate(24 * time.Hour)

	prices := devFallbackPrices(today)

	// Simpan ke DB agar calcChange bisa bekerja (perlu data kemarin atau hari ini)
	for i := range prices {
		s.db.Save(&prices[i])
	}

	// Buat event dummy
	event := &models.GoldScrapedEvent{
		Date:             today.Format("2006-01-02"),
		UpdateTime:       today.Format("02 Jan 2006 10:00:00"),
		PriceID:          prices[0].ID,
		Prices:           prices,
		ChangePct:        1.25,
		ChangeAmt:        15000,
		Trend:            "down",
		BuybackChangeAmt: 0,
		BuybackTrend:     "stable",
	}

	return event, nil
}

// scrapeWithChromedp uses chromedp headless browser to bypass anti-bot protection
func (s *AntamScraper) scrapeWithChromedp(defaultDate time.Time) (time.Time, []models.GoldPrice, error) {
	var prices []models.GoldPrice
	scrapedDate := defaultDate

	chromeTimeout := time.Duration(s.cfg.ScrapeTimeoutSeconds*5+120) * time.Second
	log.Printf("[scraper] 🔧 Chrome timeout set to: %v", chromeTimeout)

	chromePath := os.Getenv("CHROME_BIN")
	if chromePath == "" {
		chromePath = "/usr/bin/google-chrome"
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer allocCancel()

	ctx, cancel := context.WithTimeout(allocCtx, chromeTimeout)
	defer cancel()

	ctx, cancel = chromedp.NewContext(ctx)
	defer cancel()

	log.Printf("[scraper] 🔍 Starting simplified chromedp navigation to: %s", s.cfg.AntamURL)

	var htmlContent string
	var err error
	maxRetries := 2
	var lastUpdateStr string
	var price1gStr string
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("[scraper] 🔄 Retry attempt %d/%d...", attempt, maxRetries)
			time.Sleep(5 * time.Second)
		}

		var buf []byte
		err = chromedp.Run(ctx,
			chromedp.Navigate(s.cfg.AntamURL),
			chromedp.WaitVisible("body", chromedp.ByQuery),

			// Handle Modal (Click Cancel)
			chromedp.ActionFunc(func(ctx context.Context) error {
				log.Printf("[scraper] ⏳ Checking for location modal...")
				timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()

				err := chromedp.WaitVisible("button.swal-button--cancel", chromedp.ByQuery).Do(timeoutCtx)
				if err == nil {
					log.Printf("[scraper] 🖱️ Modal detected. Clicking Cancel button...")
					return chromedp.Click("button.swal-button--cancel", chromedp.ByQuery).Do(ctx)
				}
				log.Printf("[scraper] ℹ️ Modal not found or already dismissed. Proceeding...")
				return nil
			}),

			// Wait for hero price components
			chromedp.WaitVisible(".hero-price", chromedp.ByQuery),
			chromedp.WaitVisible(".child-4 p span.text", chromedp.ByQuery),
			chromedp.WaitVisible(".child-2 .price .current", chromedp.ByQuery),

			// Small screen for compact screenshot as requested
			chromedp.EmulateViewport(400, 800),
			chromedp.Screenshot(".hero-price", &buf, chromedp.ByQuery),
			chromedp.ActionFunc(func(ctx context.Context) error {
				s.saveDebugFile(fmt.Sprintf("hero_price_attempt%d.png", attempt+1), buf)
				return nil
			}),

			// Extract data
			chromedp.Text(".child-4 p span.text", &lastUpdateStr, chromedp.ByQuery),
			chromedp.Text(".child-2 .price .current", &price1gStr, chromedp.ByQuery),
			chromedp.OuterHTML("html", &htmlContent, chromedp.ByQuery),
		)

		if err == nil {
			break
		}
		log.Printf("[scraper] ⚠️ Attempt %d failed: %v", attempt+1, err)
	}

	if err != nil {
		return scrapedDate, nil, fmt.Errorf("chromedp navigation failed: %w", err)
	}

	log.Printf("[scraper] 🔍 Raw update time string: %q", lastUpdateStr)
	log.Printf("[scraper] 🔍 Raw price string: %q", price1gStr)

	if strings.Contains(lastUpdateStr, "Perubahan terakhir:") {
		timeStr := strings.TrimSpace(strings.ReplaceAll(lastUpdateStr, "Perubahan terakhir:", ""))
		timeStr = strings.ReplaceAll(timeStr, "Mei", "May")
		timeStr = strings.ReplaceAll(timeStr, "Agt", "Aug")
		timeStr = strings.ReplaceAll(timeStr, "Okt", "Oct")
		timeStr = strings.ReplaceAll(timeStr, "Des", "Dec")

		loc, _ := time.LoadLocation("Asia/Jakarta")
		if t, err := time.ParseInLocation("02 Jan 2006 15:04:05", timeStr, loc); err == nil {
			scrapedDate = t
			log.Printf("[scraper] ✅ Parsed update time: %s", scrapedDate.Format("2006-01-02 15:04:05"))
		}
	}

	if price1gStr != "" {
		price := parsePrice(price1gStr)
		log.Printf("[scraper] 💰 Parsed price: %d from %q", price, price1gStr)
		if price > 0 {
			prices = append(prices, models.GoldPrice{
				Date:             scrapedDate.Truncate(24 * time.Hour),
				Gram:             1,
				BuyPrice:         price,
				SellPrice:        0, // Simplified: set to 0 or handled later
				SourceURL:        s.cfg.AntamURL,
				SourceUpdateTime: &scrapedDate,
			})
		}
	}

	log.Printf("[scraper] ✅ Extracted %d price entries (1g only)", len(prices))

	// 7. Get Buyback Price
	log.Printf("[scraper] 🔍 Navigating to buyback page for detailed data (fresh context)...")
	var buybackPriceStr string
	var buybackBuf []byte

	// Gunakan context baru khusus untuk buyback agar tidak kena deadline dari navigasi sebelumnya
	bbCtx, bbCancel := chromedp.NewContext(allocCtx)
	defer bbCancel()

	// Timeout 5 menit khusus untuk buyback
	bbCtx, bbCancel = context.WithTimeout(bbCtx, 5*time.Minute)
	defer bbCancel()

	err = chromedp.Run(bbCtx,
		chromedp.Navigate("https://www.logammulia.com/id/sell/gold"),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		// Pengecekan dinamis untuk modal dan chart-info
		chromedp.ActionFunc(func(ctx context.Context) error {
			log.Printf("[scraper] ⏳ Menunggu .chart-info atau modal (polling)...")
			for i := 0; i < 30; i++ { // Polling selama 150 detik
				var hasChart bool
				_ = chromedp.Evaluate(`document.querySelector(".chart-info") !== null`, &hasChart).Do(ctx)
				if hasChart {
					log.Printf("[scraper] ✅ .chart-info terdeteksi!")
					// Tunggu sebentar agar benar-benar visible/rendered
					time.Sleep(2 * time.Second)
					return nil
				}

				var hasModal bool
				_ = chromedp.Evaluate(`document.querySelector(".swal-button--cancel") !== null`, &hasModal).Do(ctx)
				if hasModal {
					log.Printf("[scraper] 🖱️ Modal terdeteksi saat polling. Klik Cancel...")
					_ = chromedp.Click(".swal-button--cancel", chromedp.ByQuery).Do(ctx)
				} else {
					// Cek juga tombol OK jika Cancel tidak ada
					_ = chromedp.Evaluate(`document.querySelector(".swal-button--confirm") !== null`, &hasModal).Do(ctx)
					if hasModal {
						log.Printf("[scraper] 🖱️ Modal terdeteksi (tombol OK). Klik OK...")
						_ = chromedp.Click(".swal-button--confirm", chromedp.ByQuery).Do(ctx)
					}
				}

				time.Sleep(5 * time.Second)
			}
			return fmt.Errorf("timeout: .chart-info tidak muncul setelah 150 detik")
		}),
		chromedp.WaitVisible(".chart-info", chromedp.ByQuery),
		chromedp.Screenshot(".right", &buybackBuf, chromedp.ByQuery),
		chromedp.Value("input#valBasePrice", &buybackPriceStr, chromedp.ByQuery),
	)

	if err != nil {
		// Screenshot body saat gagal untuk debug
		var failBuf []byte
		_ = chromedp.Run(bbCtx, chromedp.Screenshot("body", &failBuf, chromedp.ByQuery))
		s.saveDebugFile("buyback_failed_capture.png", failBuf)
		log.Printf("[scraper] ⚠️ Failed to get buyback price: %v (screenshot saved)", err)
	} else {
		s.saveDebugFile("buyback_info.png", buybackBuf)
		bbPrice := parsePrice(buybackPriceStr)
		log.Printf("[scraper] 💰 Parsed buyback price: %d from %q", bbPrice, buybackPriceStr)
		if bbPrice > 0 {
			for i := range prices {
				if prices[i].Gram == 1 {
					prices[i].SellPrice = bbPrice
				}
			}
		}
	}

	return scrapedDate, prices, nil
}

// scrape mengambil data harga dari logammulia.com
func (s *AntamScraper) scrape(defaultDate time.Time) (time.Time, []models.GoldPrice, error) {
	var prices []models.GoldPrice
	scrapedDate := defaultDate

	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	c.SetRequestTimeout(time.Duration(s.cfg.ScrapeTimeoutSeconds) * time.Second)

	c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "en-US,en;q=0.9,id;q=0.8")
		r.Headers.Set("Cache-Control", "max-age=0")
		r.Headers.Set("Sec-Ch-Ua", `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`)
		r.Headers.Set("Sec-Ch-Ua-Mobile", "?0")
		r.Headers.Set("Sec-Ch-Ua-Platform", `"Windows"`)
		r.Headers.Set("Sec-Fetch-Dest", "document")
		r.Headers.Set("Sec-Fetch-Mode", "navigate")
		r.Headers.Set("Sec-Fetch-Site", "none")
		r.Headers.Set("Sec-Fetch-User", "?1")
		r.Headers.Set("Upgrade-Insecure-Requests", "1")
	})

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

	cBuyback.OnRequest(func(r *colly.Request) {
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "en-US,en;q=0.9,id;q=0.8")
		r.Headers.Set("Cache-Control", "max-age=0")
		r.Headers.Set("Sec-Ch-Ua", `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`)
		r.Headers.Set("Sec-Ch-Ua-Mobile", "?0")
		r.Headers.Set("Sec-Ch-Ua-Platform", `"Windows"`)
		r.Headers.Set("Sec-Fetch-Dest", "document")
		r.Headers.Set("Sec-Fetch-Mode", "navigate")
		r.Headers.Set("Sec-Fetch-Site", "none")
		r.Headers.Set("Sec-Fetch-User", "?1")
		r.Headers.Set("Upgrade-Insecure-Requests", "1")
	})

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

func (s *AntamScraper) saveDebugFile(filename string, data []byte) {
	debugDir := "/tmp/scraper-debug"
	_ = os.MkdirAll(debugDir, 0755)

	path := filepath.Join(debugDir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("[scraper] ❌ Failed to save debug file %s: %v", filename, err)
	} else {
		log.Printf("[scraper] 🛡️ Debug file saved: %s", path)
	}
}

func stripTags(s string) string {
	var res strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			res.WriteRune(r)
		}
	}
	return strings.TrimSpace(res.String())
}

func parseGram(s string) float64 {
	s = strings.TrimSpace(s)
	// Ambil hanya angka dan titik/koma
	var res strings.Builder
	hasDot := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			res.WriteRune(r)
		} else if (r == '.' || r == ',') && !hasDot {
			res.WriteRune('.')
			hasDot = true
		}
	}
	v, _ := strconv.ParseFloat(res.String(), 64)
	return v
}

func parsePrice(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	// Jika format input value "2620000.00"
	if strings.Contains(s, ".") && !strings.Contains(s, ",") && !strings.Contains(s, "Rp") {
		parts := strings.Split(s, ".")
		v, _ := strconv.ParseInt(parts[0], 10, 64)
		return v
	}

	// Jika format "Rp 2,620,000" (US style thousand separator)
	if strings.Contains(s, ",") && !strings.Contains(s, ".") {
		s = strings.ReplaceAll(s, ",", "")
	}

	// Default: ambil hanya angka sampai sebelum desimal (ID style: koma adalah desimal)
	var res strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			res.WriteRune(r)
		} else if r == ',' {
			// Biasanya harga Antam "Rp1.000.000,00", kita ambil angka sebelum desimal
			break
		}
	}
	v, _ := strconv.ParseInt(res.String(), 10, 64)
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
