package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"dnarmasid/shared/config"
	"dnarmasid/shared/db"
	"dnarmasid/shared/models"
	"dnarmasid/shared/queue"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	log.Println("🤖 [telegram-bot] Starting DnarMasID Telegram Bot...")

	cfg := config.Load()
	database := db.Connect(cfg)
	q := queue.NewClient(cfg)

	database.AutoMigrate(&models.Subscriber{})

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		log.Fatalf("[telegram-bot] ❌ Failed to init bot: %v", err)
	}

	log.Printf("[telegram-bot] ✅ Authorized as @%s", bot.Self.UserName)

	broadcaster := NewBroadcaster(cfg, database, bot)
	tracker := NewProgressTracker(bot)
	handler := NewCommandHandler(cfg, database, bot, q, tracker)

	// Register command menu (setMyCommands)
	if err := registerBotCommands(bot); err != nil {
		log.Printf("[telegram-bot] ⚠️ Failed to register commands: %v", err)
	} else {
		log.Printf("[telegram-bot] ✅ Bot commands registered")
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup

	// ─── Goroutine 1: Listen command dari user (subscribe/unsubscribe/dll)
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.Listen(quit)
	}()

	// ─── Goroutine 2: Consume content.ready → kirim caption ke admin
	//     Jika ini republish session, update progress bar
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("[telegram-bot] 📡 Listening content.ready queue...")
		for {
			select {
			case <-quit:
				return
			default:
				var event models.ContentReadyEvent
				err := q.ConsumeJSON(queue.KeyContentReady, 5*time.Second, &event)
				if err != nil {
					continue
				}
				log.Printf("[telegram-bot] 📥 content.ready received: date=%s, price_id=%d", event.Date, event.PriceID)

				// Send caption via broadcaster (normal pipeline)
				if err := broadcaster.SendContent(&event); err != nil {
					log.Printf("[telegram-bot] ❌ SendContent error: %v", err)
				}

				// If this is a republish session, update progress + send to admin
				sess := tracker.GetSession(event.PriceID)
				if sess == nil {
					log.Printf("[telegram-bot] ⚠️ No republish session found for price_id=%d", event.PriceID)
				}
				sess = tracker.UpdateStep(event.PriceID, "AI Caption", "done", "Caption generated")
				if sess != nil {
					tracker.EditMessage(sess)

					// Send caption to admin
					content, ok := event.Contents[models.PlatformGeneral]
					if ok && content != "" {
						tracker.SendToAdmin(sess.ChatID, fmt.Sprintf("✍️ *Caption Generated — %s*\n\n%s", event.Date, content))
					}
				}
			}
		}
	}()

	// ─── Goroutine 3: Consume media.ready → kirim gambar/video ke admin
	//     Jika ini republish session, update progress bar + send media to admin
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("[telegram-bot] 📡 Listening media.ready queue...")
		for {
			select {
			case <-quit:
				return
			default:
				var event models.MediaReadyEvent
				err := q.ConsumeJSON(queue.KeyMediaReady, 5*time.Second, &event)
				if err != nil {
					continue
				}
				log.Printf("[telegram-bot] 📥 media.ready received: %s (%s), price_id=%d", event.FileName, event.MediaType, event.PriceID)

				// Send media via broadcaster (normal pipeline)
				if err := broadcaster.SendMedia(&event); err != nil {
					log.Printf("[telegram-bot] ❌ SendMedia error: %v", err)
				}

				// If this is a republish session, update progress + send media to admin
				if event.MediaType == models.MediaTypeImage {
					sess := tracker.GetSession(event.PriceID)
					if sess == nil {
						log.Printf("[telegram-bot] ⚠️ media.ready: No republish session for price_id=%d", event.PriceID)
					}
					sess = tracker.UpdateStep(event.PriceID, "Media Render", "done", "Infografis uploaded")
					if sess != nil {
						tracker.EditMessage(sess)

						// Send media to admin via URL
						if event.PublicURL != "" {
							caption := fmt.Sprintf("🖼️ *Infografis — %s*\nSiap diposting ke sosmed.", event.Date)
							params := tgbotapi.Params{}
							params.AddNonZero64("chat_id", sess.ChatID)
							params.AddNonEmpty("photo", event.PublicURL)
							params.AddNonEmpty("caption", caption)
							params.AddNonEmpty("parse_mode", "Markdown")
							if _, err := bot.MakeRequest("sendPhoto", params); err != nil {
								log.Printf("[telegram-bot] ⚠️ sendPhoto to admin error: %v", err)
							}
						}
					}
				}
			}
		}
	}()

	// ─── Goroutine 4: Consume bot.media.done → update Repliz step (dedicated channel, no competition)
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("[telegram-bot] 📡 Listening bot.media.done queue...")
		for {
			select {
			case <-quit:
				return
			default:
				var event models.MediaGenerationCompletedEvent
				err := q.ConsumeJSON(queue.KeyBotMediaDone, 5*time.Second, &event)
				if err != nil {
					continue
				}
				log.Printf("[telegram-bot] 📥 bot.media.done received: date=%s, price_id=%d", event.Date, event.PriceID)

				// If this is a republish session, mark Repliz as done
				sess := tracker.GetSession(event.PriceID)
				if sess == nil {
					log.Printf("[telegram-bot] ⚠️ bot.media.done: No republish session for price_id=%d", event.PriceID)
				}
				sess = tracker.UpdateStep(event.PriceID, "Repliz Upload", "done", "Queued for posting")
				if sess != nil {
					tracker.EditMessage(sess)
					tracker.SendToAdmin(sess.ChatID, fmt.Sprintf("🚀 *Republish Complete* — %s\n\n✅ Semua tahap selesai. Proses posting berjalan otomatis ke Instagram/Facebook.", event.Date))
					go tracker.RemoveSession(event.PriceID)
				}
			}
		}
	}()

	// ─── Goroutine 5: Consume scrape.failed → kirim alert ke admin
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("[telegram-bot] 📡 Listening scrape.failed queue...")
		for {
			select {
			case <-quit:
				return
			default:
				var event models.ScrapeFailedEvent
				err := q.ConsumeJSON(queue.KeyScrapeFailed, 5*time.Second, &event)
				if err != nil {
					continue
				}
				log.Printf("[telegram-bot] 📥 scrape.failed received: date=%s", event.Date)
				if err := broadcaster.SendScrapeFailureNotification(&event); err != nil {
					log.Printf("[telegram-bot] ❌ SendScrapeFailureNotification error: %v", err)
				}
			}
		}
	}()

	// ─── Goroutine 6: Timeout watcher — mark stale republish sessions as failed
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("[telegram-bot] ⏱️ Republish timeout watcher active (timeout: 120s)")
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-quit:
				return
			case <-ticker.C:
				staleSessions := tracker.GetStaleSessions(120 * time.Second)
				for _, sess := range staleSessions {
					log.Printf("[telegram-bot] ⏱️ Session %d (%s) timed out, marking failed", sess.PriceID, sess.Date)
					sess = tracker.MarkFailed(sess.PriceID, "Timeout (120s)")
					if sess != nil {
						tracker.EditMessage(sess)
						tracker.SendToAdmin(sess.ChatID, fmt.Sprintf("🔴 *Republish Failed* — %s\n\nPipeline timeout setelah 120 detik. Periksa log untuk detail.", sess.Date))
						go tracker.RemoveSession(sess.PriceID)
					}
				}
			}
		}
	}()

	log.Println("[telegram-bot] ✅ All goroutines running.")
	<-quit
	log.Println("[telegram-bot] Shutting down...")
	wg.Wait()
	log.Println("[telegram-bot] Stopped.")
}