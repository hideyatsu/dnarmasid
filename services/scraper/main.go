package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"dnarmasid/services/scraper/chrome"
	"dnarmasid/services/storage"
	"dnarmasid/shared/config"
	"dnarmasid/shared/db"
	"dnarmasid/shared/models"
	"dnarmasid/shared/queue"
)

var (
	jobsReceived int64
	jobsFailed   int64
	lastJobTime  atomic.Value // holds time.Time
)

func main() {
	log.Println("🔍 [scraper] Starting DnarMasID Scraper...")

	cfg := config.Load()
	database := db.Connect(cfg)
	q := queue.NewClient(cfg)

	r2Uploader, err := storage.NewR2Uploader(cfg)
	if err != nil {
		log.Printf("[scraper] ⚠️ R2 Storage not configured: %v", err)
	}

	// Auto migrate tables
	database.AutoMigrate(&models.GoldPrice{}, &models.PipelineLog{}, &models.GeneratedMedia{})

	startTime := time.Now()
	chromeManager := chrome.NewManager()

	// Global Zombie Reaper — Reaps orphaned processes (Chrome children)
	go func() {
		log.Println("[scraper] 🛡️ Global Zombie Reaper started")
		for {
			var ws syscall.WaitStatus
			pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
			if pid > 0 {
				// Successfully reaped a child process
			}
			if err != nil && err == syscall.ECHILD {
				// No child processes left to wait for.
				// We sleep longer to save CPU, as parent usually reaps directly.
				time.Sleep(30 * time.Second)
				continue
			}
			time.Sleep(2 * time.Second)
		}
	}()

	scraper := NewAntamScraper(cfg, database, r2Uploader, chromeManager)

	// Health endpoint
	go func() {
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			lastJob, _ := lastJobTime.Load().(time.Time)
			status := "ok"

			// If stalled for more than 30 minutes, mark as degraded
			if !lastJob.IsZero() && time.Since(lastJob) > 30*time.Minute {
				status = "degraded"
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"status":           status,
				"chrome_instances": chromeManager.Count(),
				"jobs_received":    atomic.LoadInt64(&jobsReceived),
				"jobs_failed":      atomic.LoadInt64(&jobsFailed),
				"last_job_at":      lastJob.Format(time.RFC3339),
				"stalled_minutes": func() int64 {
					if lastJob.IsZero() {
						return 0
					}
					return int64(time.Since(lastJob).Minutes())
				}(),
				"uptime_seconds": time.Since(startTime).Seconds(),
			})
		})
		log.Println("[scraper] 🏥 Health endpoint listening on :9090")
		if err := http.ListenAndServe(":9090", nil); err != nil {
			log.Printf("[scraper] ❌ Health server failed: %v", err)
		}
	}()

	log.Println("[scraper] ✅ Ready. Waiting for job.scrape events...")

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	lastJobTime.Store(time.Time{})

	pollTicker := time.NewTicker(15 * time.Minute)
	defer pollTicker.Stop()

	for {
		select {
		case <-quit:
			log.Println("[scraper] Shutting down...")
			chromeManager.Cleanup()
			return
		case <-pollTicker.C:
			log.Printf("[scraper] 💓 still polling... (jobs=%d failed=%d)",
				atomic.LoadInt64(&jobsReceived), atomic.LoadInt64(&jobsFailed))
		default:
			// Blocking consume — tunggu job dari scheduler
			var job map[string]string
			err := q.ConsumeJSON(queue.KeyJobScrape, 5*time.Second, &job)
			if err != nil {
				// Timeout normal, lanjut loop
				continue
			}

			atomic.AddInt64(&jobsReceived, 1)
			lastJobTime.Store(time.Now())
			log.Printf("[scraper] 📥 Job received: %v", job)

			forceDummy := job["mode"] == "dummy" || job["force_dummy"] == "true"

			// Jalankan scraping
			event, price1g, err := scraper.Run(forceDummy)

			// Cleanup Chrome after EVERY run to be safe
			chromeManager.Cleanup()

			if err != nil {
				atomic.AddInt64(&jobsFailed, 1)
				log.Printf("[scraper] ❌ Scrape failed: %v", err)

				notifyScrapeFailure(err)

				// Publish failure event to telegram-bot
				failEvent := models.ScrapeFailedEvent{
					Date:    time.Now().Format("2006-01-02"),
					Source:  "Antam",
					Message: err.Error(),
				}
				if pubErr := q.Publish(queue.KeyScrapeFailed, failEvent); pubErr != nil {
					log.Printf("[scraper] ❌ Failed to publish failure event: %v", pubErr)
				}
				continue
			}

			// Guardrail: scraper.Run() returns (nil, nil) when no new data detected
			if event == nil {
				log.Println("[scraper] ℹ️ No new data (guardrail triggered skip). Skip fan-out publish.")
				notifyScrapeSuccess("Skipped - up to date", price1g)
				continue
			}

			notifyScrapeSuccess("New data ingested", price1g)

			// Publish hasil ke Redis → ai, media (Fan-out)
			// NOTE: Telegram bot tidak lagi dikirim langsung — hanya via content.ready
			// dari ai-generator untuk menghindari duplikasi pesan.
			if err := q.Publish(queue.KeyGoldScrapedAI, event); err != nil {
				log.Printf("[scraper] ❌ Failed to publish to ai: %v", err)
			}
			if err := q.Publish(queue.KeyGoldScrapedThreads, event); err != nil {
				log.Printf("[scraper] ❌ Failed to publish to threads: %v", err)
			}

			log.Printf("[scraper] ✅ gold.scraped published | Date: %s | Trend: %s | Change: %+.2f%%",
				event.Date, event.Trend, event.ChangePct)
		}
	}
}

// ─────────────────────────────────────────
// ht-notify Notification Helpers
// ─────────────────────────────────────────

// notifyScrapeSuccess sends a success notification to ht-notify (non-blocking)
func notifyScrapeSuccess(action string, price1g int64) {
	text := buildSuccessText(action, price1g, currentTimeStr())
	sendHtNotification(text)
}

// notifyScrapeFailure sends a failure notification to ht-notify (non-blocking)
func notifyScrapeFailure(err error) {
	text := buildFailureText(err, currentTimeStr())
	sendHtNotification(text)
}

func buildSuccessText(action string, price1g int64, timeStr string) string {
	return fmt.Sprintf(
		"📊 <b>[dnarmasid-scraper] Gold Price Scrape Succeeded</b>\n\n"+
			"• <b>Status:</b> 🟢 SUCCESS\n"+
			"• <b>Action:</b> %s\n"+
			"• <b>Price 1g:</b> Rp %s\n"+
			"• <b>Time:</b> %s",
		action,
		formatRupiah(price1g),
		timeStr,
	)
}

func buildFailureText(err error, timeStr string) string {
	errMsg := "unknown error"
	if err != nil {
		errMsg = err.Error()
	}
	return fmt.Sprintf(
		"⚠️ <b>[dnarmasid-scraper] Gold Price Scrape Failed</b>\n\n"+
			"• <b>Status:</b> 🔴 FAILED\n"+
			"• <b>Error:</b> <code>%s</code>\n"+
			"• <b>Time:</b> %s",
		html.EscapeString(errMsg),
		timeStr,
	)
}

func currentTimeStr() string {
	loc, err := time.LoadLocation("Asia/Jakarta")
	t := time.Now()
	if err == nil {
		t = t.In(loc)
	}
	return t.Format("2006-01-02 15:04:05 WIB")
}

// formatRupiah formats an integer with thousands dot separator (e.g. 1850000 -> 1.850.000)
func formatRupiah(amount int64) string {
	if amount < 0 {
		return "-" + formatRupiah(-amount)
	}
	s := strconv.FormatInt(amount, 10)
	n := len(s)
	if n <= 3 {
		return s
	}
	var b strings.Builder
	lead := n % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < n; i += 3 {
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// sendHtNotification delivers payload asynchronously to ht-notify gateway
// ponytail: stdlib-only background dispatcher; non-blocking HTTP post
func sendHtNotification(text string) {
	go func() {
		notifyURL := os.Getenv("NOTIFY_URL")
		if notifyURL == "" {
			notifyURL = "https://notify.hidayahteknologi.web.id/api/v1/notify"
		}
		notifyToken := os.Getenv("NOTIFY_TOKEN")
		if notifyToken == "" {
			notifyToken = "ht_sec_59b5ddf0fa1e60346f4d60c8fc2ead55c4e1afd67b30f56b"
		}

		payload := map[string]string{
			"source":     "dnarmasid-scraper",
			"target":     "log",
			"channel":    "telegram",
			"priority":   "default",
			"parse_mode": "HTML",
			"text":       text,
		}

		if err := sendHTTPRequest(notifyURL, notifyToken, payload); err != nil {
			log.Printf("[scraper] ⚠️ ht-notify error: %v", err)
		} else {
			log.Printf("[scraper] 📢 ht-notify: notification dispatched successfully")
		}
	}()
}

func sendHTTPRequest(endpoint, token string, payload map[string]string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Service-Name", "dnarmasid-scraper")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
