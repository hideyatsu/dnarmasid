package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	// MySQL
	MySQLHost     string
	MySQLPort     string
	MySQLUser     string
	MySQLPassword string
	MySQLDatabase string

	// Redis
	RedisHost string
	RedisPort string

	// Telegram
	TelegramBotToken        string
	TelegramChannelID       string
	TelegramAdminChatID     int64
	TelegramGroupID         int64
	TelegramThreadGeneralID int
	TelegramThreadPostID    int

	// AI (Ollama/Gemini)
	AIProvider   string
	OllamaHost   string
	OllamaModel  string
	GeminiAPIKey string
	GeminiModel  string

	// Scraper
	AntamURL             string
	ScrapeTimeoutSeconds int
	ScraperAPIURL        string
	ScraperAPIKey        string

	// Scheduler
	ScheduleCron        string
	ScheduleCronEvening string

	// Media
	MediaOutputPath string

	// CTA Slide
	CTATitle    string
	CTAHeadline string
	CTASubtext  string
	CTAHandle   string

	// Cloudflare R2
	R2AccountID    string
	R2AccessKey    string
	R2SecretKey    string
	R2BucketName   string
	R2PublicDomain string

	// Repliz API
	ReplizAccessKey       string
	ReplizSecretKey       string
	ReplizTikTokAccountID string

	// Asynq
	UseAsynq         bool
	AsynqConcurrency int
	AsynqRetryMax    int
}

// Load membaca .env dan return Config
func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("[config] .env not found, reading from environment")
	}

	adminChatID, _ := strconv.ParseInt(getEnv("TELEGRAM_ADMIN_CHAT_ID", "0"), 10, 64)
	groupID, _ := strconv.ParseInt(getEnv("TELEGRAM_GROUP_ID", "0"), 10, 64)
	threadGeneral, _ := strconv.Atoi(getEnv("TELEGRAM_THREAD_GENERAL_ID", "0"))
	threadPost, _ := strconv.Atoi(getEnv("TELEGRAM_THREAD_POST_ID", "0"))
	scrapeTimeout, _ := strconv.Atoi(getEnv("SCRAPE_TIMEOUT_SECONDS", "30"))
	useAsynq, _ := strconv.ParseBool(getEnv("USE_ASYNQ", "false"))
	asynqConcurrency, _ := strconv.Atoi(getEnv("ASYNQ_CONCURRENCY", "10"))
	asynqRetryMax, _ := strconv.Atoi(getEnv("ASYNQ_RETRY_MAX", "3"))

	return &Config{
		MySQLHost:     getEnv("MYSQL_HOST", "mysql"),
		MySQLPort:     getEnv("MYSQL_PORT", "3306"),
		MySQLUser:     getEnv("MYSQL_USER", "dnarmasid"),
		MySQLPassword: getEnv("MYSQL_PASSWORD", "secret"),
		MySQLDatabase: getEnv("MYSQL_DATABASE", "dnarmasid_db"),

		RedisHost: getEnv("REDIS_HOST", "redis"),
		RedisPort: getEnv("REDIS_PORT", "6379"),

		TelegramBotToken:        getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChannelID:       getEnv("TELEGRAM_CHANNEL_ID", ""),
		TelegramAdminChatID:     adminChatID,
		TelegramGroupID:         groupID,
		TelegramThreadGeneralID: threadGeneral,
		TelegramThreadPostID:    threadPost,

		AIProvider:   getEnv("AI_PROVIDER", "ollama"),
		OllamaHost:   getEnv("OLLAMA_HOST", "http://ollama:11434"),
		OllamaModel:  getEnv("OLLAMA_MODEL", "gemma4:31b-cloud"),
		GeminiAPIKey: getEnv("GEMINI_API_KEY", ""),
		GeminiModel:  getEnv("GEMINI_MODEL", "gemini-3.1-flash-lite-preview"),

		AntamURL:             getEnv("ANTAM_URL", "https://www.logammulia.com/id/harga-emas-hari-ini"),
		ScrapeTimeoutSeconds: scrapeTimeout,
		ScraperAPIURL:        getEnv("SCRAPER_API_URL", ""),
		ScraperAPIKey:        getEnv("SCRAPER_API_KEY", ""),

		ScheduleCron:        getEnv("SCHEDULE_CRON", "0 9 * * *"),
		ScheduleCronEvening: getEnv("SCHEDULE_CRON_EVENING", "0 18 * * *"),

		MediaOutputPath: getEnv("MEDIA_OUTPUT_PATH", "/app/volumes/media"),

		CTATitle:    getEnv("CTA_TITLE", "DNARMASID"),
		CTAHeadline: getEnv("CTA_HEADLINE", "INVESTASI EMAS\nMULAI HARI INI"),
		CTASubtext:  getEnv("CTA_SUBTEXT", "Update harga harian, tips & insight emas\nlangsung di tangan Anda."),
		CTAHandle:   getEnv("CTA_HANDLE", "t.me/antamdailybot"),

		R2AccountID:    getEnv("R2_ACCOUNT_ID", ""),
		R2AccessKey:    getEnv("R2_ACCESS_KEY", ""),
		R2SecretKey:    getEnv("R2_SECRET_KEY", ""),
		R2BucketName:   getEnv("R2_BUCKET_NAME", ""),
		R2PublicDomain: getEnv("R2_PUBLIC_DOMAIN", ""),

		ReplizAccessKey:       getEnv("REPLIZ_ACCESS_KEY", ""),
		ReplizSecretKey:       getEnv("REPLIZ_SECRET_KEY", ""),
		ReplizTikTokAccountID: getEnv("REPLIZ_TIKTOK_ACCOUNT_ID", ""),

		UseAsynq:         useAsynq,
		AsynqConcurrency: asynqConcurrency,
		AsynqRetryMax:    asynqRetryMax,
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
