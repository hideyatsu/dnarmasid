package queue

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
)

// AsynqClient wraps asynq.Client for task enqueueing
type AsynqClient struct {
	client *asynq.Client
}

// AsynqServer wraps asynq.Server for task processing
type AsynqServer struct {
	server *asynq.Server
	mux    *asynq.ServeMux
}

// NewAsynqClient creates a new Asynq client
func NewAsynqClient(redisAddr string) *AsynqClient {
	client := asynq.NewClient(asynq.RedisClientOpt{
		Addr: redisAddr,
	})
	log.Printf("[asynq] Client connected to %s ✅", redisAddr)
	return &AsynqClient{client: client}
}

// Close closes the Asynq client
func (c *AsynqClient) Close() error {
	return c.client.Close()
}

// Enqueue adds a task to the queue
func (c *AsynqClient) Enqueue(taskType string, payload []byte, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	task := asynq.NewTask(taskType, payload)
	return c.client.Enqueue(task, opts...)
}

// EnqueueIn schedules a task to run after a delay
func (c *AsynqClient) EnqueueIn(taskType string, payload []byte, delay time.Duration, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	task := asynq.NewTask(taskType, payload)
	opts = append(opts, asynq.ProcessIn(delay))
	return c.client.Enqueue(task, opts...)
}

// EnqueueAt schedules a task to run at a specific time
func (c *AsynqClient) EnqueueAt(taskType string, payload []byte, t time.Time, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	task := asynq.NewTask(taskType, payload)
	opts = append(opts, asynq.ProcessAt(t))
	return c.client.Enqueue(task, opts...)
}

// NewAsynqServer creates a new Asynq server with configurable options
func NewAsynqServer(redisAddr string) *AsynqServer {
	concurrency := getEnvInt("ASYNQ_CONCURRENCY", 10)
	
	server := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: concurrency,
			Queues: map[string]int{
				"critical": 6, // scrape tasks — highest priority
				"default":  3, // generate tasks
				"low":      1, // upload/notify tasks
			},
			RetryDelayFunc: func(n int, e error, t *asynq.Task) time.Duration {
				// Exponential backoff: 1m, 2m, 4m, 8m, 16m (capped)
				delay := time.Duration(1<<uint(n-1)) * time.Minute
				if delay > 16*time.Minute {
					delay = 16 * time.Minute
				}
				return delay
			},
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
				log.Printf("[asynq] ❌ Task %s failed: %v", task.Type(), err)
			}),
		},
	)

	mux := asynq.NewServeMux()
	log.Printf("[asynq] Server initialized (concurrency=%d) ✅", concurrency)

	return &AsynqServer{server: server, mux: mux}
}

// HandleFunc registers a handler for a task type
func (s *AsynqServer) HandleFunc(taskType string, handler func(context.Context, *asynq.Task) error) {
	s.mux.HandleFunc(taskType, handler)
	log.Printf("[asynq] Registered handler for %s", taskType)
}

// Run starts the Asynq server (blocking)
func (s *AsynqServer) Run() error {
	log.Println("[asynq] Server starting...")
	return s.server.Run(s.mux)
}

// Shutdown gracefully stops the server
func (s *AsynqServer) Shutdown() {
	s.server.Shutdown()
	log.Println("[asynq] Server stopped")
}

// AsynqScheduler wraps asynq.Scheduler for cron jobs
type AsynqScheduler struct {
	scheduler *asynq.Scheduler
}

// NewAsynqScheduler creates a new Asynq scheduler
func NewAsynqScheduler(redisAddr string) *AsynqScheduler {
	loc := time.FixedZone("WIB", 7*60*60)
	scheduler := asynq.NewScheduler(
		asynq.RedisClientOpt{Addr: redisAddr},
		&asynq.SchedulerOpts{
			Location: loc,
		},
	)
	log.Println("[asynq] Scheduler initialized (TZ=WIB) ✅")
	return &AsynqScheduler{scheduler: scheduler}
}

// Register adds a cron job
func (s *AsynqScheduler) Register(cronspec string, taskType string, payload []byte, opts ...asynq.Option) (string, error) {
	task := asynq.NewTask(taskType, payload)
	entryID, err := s.scheduler.Register(cronspec, task, opts...)
	if err != nil {
		return "", fmt.Errorf("register cron job: %w", err)
	}
	log.Printf("[asynq] Registered cron job: %s → %s", cronspec, taskType)
	return entryID, nil
}

// Run starts the scheduler (blocking)
func (s *AsynqScheduler) Run() error {
	log.Println("[asynq] Scheduler starting...")
	return s.scheduler.Run()
}

// Shutdown stops the scheduler
func (s *AsynqScheduler) Shutdown() {
	s.scheduler.Shutdown()
}

// Helper
func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return fallback
}


