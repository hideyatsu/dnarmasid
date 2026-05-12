package chrome

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"time"
)

// Manager handles Chrome process lifecycle to prevent zombie processes and FD leaks.
type Manager struct {
	instances []*exec.Cmd
	mu        sync.Mutex
}

// NewManager creates a new Chrome manager.
func NewManager() *Manager {
	return &Manager{
		instances: make([]*exec.Cmd, 0),
	}
}

// Spawn starts a new Chrome process with a specific debugging port and tracks it.
func (m *Manager) Spawn(ctx context.Context, chromePath string, debugPort int, args ...string) (*exec.Cmd, error) {
	fullArgs := append([]string{
		"--headless",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-gpu",
		"--disable-software-rasterizer",
		"--disable-extensions",
		"--disable-background-networking",
		"--disable-sync",
		"--disable-translate",
		"--no-first-run",
		"--disable-setuid-sandbox",
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		fmt.Sprintf("--remote-debugging-port=%d", debugPort),
	}, args...)

	cmd := exec.CommandContext(ctx, chromePath, fullArgs...)

	m.mu.Lock()
	m.instances = append(m.instances, cmd)
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		m.Remove(cmd)
		return nil, err
	}

	// Wait for port to be ready (timeout 5s)
	if err := m.WaitPort(debugPort, 5*time.Second); err != nil {
		m.CleanupOne(cmd)
		return nil, fmt.Errorf("chrome port %d not ready: %w", debugPort, err)
	}

	return cmd, nil
}

// WaitPort waits for a port to be ready.
func (m *Manager) WaitPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for port %d", port)
}

// GetFreePort finds an available TCP port.
func (m *Manager) GetFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Cleanup kills and reaps all tracked Chrome processes.
func (m *Manager) Cleanup() {
	m.mu.Lock()
	instances := make([]*exec.Cmd, len(m.instances))
	copy(instances, m.instances)
	m.mu.Unlock()

	for _, cmd := range instances {
		m.CleanupOne(cmd)
	}
}

// CleanupOne kills and reaps a single Chrome process.
func (m *Manager) CleanupOne(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		m.Remove(cmd)
		return
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait() // Reaping the zombie

	m.Remove(cmd)
}

// Remove removes a command from the tracking list.
func (m *Manager) Remove(cmd *exec.Cmd) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, c := range m.instances {
		if c == cmd {
			m.instances = append(m.instances[:i], m.instances[i+1:]...)
			break
		}
	}
}

// Count returns the number of currently tracked Chrome processes.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.instances)
}
