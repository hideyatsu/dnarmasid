package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"dnarmasid/shared/config"

	"github.com/redis/go-redis/v9"
)

// Queue keys — kontrak komunikasi antar service
// ⚠️ JANGAN ubah nama key ini tanpa update semua service terkait
const (
	KeyJobScrape          = "job.scrape"              // scheduler → scraper
	KeyGoldScrapedAI      = "gold.scraped.ai"         // scraper → ai-generator
	KeyGoldScrapedMedia   = "gold.scraped.media"      // scraper → media-generator
	KeyGoldScrapedBot     = "gold.scraped.telegram"   // scraper → telegram-bot
	KeyContentReady       = "content.ready"           // ai-generator → telegram-bot
	KeyMediaReady         = "media.ready"             // media-generator → telegram-bot
	KeyScrapeFailed       = "scrape.failed"           // scraper → telegram-bot
)

type Client struct {
	rdb *redis.Client
	ctx context.Context
}

// NewClient membuka koneksi ke Redis
func NewClient(cfg *config.Config) *Client {
	addr := fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort)

	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "",
		DB:       0,
	})

	ctx := context.Background()

	// Retry sampai Redis siap
	for i := 0; i < 10; i++ {
		_, err := rdb.Ping(ctx).Result()
		if err == nil {
			break
		}
		log.Printf("[queue] Redis not ready, retrying (%d/10)...", i+1)
		time.Sleep(2 * time.Second)
	}

	log.Println("[queue] Redis connected ✅")
	return &Client{rdb: rdb, ctx: ctx}
}

// Publish mengirim pesan ke Redis list (LPUSH)
func (c *Client) Publish(key string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}
	return c.rdb.LPush(c.ctx, key, data).Err()
}

// Consume menunggu & membaca pesan dari Redis list (BRPOP — blocking)
func (c *Client) Consume(key string, timeout time.Duration) ([]byte, error) {
	result, err := c.rdb.BRPop(c.ctx, timeout, key).Result()
	if err != nil {
		return nil, err
	}
	// result[0] = key, result[1] = value
	return []byte(result[1]), nil
}

// ConsumeJSON consume + unmarshal langsung ke struct
func (c *Client) ConsumeJSON(key string, timeout time.Duration, dest any) error {
	data, err := c.Consume(key, timeout)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}
