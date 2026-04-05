package main

import (
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
	handler := NewCommandHandler(cfg, database, bot)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup

	// ─── Goroutine 1: Listen command dari user (subscribe/unsubscribe/dll)
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.Listen(quit)
	}()

	/* 
	// ─── Goroutine 2: Consume content.ready → kirim caption ke admin
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
				if err := broadcaster.SendContent(&event); err != nil {
					log.Printf("[telegram-bot] ❌ SendContent error: %v", err)
				}
			}
		}
	}()

	// ─── Goroutine 3: Consume media.ready → kirim gambar/video ke admin
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
				if err := broadcaster.SendMedia(&event); err != nil {
					log.Printf("[telegram-bot] ❌ SendMedia error: %v", err)
				}
			}
		}
	}()
	*/

	// ─── Goroutine 4: Consume gold.scraped → kirim notifikasi awal ke admin
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("[telegram-bot] 📡 Listening gold.scraped queue...")
		for {
			select {
			case <-quit:
				return
			default:
				var event models.GoldScrapedEvent
				err := q.ConsumeJSON(queue.KeyGoldScraped, 5*time.Second, &event)
				if err != nil {
					continue
				}
				log.Printf("[telegram-bot] 📥 gold.scraped received: date=%s", event.Date)
				if err := broadcaster.SendScrapeNotification(&event); err != nil {
					log.Printf("[telegram-bot] ❌ SendScrapeNotification error: %v", err)
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
