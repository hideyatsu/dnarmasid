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
				log.Printf("[telegram-bot] 📥 content.ready received: date=%s", event.Date)

				// Send caption via broadcaster (normal pipeline)
				if err := broadcaster.SendContent(&event); err != nil {
					log.Printf("[telegram-bot] ❌ SendContent error: %v", err)
				}

				// If this is a republish session, update progress + send to admin
				if sess := tracker.UpdateStep(event.PriceID, "AI Caption", "done", "Caption generated"); sess != nil {
					tracker.EditMessage(sess)

					// Send caption to admin
					content, ok := event.Contents[models.PlatformGeneral]
					if ok && content != "" {
						tracker.SendToAdmin(sess.ChatID, fmt.Sprintf("✍️ *Caption Generated*\n\n%s", content))
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
				log.Printf("[telegram-bot] 📥 media.ready received: %s (%s)", event.FileName, event.MediaType)

				// Send media via broadcaster (normal pipeline)
				if err := broadcaster.SendMedia(&event); err != nil {
					log.Printf("[telegram-bot] ❌ SendMedia error: %v", err)
				}

				// If this is a republish session, update progress + send media to admin
				if event.MediaType == models.MediaTypeImage {
					sess := tracker.UpdateStep(event.PriceID, "Media Render", "done", "Infografis uploaded")
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

	// ─── Goroutine 4: Consume media.generation.completed → update Repliz step
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("[telegram-bot] 📡 Listening media.generation.completed queue...")
		for {
			select {
			case <-quit:
				return
			default:
				var event models.MediaGenerationCompletedEvent
				err := q.ConsumeJSON(queue.KeyMediaGenerationCompleted, 5*time.Second, &event)
				if err != nil {
					continue
				}
				log.Printf("[telegram-bot] 📥 media.generation.completed received: date=%s", event.Date)

				// If this is a republish session, mark Repliz as done
				sess := tracker.UpdateStep(event.PriceID, "Repliz Upload", "done", "Queued for posting")
				if sess != nil {
					tracker.EditMessage(sess)
					// Mark session complete and notify admin
					tracker.SendToAdmin(sess.ChatID, fmt.Sprintf("🚀 *Republish Complete* — %s\n\n✅ Semua tahap selesai. Proses posting berjalan otomatis ke Instagram/Facebook.", event.Date))
					// Clean up tracker
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

	log.Println("[telegram-bot] ✅ All goroutines running.")
	<-quit
	log.Println("[telegram-bot] Shutting down...")
	wg.Wait()
	log.Println("[telegram-bot] Stopped.")
}