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

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
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
	parsedDate, prices, err := s.scrapeWithChromedp(today)
	if err != nil {
		return nil, fmt.Errorf("scrape error: %w", err)
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("no prices found")
	}

	// Set update time dari scraped date
	updateTime := parsedDate

	// 3. Simpan atau Update ke MySQL (Option A)
	var didChange bool
	for i := range prices {
		prices[i].SourceUpdateTime = &updateTime

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
			if existing.SourceUpdateTime != nil && existing.SourceUpdateTime.Equal(updateTime) {
				isSameTime = true
			}

			if !isSameTime {
				// Ada update baru di hari yang sama (misal update sore)
				existing.BuyPrice = prices[i].BuyPrice
				existing.SellPrice = prices[i].SellPrice
				existing.SourceUpdateTime = &updateTime
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
	// Konversi kembali dari May -> Mei dsb agar enak dibaca user Indo
	updateTimeStr = updateTime.Format("02 Jan 2006 15:04:05")
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

	// Use even more generous timeout for interactions
	chromeTimeout := time.Duration(s.cfg.ScrapeTimeoutSeconds*5+120) * time.Second
	log.Printf("[scraper] 🔧 Chrome timeout set to: %v", chromeTimeout)

	// Get chrome path from env or use default
	chromePath := os.Getenv("CHROME_BIN")
	if chromePath == "" {
		chromePath = "/usr/bin/google-chrome"
	}

	// Configure Chrome with proper flags for Docker environment
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
		// "single-process" removed as it can cause "context canceled" in some Docker/Linux envs
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer allocCancel()

	ctx, cancel := context.WithTimeout(allocCtx, chromeTimeout)
	defer cancel()

	ctx, cancel = chromedp.NewContext(ctx)
	defer cancel()

	log.Printf("[scraper] 🔍 Starting chromedp navigation to: %s", s.cfg.AntamURL)

	// Array untuk menyimpan data dari chromedp
	var htmlContent string
	var pageTitle string

	// Navigate with retry logic
	var err error
	maxRetries := 2
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("[scraper] 🔄 Retry attempt %d/%d...", attempt, maxRetries)
			time.Sleep(5 * time.Second)
		}

		log.Printf("[scraper] 🔍 Navigating and handling modal (attempt %d)...", attempt+1)
		navStart := time.Now()

		var buf []byte
		err = chromedp.Run(ctx,
			// 1. Setup Geolocation
			browser.GrantPermissions([]browser.PermissionType{browser.PermissionTypeGeolocation}).WithOrigin(s.cfg.AntamURL),
			emulation.SetGeolocationOverride().
				WithLatitude(-6.2088).
				WithLongitude(106.8456).
				WithAccuracy(1),

			// 2. Navigate
			chromedp.Navigate(s.cfg.AntamURL),
			chromedp.WaitVisible("body", chromedp.ByQuery),
			chromedp.Screenshot("body", &buf, chromedp.ByQuery),
			chromedp.ActionFunc(func(ctx context.Context) error {
				s.saveDebugFile(fmt.Sprintf("step1_nav_attempt%d.png", attempt+1), buf)
				return nil
			}),

			// 3. Handle Modal
			chromedp.ActionFunc(func(ctx context.Context) error {
				log.Printf("[scraper] ⏳ Checking for location modal...")
				timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()

				err := chromedp.WaitVisible("button.swal-button--confirm", chromedp.ByQuery).Do(timeoutCtx)
				if err == nil {
					log.Printf("[scraper] 🖱️ Modal detected. Clicking OK button...")
					_ = chromedp.Screenshot("body", &buf, chromedp.ByQuery).Do(ctx)
					s.saveDebugFile(fmt.Sprintf("step2_modal_detected_attempt%d.png", attempt+1), buf)

					return chromedp.Click("button.swal-button--confirm", chromedp.ByQuery).Do(ctx)
				}
				log.Printf("[scraper] ℹ️ Modal not found or already dismissed (%v). Proceeding...", err)
				return nil
			}),

			// 4. Wait for the price table
			chromedp.WaitVisible("table.table-bordered", chromedp.ByQuery),
			chromedp.Screenshot("body", &buf, chromedp.ByQuery),
			chromedp.ActionFunc(func(ctx context.Context) error {
				s.saveDebugFile(fmt.Sprintf("step3_table_visible_attempt%d.png", attempt+1), buf)
				return nil
			}),

			chromedp.Sleep(3*time.Second),
			chromedp.OuterHTML("html", &htmlContent, chromedp.ByQuery),
		)

		navDuration := time.Since(navStart)
		log.Printf("[scraper] 🔍 Navigation & interaction took: %v", navDuration)

		if err == nil {
			s.saveDebugFile("final_page.html", []byte(htmlContent))
			break
		}
		log.Printf("[scraper] ⚠️ Attempt %d failed: %v", attempt+1, err)
	}

	if err != nil {
		log.Printf("[scraper] ❌ chromedp error after all attempts: %v", err)
		return scrapedDate, nil, fmt.Errorf("chromedp navigation failed: %w", err)
	}

	log.Printf("[scraper] ✅ Navigation successful, HTML length: %d chars", len(htmlContent))

	// Try to get page title separately (don't fail if not found)
	_ = chromedp.Run(ctx,
		chromedp.Text("h2.ngc-title", &pageTitle, chromedp.ByQuery),
	)

	// Parse date dari title: "Harga Emas Hari Ini, 26 Apr 2026"
	pageTitle = strings.TrimSpace(pageTitle)
	parts := strings.Split(pageTitle, ",")
	if len(parts) > 1 {
		dateStr := strings.TrimSpace(parts[1])
		dateStr = strings.ReplaceAll(dateStr, "Mei", "May")
		dateStr = strings.ReplaceAll(dateStr, "Agt", "Aug")
		dateStr = strings.ReplaceAll(dateStr, "Okt", "Oct")
		dateStr = strings.ReplaceAll(dateStr, "Des", "Dec")

		loc, _ := time.LoadLocation("Asia/Jakarta")
		if t, err := time.ParseInLocation("02 Jan 2006", dateStr, loc); err == nil {
			scrapedDate = t
			log.Printf("[scraper] ✅ Parsed date from title: %s", scrapedDate.Format("2006-01-02"))
		}
	}

	// Parse prices dari HTML content
	isEmasBatangan := false

	// Parse section headers
	if strings.Contains(htmlContent, ">Emas Batangan<") && !strings.Contains(htmlContent, ">Emas Batangan Gift Series<") {
		isEmasBatangan = true
	}

	// Split by table rows
	rows := strings.Split(htmlContent, "<tr>")
	for _, row := range rows {
		// Check if this is a section header (th with colspan)
		if strings.Contains(row, "th colspan") || strings.Contains(row, "<th>") {
			thMatch := strings.Index(row, "<th")
			if thMatch != -1 {
				// Find closing </th>
				thEnd := strings.Index(row[thMatch:], "</th>")
				if thEnd != -1 {
					thContent := row[thMatch : thMatch+thEnd]
					thText := stripTags(thContent)

					if thText == "Emas Batangan" {
						isEmasBatangan = true
					} else if strings.Contains(thText, "Gift Series") || strings.Contains(thText, "Selamat") ||
						strings.Contains(thText, "Imlek") || strings.Contains(thText, "Batik") ||
						strings.Contains(thText, "Perak") {
						isEmasBatangan = false
					}
				}
			}
		}

		if !isEmasBatangan {
			continue
		}

		// Parse td cells
		tdCount := strings.Count(row, "<td")
		if tdCount < 2 {
			continue
		}

		// Extract td contents
		var cols []string
		tdMatches := strings.Split(row, "<td")
		for i := 1; i < len(tdMatches); i++ {
			tdContent := tdMatches[i]
			// Find closing </td> or </tr>
			tdEnd := strings.Index(tdContent, "</td>")
			if tdEnd == -1 {
				tdEnd = strings.Index(tdContent, "</tr>")
			}
			if tdEnd != -1 {
				cellContent := tdContent[:tdEnd]
				cellText := stripTags("<td" + cellContent)
				cols = append(cols, cellText)
			}
		}

		if len(cols) >= 2 {
			gram := parseGram(cols[0])
			buyPrice := parsePrice(cols[1])

			if gram > 0 && buyPrice > 0 {
				prices = append(prices, models.GoldPrice{
					Date:      scrapedDate,
					Gram:      gram,
					BuyPrice:  buyPrice,
					SellPrice: 0,
					SourceURL: s.cfg.AntamURL,
				})
			}
		}
	}

	log.Printf("[scraper] ✅ Extracted %d price entries via chromedp", len(prices))

	// 7. Get Buyback Price — use a fresh context so we don't inherit the exhausted deadline
	log.Printf("[scraper] 🔍 Navigating to buyback page (fresh context)...")
	buybackCtx, buybackCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer buybackCancel()

	bbCtx, bbCancel := context.WithTimeout(buybackCtx, 90*time.Second)
	defer bbCancel()

	bbCtx, bbCancel = chromedp.NewContext(bbCtx)
	defer bbCancel()

	var buybackHTML string
	var buybackValue string
	err = chromedp.Run(bbCtx,
		chromedp.Navigate("https://www.logammulia.com/id/sell/gold"),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		// Handle modal on buyback page too
		chromedp.ActionFunc(func(ctx context.Context) error {
			timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := chromedp.WaitVisible("button.swal-button--confirm", chromedp.ByQuery).Do(timeoutCtx); err == nil {
				log.Printf("[scraper] 🖱️ Buyback modal detected. Clicking OK...")
				return chromedp.Click("button.swal-button--confirm", chromedp.ByQuery).Do(ctx)
			}
			return nil
		}),
		chromedp.WaitVisible("input#valBasePrice", chromedp.ByQuery),
		chromedp.Value("input#valBasePrice", &buybackValue, chromedp.ByQuery),
		chromedp.OuterHTML("html", &buybackHTML, chromedp.ByQuery),
	)

	if err != nil {
		log.Printf("[scraper] ⚠️ Failed to get buyback price: %v", err)
		s.saveDebugFile("buyback_failed.html", []byte(buybackHTML))
	} else {
		log.Printf("[scraper] ✅ Buyback base value found: %s", buybackValue)
		s.saveDebugFile("buyback_page.html", []byte(buybackHTML))

		// Parse buyback (format: "1234567.00")
		bbParts := strings.Split(buybackValue, ".")
		if len(bbParts) > 0 {
			if baseBuyback, err := strconv.ParseInt(bbParts[0], 10, 64); err == nil && baseBuyback > 0 {
				log.Printf("[scraper] 💰 Applying buyback price: %d per gram", baseBuyback)
				for i := range prices {
					prices[i].SellPrice = int64(prices[i].Gram * float64(baseBuyback))
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
