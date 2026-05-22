# Panduan Trigger Manual Event (Redis Queue)

Dokumentasi ini menjelaskan cara melakukan trigger manual untuk setiap tahap dalam pipeline DnarMasID menggunakan `redis-cli`. Sistem ini menggunakan antrian Redis (List) dengan metode `LPUSH`.

## Daftar Queue Key & Payload

### 1. Scraper Job (`job.scrape`)
Digunakan untuk memerintahkan scraper mulai bekerja.
- **Service Penerima:** `scraper-service`
- **Payload:** `map[string]string`
- **Command:**
```bash
docker compose exec redis redis-cli LPUSH job.scrape \
'{
  "mode": "real",
  "triggered_at": "2026-05-01T15:00:00Z"
}'
```
*Gunakan `{"mode": "dummy"}` untuk ngetes tanpa scraping asli.*

---

### 2. Gold Scraped AI (`gold.scraped.ai`)
Digunakan untuk memicu pembuatan caption oleh AI.
- **Service Penerima:** `ai-generator`
- **Payload:** `GoldScrapedEvent`
- **Command:**
```bash
docker compose exec redis redis-cli LPUSH gold.scraped.ai \
'{
  "date": "01 Mei 2026",
  "price_id": 1,
  "trend": "up",
  "change_pct": 1.5,
  "change_amt": 5000,
  "buyback_change_amt": 2000,
  "buyback_trend": "down",
  "prices": [
    {"gram": 1, "buy_price": 1350000, "sell_price": 1240000}
  ],
  "screenshot_price_url": "https://pub-cdbb1bd0f39f43b0a223b3d70e15c94a.r2.dev/hero_price.jpeg",
  "screenshot_buyback_url": "https://pub-cdbb1bd0f39f43b0a223b3d70e15c94a.r2.dev/hero_price.jpeg"
}'
```

---

### 3. Gold Scraped Media (`gold.scraped.media`)
Digunakan untuk memicu pembuatan gambar/infografis.
- **Service Penerima:** `media-generator`
- **Payload:** `GoldScrapedEvent`
- **Command:**
```bash
docker compose exec redis redis-cli LPUSH gold.scraped.media \
'{
  "date": "01 Mei 2026",
  "price_id": 1,
  "trend": "up",
  "change_pct": 1.5,
  "change_amt": 5000,
  "buyback_change_amt": 2000,
  "buyback_trend": "down",
  "prices": [
    {"gram": 1, "buy_price": 1350000, "sell_price": 1240000}
  ],
  "screenshot_price_url": "https://pub-cdbb1bd0f39f43b0a223b3d70e15c94a.r2.dev/hero_price.jpeg",
  "screenshot_buyback_url": "https://pub-cdbb1bd0f39f43b0a223b3d70e15c94a.r2.dev/hero_price.jpeg"
}'
```

---

### 4. Content Ready (`content.ready`)
Digunakan untuk mengirim caption yang sudah jadi ke bot Telegram.
- **Service Penerima:** `telegram-bot`
- **Payload:** `ContentReadyEvent`
- **Command:**
```bash
docker compose exec redis redis-cli LPUSH content.ready \
'{
  "price_id": 1,
  "date": "01 Mei 2026",
  "contents": {
    "general": "Harga emas naik!",
    "instagram": "🚀 Emas Naik!"
  },
  "analysis": "Tren bullish"
}'
```

---

### 5. Media Ready (`media.ready`)
Digunakan untuk mengirim file gambar/video ke bot Telegram.
- **Service Penerima:** `telegram-bot`
- **Payload:** `MediaReadyEvent`
- **Command:**
```bash
docker compose exec redis redis-cli LPUSH media.ready \
'{
  "price_id": 1,
  "date": "01 Mei 2026",
  "media_type": "image",
  "public_url": "https://storage.repliz.com/dummy/info.png",
  "file_name": "info.png"
}'
```

---

### 6. Media Generation Completed (`media.generation.completed`)
Digunakan untuk memicu upload otomatis ke TikTok/Social Media via Repliz.
- **Service Penerima:** `repliz-uploader`
- **Payload:** `MediaGenerationCompletedEvent`
- **Command:**
```bash
docker compose exec redis redis-cli LPUSH media.generation.completed \
'{
  "price_id": 1,
  "date": "01 Mei 2026",
  "caption": "Update Harga Emas Hari Ini!",
  "infographic_url": "https://storage.repliz.com/dummy/info.png"
}'
```

---

### 7. Gold Scraped Bot (`gold.scraped.telegram`)
Digunakan untuk mengirim ringkasan harga langsung ke bot Telegram.
- **Service Penerima:** `telegram-bot`
- **Payload:** `GoldScrapedEvent`
- **Command:**
```bash
docker compose exec redis redis-cli LPUSH gold.scraped.telegram \
'{
  "date": "01 Mei 2026",
  "trend": "up",
  "prices": [
    {"gram": 1, "buy_price": 1350000}
  ]
}'
```

---

### 8. Scrape Failed (`scrape.failed`)
Digunakan untuk mengetes notifikasi error pada bot Telegram.
- **Service Penerima:** `telegram-bot`
- **Payload:** `ScrapeFailedEvent`
- **Command:**
```bash
docker compose exec redis redis-cli LPUSH scrape.failed \
'{
  "date": "2026-05-01",
  "source": "Antam",
  "message": "Connection timeout"
}'
```

---

## Tips Testing

### Cek Antrian
Untuk melihat berapa banyak item yang sedang menunggu di antrian:
```bash
docker compose exec redis redis-cli LLEN gold.scraped.ai
```

### Monitoring Log
Selalu pantau log service saat melakukan trigger manual untuk melihat apakah ada error parsing JSON:
```bash
docker-compose logs -f [nama-service]
```

### Script Otomatis
Tersedia juga script Go untuk trigger massal di folder `scripts/test-push`. Jalankan dengan:
```bash
go run scripts/test-push/main.go
```
