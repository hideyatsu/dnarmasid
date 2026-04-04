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
	TelegramBotToken    string
	TelegramChannelID   string
	TelegramAdminChatID int64

	// AI
	AnthropicAPIKey string
	AnthropicModel  string

	// Scraper
	AntamURL             string
	ScrapeTimeoutSeconds int

	// Scheduler
	ScheduleCron string

	// Media
	MediaOutputPath string
}

// Load membaca .env dan return Config
func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("[config] .env not found, reading from environment")
	}

	adminChatID, _ := strconv.ParseInt(getEnv("TELEGRAM_ADMIN_CHAT_ID", "0"), 10, 64)
	scrapeTimeout, _ := strconv.Atoi(getEnv("SCRAPE_TIMEOUT_SECONDS", "30"))

	return &Config{
		MySQLHost:     getEnv("MYSQL_HOST", "mysql"),
		MySQLPort:     getEnv("MYSQL_PORT", "3306"),
		MySQLUser:     getEnv("MYSQL_USER", "dnarmasid"),
		MySQLPassword: getEnv("MYSQL_PASSWORD", "secret"),
		MySQLDatabase: getEnv("MYSQL_DATABASE", "dnarmasid_db"),

		RedisHost: getEnv("REDIS_HOST", "redis"),
		RedisPort: getEnv("REDIS_PORT", "6379"),

		TelegramBotToken:    getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChannelID:   getEnv("TELEGRAM_CHANNEL_ID", ""),
		TelegramAdminChatID: adminChatID,

		AnthropicAPIKey: getEnv("ANTHROPIC_API_KEY", ""),
		AnthropicModel:  getEnv("ANTHROPIC_MODEL", "claude-sonnet-4-20250514"),

		AntamURL:             getEnv("ANTAM_URL", "https://www.logammulia.com/id/harga-emas-hari-ini"),
		ScrapeTimeoutSeconds: scrapeTimeout,

		ScheduleCron: getEnv("SCHEDULE_CRON", "0 8 * * *"),

		MediaOutputPath: getEnv("MEDIA_OUTPUT_PATH", "/app/volumes/media"),
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
