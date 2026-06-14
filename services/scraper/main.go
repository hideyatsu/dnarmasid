package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dnarmasid/services/scraper/chrome"
	"dnarmasid/services/storage"
	"dnarmasid/shared/config"
	"dnarmasid/shared/db"
	"dnarmasid/shared/models"
	"dnarmasid/shared/queue"
	"encoding/json"
	"net/http"
	"sync/atomic"
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

	// Auto migrate table
	database.AutoMigrate(&models.GoldPrice{}, &models.PipelineLog{})

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
			event, err := scraper.Run(forceDummy)

			// Cleanup Chrome after EVERY run to be safe
			chromeManager.Cleanup()

			if err != nil {
				atomic.AddInt64(&jobsFailed, 1)
				log.Printf("[scraper] ❌ Scrape failed: %v", err)

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

			// Publish hasil ke Redis → ai, media (Fan-out)
			// NOTE: Telegram bot tidak lagi dikirim langsung — hanya via content.ready
			// dari ai-generator untuk menghindari duplikasi pesan.
			if err := q.Publish(queue.KeyGoldScrapedAI, event); err != nil {
				log.Printf("[scraper] ❌ Failed to publish to ai: %v", err)
			}
			if err := q.Publish(queue.KeyGoldScrapedMedia, event); err != nil {
				log.Printf("[scraper] ❌ Failed to publish to media: %v", err)
			}
			if err := q.Publish(queue.KeyGoldScrapedThreads, event); err != nil {
				log.Printf("[scraper] ❌ Failed to publish to threads: %v", err)
			}

			log.Printf("[scraper] ✅ gold.scraped published | Date: %s | Trend: %s | Change: %+.2f%%",
				event.Date, event.Trend, event.ChangePct)
		}
	}
}
