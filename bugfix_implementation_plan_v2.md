# 🛠️ Fix Implementation Plan v2 — Scraper Refinements

## Context

Scraper chrome zombie fix sudah applied (commit `daf06ab`). Review kedua menemukan 4 remaining issues yang perlu di-address.

---

## 🔴 Fix #1 — `atomic.LoadInt64` di Log.Printf (BUG)

**File:** `services/scraper/main.go`

```go
// ❌ Salah — pass pointer, bukan value
log.Printf("...jobs=%d failed=%d", &jobsReceived, &jobsFailed)

// ✅ Benar
log.Printf("...jobs=%d failed=%d", atomic.LoadInt64(&jobsReceived), atomic.LoadInt64(&jobsFailed))
```

**Lokasi:** Baris sekitar `pollTicker.C` case.

---

## 🟡 Fix #2 — Global Zombie Reaper CPU Waste

**File:** `services/scraper/main.go`

```go
// ❌ Current — ECHILD branch tidak return, masih loop 2s
if err == syscall.ECHILD {
    time.Sleep(10 * time.Second)
    continue
}
```

**Fix:**
```go
if err == syscall.ECHILD {
    // Parent process sudah mereap semua child sendiri (via defer CleanupOne)
    // Zombie reaper tidak diperlukan lagi, exit goroutine
    log.Println("[scraper] 🛡️ Zombie Reaper: no orphans, exiting reaper")
    return
}
```

**Alternative** (jika ingin reaper tetap jalan untuk safety):
```go
if err == syscall.ECHILD {
    time.Sleep(30 * time.Second)
    continue
}
```

---

## 🟡 Fix #3 — Stall Detection (Aktifkan)

**File:** `services/scraper/main.go`

Health endpoint & heartbeat — stall detection masih dikomentarin.

**Fix — di health handler:**
```go
if !lastJob.IsZero() && time.Since(lastJob) > 30*time.Minute {
    status = "degraded"
}

w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(map[string]any{
    "status":           status,
    "chrome_instances": chromeManager.Count(),
    "jobs_received":    atomic.LoadInt64(&jobsReceived),
    "jobs_failed":      atomic.LoadInt64(&jobsFailed),
    "last_job_at":      lastJob.Format(time.RFC3339),
    "uptime_seconds":   time.Since(startTime).Seconds(),
})
```

**Fix — di health response JSON, tambah field:**
```go
"stalled_minutes": func() int64 {
    if lastJob.IsZero() { return 0 }
    return int64(time.Since(lastJob).Minutes())
}(),
```

---

## 🟡 Fix #4 — Poll Heartbeat Interval (10-15 min)

**File:** `services/scraper/main.go`

```go
// ❌ Current — 1 jam terlalu lama untuk detect stall
pollTicker := time.NewTicker(1 * time.Hour)

// ✅ Recommended — 15 menit
pollTicker := time.NewTicker(15 * time.Minute)
```

---

## Execution Order

```
Step 1 → Fix #1  (atomic.LoadInt64 — BUG, critical)
Step 2 → Fix #2  (ECHILD exit — CPU efficiency)
Step 3 → Fix #3  (stall detection aktifkan)
Step 4 → Fix #4  (poll interval 15 min)
Step 5 → docker build + compose pull → restart
Step 6 → Verify: zombie = 0, health endpoint valid
```

---

## Commit Message

```
fix: improve scraper observability and zombie reaper efficiency

- use atomic.LoadInt64 for job counters in log output
- exit zombie reaper goroutine when ECHILD (parent reaps children)
- activate stall detection in /health response
- reduce poll heartbeat from 1h to 15 minutes
```

---

## Validation

- [ ] `log.Printf` output tidak panic/crash saat print counters
- [ ] `/health` → `status: "degraded"` when stalled >30 min
- [ ] `/health` → `stalled_minutes` field present
- [ ] Heartbeat log setiap 15 menit
- [ ] Zombie count = 0 after 2 job runs
