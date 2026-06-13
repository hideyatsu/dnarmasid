package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dnarmasid/services/scraper/chrome"
	"dnarmasid/services/storage"
	"dnarmasid/shared/config"
	"dnarmasid/shared/models"
	"dnarmasid/shared/utils"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/gocolly/colly/v2"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

type AntamScraper struct {
	cfg     *config.Config
	db      *gorm.DB
	storage storage.StorageService
	chrome  *chrome.Manager
}

func NewAntamScraper(cfg *config.Config, db *gorm.DB, storage storage.StorageService, chromeManager *chrome.Manager) *AntamScraper {
	return &AntamScraper{cfg: cfg, db: db, storage: storage, chrome: chromeManager}
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

	// 1. Jalankan scraping menggunakan chromedp (bypass anti-bot) atau API eksternal
	var updateTime time.Time
	var prices []models.GoldPrice
	var screenshotPrice, screenshotBuyback string
	var err error

	if s.cfg.ScraperAPIURL != "" {
		log.Printf("[scraper] 🌐 Menggunakan API Eksternal: %s", s.cfg.ScraperAPIURL)
		updateTime, prices, screenshotPrice, screenshotBuyback, err = s.scrapeWithAPI()
		if err != nil {
			log.Printf("[scraper] ⚠️ API Eksternal gagal: %v. Fallback ke scrape mandiri...", err)
			updateTime, prices, screenshotPrice, screenshotBuyback, err = s.scrapeWithChromedp(today)
		}
	} else {
		updateTime, prices, screenshotPrice, screenshotBuyback, err = s.scrapeWithChromedp(today)
	}

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
	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		// Fail-closed: DB error, skip scrape to prevent stale broadcast
		log.Printf("[scraper] ❌ Guardrail DB check failed: %v — skipping scrape to prevent stale broadcast", result.Error)
		return nil, fmt.Errorf("guardrail check failed: %w", result.Error)
	}
	if result.Error == nil && latestRecord.SourceUpdateTime != nil && latestRecord.SourceUpdateTime.Equal(updateTime) {
		log.Printf("[scraper] ℹ️ Waktu update sama (%v). Skip pipeline.", updateTime.Format("02 Jan 2006 15:04:05"))
		return nil, nil
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
		return nil, nil
	}

	// 4. Hitung perubahan vs kemarin (gram 1)
	changePct, changeAmt, trend, bbChangeAmt, bbTrend := s.calcChange(parsedDate, prices)

	updateTimeStr := updateTime.Format("02 Jan 2006 15:04:05")
	updateTimeStr = strings.ReplaceAll(updateTimeStr, "May", "Mei")
	updateTimeStr = strings.ReplaceAll(updateTimeStr, "Aug", "Agt")
	updateTimeStr = strings.ReplaceAll(updateTimeStr, "Oct", "Okt")
	updateTimeStr = strings.ReplaceAll(updateTimeStr, "Dec", "Des")

	dateStr := parsedDate.Format("02 Jan 2006")
	dateStr = strings.ReplaceAll(dateStr, "May", "Mei")
	dateStr = strings.ReplaceAll(dateStr, "Aug", "Agt")
	dateStr = strings.ReplaceAll(dateStr, "Oct", "Okt")
	dateStr = strings.ReplaceAll(dateStr, "Dec", "Des")

	event := &models.GoldScrapedEvent{
		Date:                 dateStr,
		UpdateTime:           updateTimeStr,
		PriceID:              prices[0].ID,
		Prices:               prices,
		ChangePct:            changePct,
		ChangeAmt:            changeAmt,
		Trend:                trend,
		BuybackChangeAmt:     bbChangeAmt,
		BuybackTrend:         bbTrend,
		ScreenshotPriceURL:   screenshotPrice,
		ScreenshotBuybackURL: screenshotBuyback,
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

	dateStr := today.Format("02 Jan 2006")
	dateStr = strings.ReplaceAll(dateStr, "May", "Mei")
	dateStr = strings.ReplaceAll(dateStr, "Aug", "Agt")
	dateStr = strings.ReplaceAll(dateStr, "Oct", "Okt")
	dateStr = strings.ReplaceAll(dateStr, "Dec", "Des")

	// Buat event dummy
	event := &models.GoldScrapedEvent{
		Date:                 dateStr,
		UpdateTime:           today.Format("02 Jan 2006 10:00:00"),
		PriceID:              prices[0].ID,
		Prices:               prices,
		ChangePct:            1.25,
		ChangeAmt:            15000,
		Trend:                "down",
		BuybackChangeAmt:     0,
		BuybackTrend:         "stable",
		ScreenshotPriceURL:   "https://r2.dnarmas.id/dummy_price.png",
		ScreenshotBuybackURL: "https://r2.dnarmas.id/dummy_buyback.png",
	}

	return event, nil
}

func (s *AntamScraper) scrapeWithAPI() (time.Time, []models.GoldPrice, string, string, error) {
	apiURL := fmt.Sprintf("%s/api/v1/prices/list?source=logammulia&brand=antam", strings.TrimSuffix(s.cfg.ScraperAPIURL, "/"))
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return time.Time{}, nil, "", "", fmt.Errorf("failed to create API request: %w", err)
	}

	if s.cfg.ScraperAPIKey != "" {
		req.Header.Set("X-API-KEY", s.cfg.ScraperAPIKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return time.Time{}, nil, "", "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return time.Time{}, nil, "", "", fmt.Errorf("API returned non-200 status: %d", resp.StatusCode)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Prices []struct {
				Category  string  `json:"category"`
				Weight    float64 `json:"weight"`
				BasePrice int64   `json:"base_price"`
			} `json:"prices"`
			Buybacks []struct {
				Weight float64 `json:"weight"`
				Price  int64   `json:"price"`
			} `json:"buybacks"`
			Screenshots []struct {
				Type          string `json:"type"`
				ScreenshotURL string `json:"screenshot_url"`
			} `json:"screenshots"`
		} `json:"data"`
		Metadata struct {
			SiteUpdateAt string `json:"site_update_at"`
		} `json:"metadata"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return time.Time{}, nil, "", "", fmt.Errorf("failed to decode API response: %w", err)
	}

	if payload.Status != "success" {
		return time.Time{}, nil, "", "", fmt.Errorf("API returned non-success status: %s", payload.Status)
	}

	updateTime, err := time.Parse(time.RFC3339, payload.Metadata.SiteUpdateAt)
	if err != nil {
		loc, _ := time.LoadLocation("Asia/Jakarta")
		updateTime = time.Now().In(loc)
	}

	var buybackSellPrice int64
	for _, bb := range payload.Data.Buybacks {
		if bb.Weight == 1 {
			buybackSellPrice = bb.Price
			break
		}
	}

	var screenshotPrice, screenshotBuyback string
	for _, ss := range payload.Data.Screenshots {
		if ss.Type == "price-update" && screenshotPrice == "" {
			screenshotPrice = ss.ScreenshotURL
		} else if ss.Type == "buyback-update" && screenshotBuyback == "" {
			screenshotBuyback = ss.ScreenshotURL
		}
		if screenshotPrice != "" && screenshotBuyback != "" {
			break
		}
	}

	var prices []models.GoldPrice
	for _, p := range payload.Data.Prices {
		if p.Category != "emas-batangan" {
			continue
		}

		sellPrice := int64(p.Weight * float64(buybackSellPrice))

		prices = append(prices, models.GoldPrice{
			Date:             updateTime.Truncate(24 * time.Hour),
			Gram:             p.Weight,
			BuyPrice:         p.BasePrice,
			SellPrice:        sellPrice,
			SourceURL:        s.cfg.ScraperAPIURL,
			SourceUpdateTime: &updateTime,
		})
	}

	return updateTime, prices, screenshotPrice, screenshotBuyback, nil
}

// scrapeWithChromedp uses chromedp headless browser to bypass anti-bot protection
func (s *AntamScraper) scrapeWithChromedp(defaultDate time.Time) (time.Time, []models.GoldPrice, string, string, error) {
	chromeTimeout := time.Duration(s.cfg.ScrapeTimeoutSeconds*5+120) * time.Second
	log.Printf("[scraper] 🔧 Chrome timeout set to: %v", chromeTimeout)

	chromePath := os.Getenv("CHROME_BIN")
	if chromePath == "" {
		chromePath = "/usr/bin/google-chrome"
	}

	port, err := s.chrome.GetFreePort()
	if err != nil {
		return defaultDate, nil, "", "", fmt.Errorf("failed to get free port: %w", err)
	}

	// Spawn Chrome using our manager
	cmd, err := s.chrome.Spawn(context.Background(), chromePath, port)
	if err != nil {
		return defaultDate, nil, "", "", fmt.Errorf("failed to spawn chrome: %w", err)
	}

	// Create remote allocator to connect to our spawned chrome
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d", port))
	defer allocCancel()

	// We don't need to defer cmd.Wait() here because main.go calls chromeManager.Cleanup()
	// which will kill and reap the process. But we can do it here for extra safety.
	defer s.chrome.CleanupOne(cmd)

	// Parallel scraping using errgroup
	g, ctx := errgroup.WithContext(allocCtx)

	var homeUpdateTime time.Time
	var homeBuyPrice int64
	var buybackSellPrice int64
	var screenshotPrice string
	var screenshotBuyback string

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
					if hasChart {
						break
					}

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
			chromedp.EmulateViewport(375, 667),
			chromedp.ActionFunc(func(ctx context.Context) error {
				// Wait a bit for layout to stabilize at mobile viewport
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

		jpegBuf, err := utils.ConvertPNGToJPEG(buf)
		if err != nil {
			log.Printf("[scraper] ⚠️ Failed converting hero price PNG to JPEG: %v", err)
			jpegBuf = buf // fallback to original
		}

		screenshotPrice = s.saveDebugFile("hero_price.jpg", jpegBuf)
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
					if hasChart {
						break
					}

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

			failJpeg, _ := utils.ConvertPNGToJPEG(failBuf)
			s.saveDebugFile("buyback_failed.jpg", failJpeg)
			return fmt.Errorf("buyback scrape failed: %w", err)
		}

		jpegBuf, err := utils.ConvertPNGToJPEG(buf)
		if err != nil {
			log.Printf("[scraper] ⚠️ Failed converting buyback PNG to JPEG: %v", err)
			jpegBuf = buf // fallback to original
		}

		screenshotBuyback = s.saveDebugFile("buyback_info.jpg", jpegBuf)
		buybackSellPrice = parsePrice(buybackPriceStr)
		return nil
	})

	if err := g.Wait(); err != nil {
		return defaultDate, nil, "", "", err
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
	return homeUpdateTime, prices, screenshotPrice, screenshotBuyback, nil
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

func (s *AntamScraper) saveDebugFile(filename string, data []byte) string {
	debugDir := "/tmp/scraper-debug"
	_ = os.MkdirAll(debugDir, 0755)

	path := filepath.Join(debugDir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("[scraper] ❌ Failed to save debug file %s: %v", filename, err)
	} else {
		log.Printf("[scraper] 🛡️ Debug file saved: %s", path)
	}

	// Upload to R2
	if s.storage != nil {
		url, err := s.storage.UploadFile(context.Background(), filename, data, "image/jpeg")
		if err != nil {
			log.Printf("[scraper] ❌ Failed to upload debug file %s to R2: %v", filename, err)
		} else {
			log.Printf("[scraper] ☁️ Debug file uploaded to R2: %s", url)
			// Hapus file lokal setelah berhasil upload
			os.Remove(path)
			return url
		}
	}
	return ""
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
