package firecracker

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
)

// Memory scheduling errors for caller inspection.
var (
	// ErrInvalidMemoryRequest indicates the requested memory amount was invalid (zero or negative).
	ErrInvalidMemoryRequest = errors.New("invalid memory request")

	// ErrInsufficientMemory indicates not enough physical memory is available.
	ErrInsufficientMemory = errors.New("insufficient memory")

	// ErrAcquireTimeout indicates the acquire operation timed out waiting for capacity.
	ErrAcquireTimeout = errors.New("memory acquire timeout")
)

// defaultMemoryCommitRatio is the fraction of total memory used when auto-detecting MaxCommitMB.
const defaultMemoryCommitRatio = 0.80

// MemorySystem abstracts system memory queries for testability.
type MemorySystem interface {
	GetAvailableMemoryMB() (int64, error)
	GetTotalMemoryMB() (int64, error)
}

// Scheduler defines the interface for memory scheduling.
// This interface enables mocking in tests.
type Scheduler interface {
	Acquire(ctx context.Context, memoryMB int64) error
	Release(memoryMB int64)
	Available() int64
	MinJobMemoryMB() int64
}

// MemorySchedulerConfig configures a MemoryScheduler.
type MemorySchedulerConfig struct {
	// MaxCommitMB is the maximum memory that can be committed to VMs.
	// If 0, auto-detects as 80% of total system memory.
	MaxCommitMB int64

	// ReserveMB is memory to keep reserved for the host OS.
	// Acquire fails if available memory minus this reserve is insufficient.
	ReserveMB int64

	// AcquireTimeout is the maximum time to wait for memory to become available.
	// If 0, waits indefinitely (or until context is cancelled).
	AcquireTimeout time.Duration

	// MinJobMemoryMB is the minimum memory any job might need.
	// Used to calculate memory-based capacity before claiming jobs.
	// If 0, memory-aware job claiming is disabled.
	MinJobMemoryMB int64
}

// MemorySchedulerStats provides observability into scheduler state.
// Note: Values are read independently and may not represent a consistent snapshot
// if the scheduler is actively processing requests.
type MemorySchedulerStats struct {
	CommittedMB int64
	MaxCommitMB int64
	WaitingJobs int64
}

// String returns a human-readable representation of the stats.
func (s MemorySchedulerStats) String() string {
	return fmt.Sprintf("committed=%dMB/%dMB, waiting=%d", s.CommittedMB, s.MaxCommitMB, s.WaitingJobs)
}

// MemoryScheduler manages memory allocation for VMs.
// It prevents over-commitment by tracking committed memory
// and validating against actual available memory.
//
// All methods on MemoryScheduler are safe for concurrent use.
type MemoryScheduler struct {
	committed   *semaphore.Weighted
	maxCommitMB int64
	reserveMB   int64
	timeout     time.Duration

	committedMB    atomic.Int64
	waitingJobs    atomic.Int64
	minJobMemoryMB int64

	sys    MemorySystem
	logger log.FieldLogger
}

// NewMemoryScheduler creates a scheduler with the given configuration.
// If MaxCommitMB is 0, it auto-detects 80% of total system memory.
func NewMemoryScheduler(cfg MemorySchedulerConfig) (*MemoryScheduler, error) {
	return NewMemorySchedulerWithSystem(cfg, &realMemorySystem{}, log.StandardLogger())
}

// NewMemorySchedulerWithLogger creates a scheduler with a custom logger.
// Use this to provide structured logging fields (e.g., WithField("module", "memory_scheduler")).
func NewMemorySchedulerWithLogger(cfg MemorySchedulerConfig, logger log.FieldLogger) (*MemoryScheduler, error) {
	return NewMemorySchedulerWithSystem(cfg, &realMemorySystem{}, logger)
}

// NewMemorySchedulerWithSystem creates a scheduler with a custom MemorySystem and logger (for testing).
func NewMemorySchedulerWithSystem(cfg MemorySchedulerConfig, sys MemorySystem, logger log.FieldLogger) (*MemoryScheduler, error) {
	if cfg.MaxCommitMB < 0 || cfg.ReserveMB < 0 || cfg.AcquireTimeout < 0 {
		return nil, errors.New("config values must be non-negative")
	}

	maxCommit := cfg.MaxCommitMB
	if maxCommit == 0 {
		total, err := sys.GetTotalMemoryMB()
		if err != nil {
			return nil, fmt.Errorf("detect total memory: %w", err)
		}
		maxCommit = int64(float64(total) * defaultMemoryCommitRatio)
	}

	return &MemoryScheduler{
		committed:      semaphore.NewWeighted(maxCommit),
		maxCommitMB:    maxCommit,
		reserveMB:      cfg.ReserveMB,
		timeout:        cfg.AcquireTimeout,
		minJobMemoryMB: cfg.MinJobMemoryMB,
		sys:            sys,
		logger:         logger,
	}, nil
}

// Acquire reserves memory for a VM. Blocks until memory is available
// or context is cancelled. Returns error if timeout expires.
//
// Acquire is safe for concurrent use by multiple goroutines.
func (s *MemoryScheduler) Acquire(ctx context.Context, memoryMB int64) error {
	if memoryMB <= 0 {
		return fmt.Errorf("%w: memoryMB must be positive, got %d", ErrInvalidMemoryRequest, memoryMB)
	}

	// Early rejection if request can never be satisfied
	if memoryMB > s.maxCommitMB {
		return fmt.Errorf("%w: requested %dMB exceeds maximum commit capacity %dMB",
			ErrInvalidMemoryRequest, memoryMB, s.maxCommitMB)
	}

	stats := s.Stats()
	s.logger.Debugf("acquiring memory: requested=%dMB, committed=%dMB/%dMB, waiting=%d",
		memoryMB, stats.CommittedMB, stats.MaxCommitMB, stats.WaitingJobs)

	s.waitingJobs.Add(1)
	defer s.waitingJobs.Add(-1)

	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	if err := s.committed.Acquire(ctx, memoryMB); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			s.logger.Debugf("acquire timeout after %v: %dMB requested, %dMB committed of %dMB max",
				s.timeout, memoryMB, s.committedMB.Load(), s.maxCommitMB)
			return fmt.Errorf("%w after %v: %dMB requested, %dMB committed of %dMB max",
				ErrAcquireTimeout, s.timeout, memoryMB, s.committedMB.Load(), s.maxCommitMB)
		}
		return err
	}

	s.committedMB.Add(memoryMB)
	s.logger.Debugf("acquired memory: %dMB, new committed=%dMB/%dMB",
		memoryMB, s.committedMB.Load(), s.maxCommitMB)
	return nil
}

// Release returns memory to the pool when a VM terminates.
// Callers must release exactly the amount they acquired.
func (s *MemoryScheduler) Release(memoryMB int64) {
	if memoryMB <= 0 {
		return // No-op for invalid values
	}
	s.committed.Release(memoryMB)
	s.committedMB.Add(-memoryMB)
	s.logger.Debugf("released memory: %dMB, new committed=%dMB/%dMB",
		memoryMB, s.committedMB.Load(), s.maxCommitMB)
}

// Stats returns current scheduler statistics.
func (s *MemoryScheduler) Stats() MemorySchedulerStats {
	return MemorySchedulerStats{
		CommittedMB: s.committedMB.Load(),
		MaxCommitMB: s.maxCommitMB,
		WaitingJobs: s.waitingJobs.Load(),
	}
}

// MaxCommitMB returns the maximum memory that can be committed.
func (s *MemoryScheduler) MaxCommitMB() int64 {
	return s.maxCommitMB
}

// Available returns the currently available memory capacity in MB.
func (s *MemoryScheduler) Available() int64 {
	return s.maxCommitMB - s.committedMB.Load()
}

// MinJobMemoryMB returns the configured minimum job memory.
// Returns 0 if memory-aware job claiming is disabled.
func (s *MemoryScheduler) MinJobMemoryMB() int64 {
	return s.minJobMemoryMB
}
