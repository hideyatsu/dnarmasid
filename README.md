# DnarMasID — Gold Price Automation Platform

> Update harga emas Antam otomatis setiap hari, generate konten AI untuk semua platform sosmed, dan kirim ke Telegram bot untuk di-posting manual.

---

## 🏗️ Arsitektur

```
Scheduler → Scraper → AI Generator ──→ Telegram Bot (Admin)
                    → Media Generator ──→ (gambar + video + semua caption)
```

Semua komunikasi antar service lewat **Redis Queue** (async, loose coupling).

---

## 📦 Services

| Service | Container | Fungsi |
|---|---|---|
| `scheduler` | dnarmasid-scheduler | Trigger pipeline setiap hari jam 08.00 WIB |
| `scraper` | dnarmasid-scraper | Scrape harga emas dari logammulia.com |
| `ai-generator` | dnarmasid-ai-generator | Generate caption 6 platform via Anthropic API |
| `media-generator` | dnarmasid-media-generator | Buat infografis (PNG) & video placeholder |
| `telegram-bot` | dnarmasid-telegram-bot | Kirim hasil ke admin + handle subscriber |
| `mysql` | dnarmasid-mysql | Database utama |
| `redis` | dnarmasid-redis | Message queue antar service |

---

## 🚀 Quick Start

### 1. Clone & setup environment
```bash
git clone <repo>
cd dnarmasid
cp .env.example .env
# Edit .env dengan credentials kamu
```

### 2. Isi .env
```env
TELEGRAM_BOT_TOKEN=        # Dari @BotFather
TELEGRAM_ADMIN_CHAT_ID=    # Chat ID kamu (cek via @userinfobot)
ANTHROPIC_API_KEY=         # Dari console.anthropic.com
```

### 3. Jalankan
```bash
docker compose up --build
```

### 4. Cek semua container running
```bash
docker compose ps
```

---

## 🔄 Pipeline Flow

```
08:00 WIB — Scheduler publish → job.scrape
              ↓
           Scraper scrape antam → simpan MySQL → publish gold.scraped
              ↓                              ↓
        AI Generator                  Media Generator
        generate 6 caption            buat infografis PNG
        + analisis harga              + video (TODO)
              ↓                              ↓
        publish content.ready         publish media.ready
              ↓                              ↓
                    Telegram Bot
                    kirim ke admin:
                    • Gambar infografis
                    • Video (jika ada)
                    • Analisis harga
                    • Caption Instagram
                    • Caption Facebook
                    • Caption Threads
                    • Thread Twitter/X
                    • Deskripsi YouTube
                    • Caption TikTok
```

---

## 📁 Struktur Project

```
dnarmasid/
├── docker-compose.yml
├── .env.example
├── go.mod
├── migrations/
│   └── init.sql
├── shared/                    ← Internal packages (bukan service)
│   ├── config/config.go
│   ├── db/mysql.go
│   ├── queue/redis.go
│   └── models/models.go
└── services/
    ├── scheduler/
    ├── scraper/
    ├── ai-generator/
    ├── media-generator/
    └── telegram-bot/
```

---

## 🤖 Telegram Bot Commands

| Command | Fungsi |
|---|---|
| `/start` | Mulai bot |
| `/subscribe` | Berlangganan update harian |
| `/unsubscribe` | Berhenti berlangganan |
| `/status` | Cek status langganan |
| `/help` | Bantuan |

---

## ⚙️ Konfigurasi Jadwal

Edit `SCHEDULE_CRON` di `.env`. Format: cron expression (WIB timezone).

```
# Setiap hari jam 08:00 WIB (default)
SCHEDULE_CRON=0 8 * * *

# Setiap hari jam 09:30 WIB
SCHEDULE_CRON=30 9 * * *
```

---

## 🛠️ Development

### Jalankan satu service saja
```bash
docker compose up mysql redis       # infrastruktur dulu
docker compose up scraper           # test scraper
docker compose logs -f ai-generator # lihat log
```

### Trigger scrape manual (tanpa tunggu scheduler)
```bash
# Publish manual ke Redis
docker compose exec redis redis-cli LPUSH job.scrape '{"triggered_at":"manual","source":"dev"}'
```

### Lihat data MySQL
```bash
docker compose exec mysql mysql -u dnarmasid -psecret dnarmasid_db
```

---

## 📋 TODO / Roadmap

- [ ] **Phase 2** — Implementasi video generation (FFmpeg)
- [ ] **Phase 2** — Broadcast ke subscriber aktif
- [ ] **Phase 3** — Dashboard monitoring (web UI)
- [ ] **Phase 3** — Alert jika harga naik/turun signifikan (>2%)
- [ ] **Phase 4** — Auto-retry jika scrape/AI gagal

---

## ⚠️ Catatan Penting

- **Scraper selector** di `services/scraper/antam.go` perlu disesuaikan dengan struktur HTML terbaru logammulia.com
- **Twitter/X API** berbayar ($100/bulan) — caption tetap di-generate, post manual
- **Video generation** masih placeholder — perlu implementasi FFmpeg
- Harga dev fallback tersedia jika scrape gagal (untuk testing)

---

*Built with ❤️ by DnarMasID | Golang + Docker + Anthropic AI*
