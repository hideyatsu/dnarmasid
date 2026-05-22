# 🐛 Bugfix Implementation Plan — Scraper's Chrome Zombie & FD Leak

## Context

Scraper service menghasilkan ~76 zombie `[chrome]` processes sejak 8 Mei 2026, terakumulasi dari setiap job run yang tidak di-reap dengan benar oleh parent. Pattern menyebabkan:
- FD leak per job (socket ke Chrome debugging port)
- Missed evening jobs (11 & 12 Mei) tanpa error log
- `BRPOP` loop tanpa visibility ke state hang/slow

---

## Issue Breakdown

### 🔴 Issue #1 — Chrome Zombie Processes (Root Cause)
**File:** `services/scraper/chrome/*` atau `cmd/chrome.go`

Chrome di-spawn sebagai child process, tapi parent tidak call `wait()` setelah child exit. Setiap run 8 Chrome instances → 8 zombies.

**Fix:**
```
1. Spawn Chrome via exec.Command (sudah ada)
2. Store *os.Process untuk setiap Chrome instance
3. On job completion → process.Wait() untuk SEMUA Chrome instances
4. On timeout/error → process.Kill() + process.Wait()
5. Tambah defer di Run() untuk guarantee cleanup
```

### 🔴 Issue #2 — FD Leak
**File:** `services/scraper/chrome/*`

Setiap Chrome spawn membuka socket ke debugging port, tapi tidak di-close setelah job selesai.

**Fix:**
```
1. Chrome instances disimpan di slice/struct
2. Cleanup semua FD di end of Run(): chrome.Process.Kill(); chrome.Wait()
3. Tambah defer di top-level Run() sebagai safety net
```

### 🟡 Issue #3 — BRPOP Loop Visibility
**File:** `services/scraper/main.go`

`BRPOP` timeout 5 detik, tapi kalau call itu sendiri stuck (bukan timeout), tidak ada log.

**Fix:**
```
1. Tambah periodic heartbeat log setiap N iteration: "still polling..."
2. Track last successful consume timestamp
3. If delta > threshold (e.g., 10 menit) → log warning: "consumer stalled"
4. Tambah metrics counter: jobs_received, jobs_failed, consume_stalls
```

### 🟡 Issue #4 — Docker Log Buffer
**File:** `docker-compose.yml`

Docker default ring buffer ~10MB. Heavy debug output (R2 upload, SQL) overwrite log lama.

**Fix:**
```
1. Di compose file, tambah:
   logging:
     driver: "json-file"
     options:
       max-size: "50m"
       max-file: "5"
       compress: "true"
2. Atau switch ke loki/fluentd untuk centralized log
```

### 🟡 Issue #5 — No Health Endpoint
**File:** `services/scraper/main.go`

Scraper tidak expose health endpoint, jadi monitoring tidak punya visibility.

**Fix:**
```
1. Tambah HTTP server di port berbeda (e.g., :9090):
   GET /health → {"status":"ok","zombies":0,"jobs_today":N,"last_scrape":"..."}
   GET /health/chrome → {"chrome_instances":N,"zombies":0}
2. Docker healthcheck → curl localhost:9090/health
3. Expose port di compose
```

---

## Proposed Code Structure

### services/scraper/chrome/manager.go (NEW)

```go
package chrome

type Manager struct {
    instances []*exec.Cmd
    mu        sync.Mutex
}

func (m *Manager) Spawn(ctx context.Context, debugPort int) (*exec.Cmd, error) {
    cmd := exec.CommandContext(ctx, "chrome",
        "--headless",
        fmt.Sprintf("--remote-debugging-port=%d", debugPort),
        ...
    )
    m.mu.Lock()
    m.instances = append(m.instances, cmd)
    m.mu.Unlock()
    return cmd, nil
}

func (m *Manager) Cleanup() {
    m.mu.Lock()
    defer m.mu.Unlock()
    for _, cmd := range m.instances {
        if cmd.Process == nil {
            continue
        }
        cmd.Process.Kill()
        cmd.Wait() // reap immediately
    }
    m.instances = nil
}

// CleanupOne reaps a single finished Chrome
func (m *Manager) CleanupOne(cmd *exec.Cmd) {
    m.mu.Lock()
    defer m.mu.Unlock()
    for i, c := range m.instances {
        if c == cmd {
            if c.Process != nil {
                c.Process.Kill()
                c.Wait()
            }
            m.instances = append(m.instances[:i], m.instances[i+1:]...)
            break
        }
    }
}
```

### services/scraper/main.go (PATCH)

```go
// Di main():
// Ganti loop existing dengan:

var (
    jobsReceived counter
    jobsFailed   counter
    stalls       counter
    lastJobTime  time.Time
)

pollTicker := time.NewTicker(30 * time.Second)
defer pollTicker.Stop()

for {
    select {
    case <-quit:
        log.Println("[scraper] Shutting down...")
        chromeManager.Cleanup()
        return
    case <-pollTicker.C:
        if !lastJobTime.IsZero() && time.Since(lastJobTime) > 10*time.Minute {
            log.Printf("[scraper] ⚠️ Stalled: no job in %v", time.Since(lastJobTime))
            stalls.Inc()
        }
        log.Printf("[scraper] still polling... (jobs=%d failed=%d stalls=%d)",
            jobsReceived.Value(), jobsFailed.Value(), stalls.Value())
    default:
        // existing BRPOP logic...
        // di defer atau finally:
        chromeManager.Cleanup()
    }
}
```

### docker-compose.yml (PATCH)

```yaml
scraper:
  logging:
    driver: "json-file"
    options:
      max-size: "50m"
      max-file: "5"
```

### health endpoint (NEW — optional, low priority)

```go
// di main() goroutine:
http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
    json.NewEncoder(w).Encode(map[string]any{
        "status":       "ok",
        "chrome_count": chromeManager.Count(),
        "jobs_today":   jobsReceived.Value(),
        "last_scrape":  lastJobTime.Format(time.RFC3339),
    })
})
http.ListenAndServe(":9090", nil)
```

---

## Execution Order

```
Phase 1 (Safety):        Docker log buffer + health endpoint
Phase 2 (Core Fix):      Chrome Manager — defer Cleanup() di Run()
Phase 3 (Resilience):    BRPOP heartbeat + stall detection
Phase 4 (Validation):    Smoke test + zombie count verification
```

---

## Validation Checklist

- [ ] `ps aux | grep defunct | wc -l` → 0 setelah 3 job runs
- [ ] Evening schedule 12 Mei 18:00 → scraper log: "Job received" ✅
- [ ] Telegram: tidak ada `scrape.failed` untuk evening runs
- [ ] `/health` endpoint returns valid JSON
- [ ] Docker logs tidak ter-truncate setelah 1 minggu
