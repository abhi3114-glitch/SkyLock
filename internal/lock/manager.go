package lock

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/skylock/internal/store"
)

var (
	// ErrLockNotHeld is returned when trying to release a lock that isn't held
	ErrLockNotHeld = errors.New("lock not held")

	// ErrLockTimeout is returned when lock acquisition times out
	ErrLockTimeout = errors.New("lock acquisition timed out")

	// ErrLockExists is returned when a lock already exists
	ErrLockExists = errors.New("lock already exists")
)

// Lock represents a distributed lock
type Lock struct {
	Name      string
	Owner     string
	SessionID string
	Path      string
	Sequence  uint64
	Acquired  time.Time
}

// Manager manages distributed locks
type Manager struct {
	mu     sync.RWMutex
	store  *store.Store
	logger zerolog.Logger

	// Active locks by path
	locks map[string]*Lock

	// Lock waiters
	waiters map[string][]chan struct{}
}

// NewManager creates a new lock manager
func NewManager(s *store.Store, logger zerolog.Logger) *Manager {
	m := &Manager{
		store:   s,
		logger:  logger.With().Str("component", "lock").Logger(),
		locks:   make(map[string]*Lock),
		waiters: make(map[string][]chan struct{}),
	}

	// Set up watch callbacks for lock notifications
	s.SetWatchCallbacks(nil, nil, m.onNodeDeleted)

	return m
}

// onNodeDeleted handles node deletion events (for lock release)
func (m *Manager) onNodeDeleted(path string) {
	if !strings.HasPrefix(path, "/locks/") {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Extract lock name from path
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return
	}
	lockName := parts[2]

	// Notify waiters
	waiters, ok := m.waiters[lockName]
	if ok && len(waiters) > 0 {
		// Notify the first waiter
		select {
		case waiters[0] <- struct{}{}:
		default:
		}
	}
}

// Lock acquires a distributed lock (blocking)
func (m *Manager) Lock(name, owner, sessionID string) (*Lock, error) {
	return m.TryLock(name, owner, sessionID, 0) // 0 means wait forever
}

// TryLock attempts to acquire a lock with a timeout
// timeout of 0 means wait forever
// timeout of -1 means return immediately if lock not available
func (m *Manager) TryLock(name, owner, sessionID string, timeout time.Duration) (*Lock, error) {
	lockPath := "/locks/" + name

	// Ensure lock directory exists
	if !m.store.Exists(lockPath) {
		m.store.Create(lockPath, nil, false, "", false)
	}

	// Create ephemeral sequential node under lock path
	nodePath, err := m.store.Create(lockPath+"/lock-", []byte(owner), true, sessionID, true)
	if err != nil {
		return nil, err
	}

	// Try to acquire the lock
	lock := &Lock{
		Name:      name,
		Owner:     owner,
		SessionID: sessionID,
		Path:      nodePath,
	}

	// Extract our sequence number from the path
	baseName := strings.TrimPrefix(nodePath, lockPath+"/")
	parts := strings.Split(baseName, "-")
	if len(parts) == 2 {
		// Parse sequence from the suffix
		var seq uint64
		for _, c := range parts[1] {
			seq = seq*10 + uint64(c-'0')
		}
		lock.Sequence = seq
	}

	// Check if we have the lock
	acquired, err := m.checkLockAcquired(lockPath, nodePath)
	if err != nil {
		m.store.Delete(nodePath, -1)
		return nil, err
	}

	if acquired {
		lock.Acquired = time.Now()
		m.mu.Lock()
		m.locks[nodePath] = lock
		m.mu.Unlock()
		m.logger.Info().
			Str("lock", name).
			Str("owner", owner).
			Str("path", nodePath).
			Msg("Lock acquired")
		return lock, nil
	}

	// If timeout is -1, return immediately
	if timeout < 0 {
		m.store.Delete(nodePath, -1)
		return nil, ErrLockTimeout
	}

	// Wait for lock
	return m.waitForLock(lock, lockPath, nodePath, timeout)
}

// checkLockAcquired checks if we have the lowest sequence number
func (m *Manager) checkLockAcquired(lockPath, ourPath string) (bool, error) {
	children, err := m.store.GetChildren(lockPath)
	if err != nil {
		return false, err
	}

	if len(children) == 0 {
		return false, nil
	}

	// Sort children by sequence number
	sort.Strings(children)

	// Check if we're first
	ourName := strings.TrimPrefix(ourPath, lockPath+"/")
	return children[0] == ourName, nil
}

// waitForLock waits for the lock to be acquired
func (m *Manager) waitForLock(lock *Lock, lockPath, ourPath string, timeout time.Duration) (*Lock, error) {
	waiter := make(chan struct{}, 1)

	m.mu.Lock()
	m.waiters[lock.Name] = append(m.waiters[lock.Name], waiter)
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		waiters := m.waiters[lock.Name]
		for i, w := range waiters {
			if w == waiter {
				m.waiters[lock.Name] = append(waiters[:i], waiters[i+1:]...)
				break
			}
		}
		m.mu.Unlock()
	}()

	var timer <-chan time.Time
	if timeout > 0 {
		timer = time.After(timeout)
	}

	for {
		select {
		case <-waiter:
			// Check if we now have the lock
			acquired, err := m.checkLockAcquired(lockPath, ourPath)
			if err != nil {
				m.store.Delete(ourPath, -1)
				return nil, err
			}
			if acquired {
				lock.Acquired = time.Now()
				m.mu.Lock()
				m.locks[ourPath] = lock
				m.mu.Unlock()
				m.logger.Info().
					Str("lock", lock.Name).
					Str("owner", lock.Owner).
					Str("path", ourPath).
					Msg("Lock acquired after wait")
				return lock, nil
			}

		case <-timer:
			m.store.Delete(ourPath, -1)
			return nil, ErrLockTimeout
		}
	}
}

// Unlock releases a distributed lock
func (m *Manager) Unlock(lock *Lock) error {
	if lock == nil || lock.Path == "" {
		return ErrLockNotHeld
	}

	m.mu.Lock()
	delete(m.locks, lock.Path)
	m.mu.Unlock()

	if err := m.store.Delete(lock.Path, -1); err != nil {
		return err
	}

	m.logger.Info().
		Str("lock", lock.Name).
		Str("owner", lock.Owner).
		Msg("Lock released")

	return nil
}

// IsLocked checks if a lock is currently held
func (m *Manager) IsLocked(name string) bool {
	lockPath := "/locks/" + name
	children, err := m.store.GetChildren(lockPath)
	if err != nil {
		return false
	}
	return len(children) > 0
}

// GetLockHolder returns the current lock holder, if any
func (m *Manager) GetLockHolder(name string) (string, error) {
	lockPath := "/locks/" + name
	children, err := m.store.GetChildren(lockPath)
	if err != nil {
		return "", err
	}

	if len(children) == 0 {
		return "", nil
	}

	sort.Strings(children)
	holderPath := lockPath + "/" + children[0]
	node, err := m.store.Get(holderPath)
	if err != nil {
		return "", err
	}

	return string(node.Data), nil
}

// GetQueueLength returns the number of clients waiting for a lock
func (m *Manager) GetQueueLength(name string) int {
	lockPath := "/locks/" + name
	children, err := m.store.GetChildren(lockPath)
	if err != nil {
		return 0
	}
	return len(children)
}

// LockWithID acquires a lock with a specific ID for idempotency
func (m *Manager) LockWithID(name, owner, sessionID, lockID string) (*Lock, error) {
	if lockID == "" {
		lockID = uuid.New().String()
	}
	return m.Lock(name, owner, sessionID)
}
