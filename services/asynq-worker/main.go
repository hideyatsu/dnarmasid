package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"dnarmasid/internal/handlers"
	internalQueue "dnarmasid/internal/queue"
	"dnarmasid/internal/tasks"
	"dnarmasid/shared/config"
	"dnarmasid/shared/queue"
)

func main() {
	log.Println("🔄 [asynq-worker] Starting DnarMasID Asynq Worker...")

	cfg := config.Load()

	if !cfg.UseAsynq {
		log.Println("[asynq-worker] ⚠️ USE_ASYNQ=false. Worker will exit.")
		log.Println("[asynq-worker] Set USE_ASYNQ=true to enable Asynq processing.")
		return
	}

	// Shadow mode check
	shadowMode := os.Getenv("ASYNQ_SHADOW_MODE") != "false"
	handlers.ShadowMode = shadowMode
	if shadowMode {
		log.Println("[asynq-worker] 👻 SHADOW MODE: handlers will log only, no side effects")
	} else {
		log.Println("[asynq-worker] 🚀 LIVE MODE: handlers will execute real actions")
		// Initialize Redis queue for bridge mode
		handlers.RedisQueue = queue.NewClient(cfg)
		log.Println("[asynq-worker] Redis bridge initialized for live mode")
	}

	redisAddr := fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort)
	server := internalQueue.NewAsynqServer(redisAddr)

	// Register handlers
	server.HandleFunc(tasks.TypeScrape, handlers.HandleScrape)
	server.HandleFunc(tasks.TypeGenerateAI, handlers.HandleGenerateAI)
	server.HandleFunc(tasks.TypeGenerateMedia, handlers.HandleGenerateMedia)
	server.HandleFunc(tasks.TypeNotifyTelegram, handlers.HandleNotifyTelegram)
	server.HandleFunc(tasks.TypeUpload, handlers.HandleUpload)

	// Graceful shutdown
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("[asynq-worker] Shutting down...")
		server.Shutdown()
	}()

	log.Printf("[asynq-worker] ✅ Ready. Concurrency=%d", cfg.AsynqConcurrency)
	if err := server.Run(); err != nil {
		log.Fatalf("[asynq-worker] Server error: %v", err)
	}

	log.Println("[asynq-worker] Stopped.")
}
