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

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/gocolly/colly/v2"
	"golang.org/x/sync/errgroup"
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

	// Parallel scraping using errgroup
	g, ctx := errgroup.WithContext(allocCtx)

	var homeUpdateTime time.Time
	var homeBuyPrice int64
	var buybackSellPrice int64

	// 1. Scrape Home Page
	g.Go(func() error {
		hCtx, hCancel := chromedp.NewContext(ctx)
		defer hCancel()
		hCtx, hCancel = context.WithTimeout(hCtx, chromeTimeout)
		defer hCancel()

		var lastUpdateStr string
		var price1gStr string
		var buf []byte

		err := chromedp.Run(hCtx,
			network.SetBlockedURLS([]string{"*google-analytics.com*", "*googletagmanager.com*", "*facebook.net*", "*doubleclick.net*", "*hotjar.com*"}),
			chromedp.Navigate(s.cfg.AntamURL),
			chromedp.WaitVisible("body", chromedp.ByQuery),

			chromedp.ActionFunc(func(ctx context.Context) error {
				for i := 0; i < 10; i++ {
					var hasChart bool
					_ = chromedp.Evaluate(`document.querySelector(".hero-price") !== null`, &hasChart).Do(ctx)
					if hasChart { break }

					var hasModal bool
					_ = chromedp.Evaluate(`
						(function() {
							const btn = document.querySelector(".swal-button--cancel");
							if (btn) {
								btn.click();
								return true;
							}
							return false;
						})()
					`, &hasModal).Do(ctx)
					if hasModal {
						log.Printf("[scraper] 🛡️ Home modal detected and closed")
						time.Sleep(1 * time.Second)
					}
					time.Sleep(2 * time.Second)
				}
				return nil
			}),

			chromedp.WaitVisible(".hero-price", chromedp.ByQuery),
			chromedp.EmulateViewport(400, 800),
			chromedp.ActionFunc(func(ctx context.Context) error {
				// Wait a bit for chart to stabilize
				time.Sleep(1 * time.Second)
				return nil
			}),
			chromedp.Screenshot(".hero-price", &buf, chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector(".child-4 p span.text")?.innerText || ""`, &lastUpdateStr),
			chromedp.Evaluate(`document.querySelector(".child-2 .price .current")?.innerText || ""`, &price1gStr),
		)

		if err != nil {
			return fmt.Errorf("home scrape failed: %w", err)
		}

		s.saveDebugFile("hero_price.png", buf)
		homeBuyPrice = parsePrice(price1gStr)

		if strings.Contains(lastUpdateStr, "Perubahan terakhir:") {
			timeStr := strings.TrimSpace(strings.ReplaceAll(lastUpdateStr, "Perubahan terakhir:", ""))
			timeStr = strings.ReplaceAll(timeStr, "Mei", "May")
			timeStr = strings.ReplaceAll(timeStr, "Agt", "Aug")
			timeStr = strings.ReplaceAll(timeStr, "Okt", "Oct")
			timeStr = strings.ReplaceAll(timeStr, "Des", "Dec")
			loc, _ := time.LoadLocation("Asia/Jakarta")
			if t, err := time.ParseInLocation("02 Jan 2006 15:04:05", timeStr, loc); err == nil {
				homeUpdateTime = t
			}
		}
		return nil
	})

	// 2. Scrape Buyback Page
	g.Go(func() error {
		bbCtx, bbCancel := chromedp.NewContext(ctx)
		defer bbCancel()
		bbCtx, bbCancel = context.WithTimeout(bbCtx, chromeTimeout)
		defer bbCancel()

		var buybackPriceStr string
		var buf []byte

		err := chromedp.Run(bbCtx,
			network.SetBlockedURLS([]string{"*google-analytics.com*", "*googletagmanager.com*", "*facebook.net*", "*doubleclick.net*", "*hotjar.com*"}),
			chromedp.Navigate("https://www.logammulia.com/id/sell/gold"),
			chromedp.WaitVisible("body", chromedp.ByQuery),

			chromedp.ActionFunc(func(ctx context.Context) error {
				for i := 0; i < 10; i++ {
					var hasChart bool
					_ = chromedp.Evaluate(`document.querySelector(".chart-info") !== null`, &hasChart).Do(ctx)
					if hasChart { break }

					var hasModal bool
					_ = chromedp.Evaluate(`
						(function() {
							const btn = document.querySelector(".swal-button--cancel");
							if (btn) {
								btn.click();
								return true;
							}
							return false;
						})()
					`, &hasModal).Do(ctx)
					if hasModal {
						log.Printf("[scraper] 🛡️ Buyback modal detected and closed")
						time.Sleep(1 * time.Second)
					}
					time.Sleep(2 * time.Second)
				}
				return nil
			}),

			chromedp.WaitVisible(".chart-info", chromedp.ByQuery),
			chromedp.Screenshot(".right", &buf, chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector("input#valBasePrice")?.value || ""`, &buybackPriceStr),
		)

		if err != nil {
			// Save failed capture for debug but don't fail the whole group if possible?
			// Actually, if buyback fails, we still want the buy price if it's a new day.
			// But for now, let's treat it as an error.
			var failBuf []byte
			_ = chromedp.Run(bbCtx, chromedp.Screenshot("body", &failBuf, chromedp.ByQuery))
			s.saveDebugFile("buyback_failed.png", failBuf)
			return fmt.Errorf("buyback scrape failed: %w", err)
		}

		s.saveDebugFile("buyback_info.png", buf)
		buybackSellPrice = parsePrice(buybackPriceStr)
		return nil
	})

	if err := g.Wait(); err != nil {
		return defaultDate, nil, err
	}

	prices := []models.GoldPrice{
		{
			Date:             homeUpdateTime.Truncate(24 * time.Hour),
			Gram:             1,
			BuyPrice:         homeBuyPrice,
			SellPrice:        buybackSellPrice,
			SourceURL:        s.cfg.AntamURL,
			SourceUpdateTime: &homeUpdateTime,
		},
	}

	log.Printf("[scraper] ✅ Parallel scrape complete. Buy: %d, Sell: %d", homeBuyPrice, buybackSellPrice)
	return homeUpdateTime, prices, nil
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
