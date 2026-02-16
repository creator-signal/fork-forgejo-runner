package firecracker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

// Test constants for meaningful values
const (
	testTotalMemoryMB     = 16000
	testAvailableMemoryMB = 12000
	testDefaultMaxCommit  = 4000
	testDefaultTimeout    = time.Second
)

// mockMemorySystem provides controllable memory values for testing.
// All fields are protected by mu for safe concurrent access.
type mockMemorySystem struct {
	mu          sync.RWMutex
	totalMB     int64
	availableMB int64
	err         error
}

func (m *mockMemorySystem) GetTotalMemoryMB() (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalMB, m.err
}

func (m *mockMemorySystem) GetAvailableMemoryMB() (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.availableMB, m.err
}

func (m *mockMemorySystem) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// testSchedulerConfig holds configuration for creating test schedulers.
type testSchedulerConfig struct {
	totalMB        int64
	availableMB    int64
	maxCommitMB    int64
	reserveMB      int64
	acquireTimeout time.Duration
}

// defaultTestConfig returns a standard test configuration.
func defaultTestConfig() testSchedulerConfig {
	return testSchedulerConfig{
		totalMB:        testTotalMemoryMB,
		availableMB:    testAvailableMemoryMB,
		maxCommitMB:    testDefaultMaxCommit,
		reserveMB:      0,
		acquireTimeout: testDefaultTimeout,
	}
}

// newTestScheduler creates a scheduler with the given config for testing.
func newTestScheduler(t *testing.T, cfg testSchedulerConfig) (*MemoryScheduler, *mockMemorySystem) {
	t.Helper()
	sys := &mockMemorySystem{totalMB: cfg.totalMB, availableMB: cfg.availableMB}
	logger := log.New()
	logger.SetLevel(log.DebugLevel)
	sched, err := NewMemorySchedulerWithSystem(MemorySchedulerConfig{
		MaxCommitMB:    cfg.maxCommitMB,
		ReserveMB:      cfg.reserveMB,
		AcquireTimeout: cfg.acquireTimeout,
	}, sys, logger)
	if err != nil {
		t.Fatalf("NewMemoryScheduler: %v", err)
	}
	return sched, sys
}

// mustAcquire is a helper that fails the test if Acquire fails.
func mustAcquire(ctx context.Context, t *testing.T, sched *MemoryScheduler, mb int64) {
	t.Helper()
	if err := sched.Acquire(ctx, mb); err != nil {
		t.Fatalf("Acquire %dMB: %v", mb, err)
	}
}

// assertCommitted verifies the committed memory matches expected.
func assertCommitted(t *testing.T, sched *MemoryScheduler, expectedMB int64) {
	t.Helper()
	stats := sched.Stats()
	if stats.CommittedMB != expectedMB {
		t.Errorf("committed = %d, want %d", stats.CommittedMB, expectedMB)
	}
}

func TestMemoryScheduler_Acquire(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.maxCommitMB = 8000
	cfg.reserveMB = 1000
	sched, _ := newTestScheduler(t, cfg)
	ctx := context.Background()

	mustAcquire(ctx, t, sched, 2000)
	assertCommitted(t, sched, 2000)

	mustAcquire(ctx, t, sched, 3000)
	assertCommitted(t, sched, 5000)

	sched.Release(2000)
	assertCommitted(t, sched, 3000)
}

func TestMemoryScheduler_Acquire_InvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		requestMB int64
		wantErr   error
	}{
		{"negative memory", -1000, ErrInvalidMemoryRequest},
		{"zero memory", 0, ErrInvalidMemoryRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched, _ := newTestScheduler(t, defaultTestConfig())
			ctx := context.Background()

			err := sched.Acquire(ctx, tt.requestMB)
			if err == nil {
				t.Errorf("Acquire(%d) = nil, want error", tt.requestMB)
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Acquire(%d) error = %v, want %v", tt.requestMB, err, tt.wantErr)
			}
		})
	}
}

func TestMemoryScheduler_Timeout(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.maxCommitMB = 4000
	cfg.acquireTimeout = 100 * time.Millisecond
	sched, _ := newTestScheduler(t, cfg)
	ctx := context.Background()

	mustAcquire(ctx, t, sched, 4000)

	err := sched.Acquire(ctx, 1000)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
	if !errors.Is(err, ErrAcquireTimeout) {
		t.Errorf("error = %v, want ErrAcquireTimeout", err)
	}
}

func TestMemoryScheduler_RequestExceedsMax(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.maxCommitMB = 4000
	sched, _ := newTestScheduler(t, cfg)

	ctx := context.Background()
	err := sched.Acquire(ctx, 5000)
	if err == nil {
		t.Error("expected error for request exceeding max, got nil")
	}
	if !errors.Is(err, ErrInvalidMemoryRequest) {
		t.Errorf("error = %v, want ErrInvalidMemoryRequest", err)
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("error message should mention exceeds maximum: %v", err)
	}
}

func TestMemoryScheduler_AutoDetect(t *testing.T) {
	const totalMB int64 = 10000
	const expectedMax int64 = 8000 // 80% of total

	sys := &mockMemorySystem{totalMB: totalMB, availableMB: 8000}
	logger := log.New()
	sched, err := NewMemorySchedulerWithSystem(MemorySchedulerConfig{
		MaxCommitMB:    0, // trigger auto-detect
		ReserveMB:      0,
		AcquireTimeout: testDefaultTimeout,
	}, sys, logger)
	if err != nil {
		t.Fatalf("NewMemoryScheduler: %v", err)
	}

	if sched.MaxCommitMB() != expectedMax {
		t.Errorf("auto-detect max = %d, want %d (80%% of %d)", sched.MaxCommitMB(), expectedMax, totalMB)
	}
}

func TestMemoryScheduler_AutoDetectFails(t *testing.T) {
	sys := &mockMemorySystem{
		totalMB: 0,
		err:     errors.New("simulated memory detection error"),
	}
	logger := log.New()
	_, err := NewMemorySchedulerWithSystem(MemorySchedulerConfig{
		MaxCommitMB:    0,
		ReserveMB:      0,
		AcquireTimeout: testDefaultTimeout,
	}, sys, logger)
	if err == nil {
		t.Error("expected error when auto-detect fails, got nil")
	}
}

func TestMemoryScheduler_NegativeConfigValues(t *testing.T) {
	sys := &mockMemorySystem{totalMB: testTotalMemoryMB, availableMB: testAvailableMemoryMB}
	logger := log.New()

	tests := []struct {
		name string
		cfg  MemorySchedulerConfig
	}{
		{"negative MaxCommitMB", MemorySchedulerConfig{MaxCommitMB: -1}},
		{"negative ReserveMB", MemorySchedulerConfig{ReserveMB: -1}},
		{"negative AcquireTimeout", MemorySchedulerConfig{AcquireTimeout: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewMemorySchedulerWithSystem(tt.cfg, sys, logger)
			if err == nil {
				t.Error("expected error for negative config value, got nil")
			}
			if !strings.Contains(err.Error(), "non-negative") {
				t.Errorf("error message should mention non-negative: %v", err)
			}
		})
	}
}

func TestMemoryScheduler_Concurrent(t *testing.T) {
	const numGoroutines = 4
	const perRequestMB int64 = 1000

	cfg := defaultTestConfig()
	cfg.maxCommitMB = numGoroutines * perRequestMB
	cfg.acquireTimeout = 5 * time.Second
	sched, _ := newTestScheduler(t, cfg)

	ctx := context.Background()
	errs := make(chan error, numGoroutines)
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sched.Acquire(ctx, perRequestMB); err != nil {
				errs <- err
				return
			}
			time.Sleep(50 * time.Millisecond)
			sched.Release(perRequestMB)
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent Acquire: %v", err)
	}
	assertCommitted(t, sched, 0)
}

func TestMemoryScheduler_ReleaseUnblocks(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.maxCommitMB = 2000
	cfg.acquireTimeout = 5 * time.Second
	sched, _ := newTestScheduler(t, cfg)
	ctx := context.Background()

	mustAcquire(ctx, t, sched, 2000)

	done := make(chan error, 1)
	go func() {
		done <- sched.Acquire(ctx, 1000)
	}()

	time.Sleep(50 * time.Millisecond)
	sched.Release(1000)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("blocked Acquire failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Release did not unblock waiting Acquire")
	}
}

func TestMemoryScheduler_Release_InvalidAmounts(t *testing.T) {
	tests := []struct {
		name      string
		releaseMB int64
	}{
		{"zero", 0},
		{"negative", -1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched, _ := newTestScheduler(t, defaultTestConfig())
			ctx := context.Background()

			mustAcquire(ctx, t, sched, 2000)
			sched.Release(tt.releaseMB)
			assertCommitted(t, sched, 2000)
		})
	}
}

func TestMemoryScheduler_ContextCancellation(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.maxCommitMB = 2000
	cfg.acquireTimeout = 0 // No internal timeout
	sched, _ := newTestScheduler(t, cfg)

	ctx := context.Background()
	mustAcquire(ctx, t, sched, 2000)

	cancelCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- sched.Acquire(cancelCtx, 1000)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected context cancelled error, got nil")
		}
	case <-time.After(time.Second):
		t.Error("Acquire did not return after context cancellation")
	}
}

func TestMemoryScheduler_TimeoutErrorMessage(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.maxCommitMB = 1000
	cfg.acquireTimeout = 50 * time.Millisecond
	sched, _ := newTestScheduler(t, cfg)

	ctx := context.Background()
	mustAcquire(ctx, t, sched, 1000)

	err := sched.Acquire(ctx, 500)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "500MB") {
		t.Errorf("error should mention requested amount: %v", err)
	}
	if !errors.Is(err, ErrAcquireTimeout) {
		t.Errorf("error should be ErrAcquireTimeout: %v", err)
	}
}

func TestMemoryScheduler_Stats_TracksWaitingJobs(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.maxCommitMB = 1000
	cfg.acquireTimeout = 5 * time.Second
	sched, _ := newTestScheduler(t, cfg)

	ctx := context.Background()
	mustAcquire(ctx, t, sched, 1000)

	started := make(chan struct{})
	go func() {
		close(started)
		_ = sched.Acquire(ctx, 500)
	}()

	<-started
	time.Sleep(50 * time.Millisecond)

	stats := sched.Stats()
	if stats.WaitingJobs != 1 {
		t.Errorf("WaitingJobs = %d, want 1", stats.WaitingJobs)
	}

	sched.Release(500)
	time.Sleep(50 * time.Millisecond)

	stats = sched.Stats()
	if stats.WaitingJobs != 0 {
		t.Errorf("WaitingJobs after unblock = %d, want 0", stats.WaitingJobs)
	}
}

func TestMemorySchedulerStats_String(t *testing.T) {
	stats := MemorySchedulerStats{
		CommittedMB: 2000,
		MaxCommitMB: 8000,
		WaitingJobs: 3,
	}

	s := stats.String()
	if !strings.Contains(s, "2000") || !strings.Contains(s, "8000") || !strings.Contains(s, "3") {
		t.Errorf("Stats.String() = %q, want to contain committed, max, and waiting values", s)
	}
}
