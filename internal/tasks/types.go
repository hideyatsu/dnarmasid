package tasks

// Task type constants — kontrak antar service
// ⚠️ JANGAN ubah nama tanpa update semua handler
const (
	TypeScrape         = "scrape"
	TypeGenerateAI     = "generate:ai"
	TypeGenerateMedia  = "generate:media"
	TypeNotifyTelegram = "notify:telegram"
	TypeUpload         = "upload:repliz"
)

// Queue priorities
const (
	QueueCritical = "critical" // scrape (must succeed)
	QueueDefault  = "default"  // ai/media generation
	QueueLow      = "low"      // upload, notify
)
