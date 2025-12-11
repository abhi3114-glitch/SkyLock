package session

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Session represents a client session
type Session struct {
	ID            string
	ClientID      string
	CreatedAt     time.Time
	LastHeartbeat time.Time
	TTL           time.Duration
	EphemeralKeys []string
}

// IsExpired returns true if the session has expired
func (s *Session) IsExpired() bool {
	return time.Since(s.LastHeartbeat) > s.TTL
}

// Manager manages client sessions
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	logger   zerolog.Logger

	defaultTTL time.Duration
	maxTTL     time.Duration

	// Callback for when a session expires
	onExpire func(session *Session)

	stopCh chan struct{}
}

// NewManager creates a new session manager
func NewManager(defaultTTL, maxTTL time.Duration, logger zerolog.Logger) *Manager {
	return &Manager{
		sessions:   make(map[string]*Session),
		logger:     logger.With().Str("component", "session").Logger(),
		defaultTTL: defaultTTL,
		maxTTL:     maxTTL,
		stopCh:     make(chan struct{}),
	}
}

// Start begins the session manager's background tasks
func (m *Manager) Start() {
	go m.expirationLoop()
}

// Stop stops the session manager
func (m *Manager) Stop() {
	close(m.stopCh)
}

// SetOnExpire sets the callback for session expiration
func (m *Manager) SetOnExpire(f func(session *Session)) {
	m.onExpire = f
}

// Create creates a new session
func (m *Manager) Create(clientID string, ttl time.Duration) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate TTL
	if ttl <= 0 {
		ttl = m.defaultTTL
	}
	if ttl > m.maxTTL {
		ttl = m.maxTTL
	}

	session := &Session{
		ID:            uuid.New().String(),
		ClientID:      clientID,
		CreatedAt:     time.Now(),
		LastHeartbeat: time.Now(),
		TTL:           ttl,
		EphemeralKeys: make([]string, 0),
	}

	m.sessions[session.ID] = session
	m.logger.Info().
		Str("sessionID", session.ID).
		Str("clientID", clientID).
		Dur("ttl", ttl).
		Msg("Created session")

	return session
}

// Get returns a session by ID
func (m *Manager) Get(sessionID string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[sessionID]
}

// Heartbeat refreshes a session's last heartbeat time
func (m *Manager) Heartbeat(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return false
	}

	if session.IsExpired() {
		delete(m.sessions, sessionID)
		return false
	}

	session.LastHeartbeat = time.Now()
	m.logger.Debug().
		Str("sessionID", sessionID).
		Msg("Heartbeat received")

	return true
}

// Close closes a session
func (m *Manager) Close(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return false
	}

	delete(m.sessions, sessionID)
	m.logger.Info().
		Str("sessionID", sessionID).
		Msg("Session closed")

	// Trigger expiration callback
	if m.onExpire != nil {
		m.onExpire(session)
	}

	return true
}

// RegisterEphemeral registers an ephemeral key with a session
func (m *Manager) RegisterEphemeral(sessionID, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return false
	}

	session.EphemeralKeys = append(session.EphemeralKeys, key)
	return true
}

// UnregisterEphemeral removes an ephemeral key from a session
func (m *Manager) UnregisterEphemeral(sessionID, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return false
	}

	for i, k := range session.EphemeralKeys {
		if k == key {
			session.EphemeralKeys = append(session.EphemeralKeys[:i], session.EphemeralKeys[i+1:]...)
			return true
		}
	}

	return false
}

// expirationLoop periodically checks for expired sessions
func (m *Manager) expirationLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkExpirations()
		}
	}
}

// checkExpirations checks for and handles expired sessions
func (m *Manager) checkExpirations() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, session := range m.sessions {
		if session.IsExpired() {
			m.logger.Warn().
				Str("sessionID", id).
				Str("clientID", session.ClientID).
				Msg("Session expired")

			delete(m.sessions, id)

			// Trigger expiration callback
			if m.onExpire != nil {
				go m.onExpire(session)
			}
		}
	}
}

// Count returns the number of active sessions
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// All returns all active sessions
func (m *Manager) All() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// HeartbeatClient provides client-side heartbeat functionality
type HeartbeatClient struct {
	sessionID string
	interval  time.Duration
	heartbeat func(ctx context.Context, sessionID string) error
	logger    zerolog.Logger
	stopCh    chan struct{}
}

// NewHeartbeatClient creates a new heartbeat client
func NewHeartbeatClient(sessionID string, interval time.Duration, heartbeatFunc func(ctx context.Context, sessionID string) error, logger zerolog.Logger) *HeartbeatClient {
	return &HeartbeatClient{
		sessionID: sessionID,
		interval:  interval,
		heartbeat: heartbeatFunc,
		logger:    logger.With().Str("component", "heartbeat").Logger(),
		stopCh:    make(chan struct{}),
	}
}

// Start begins sending heartbeats
func (h *HeartbeatClient) Start() {
	go h.run()
}

// Stop stops sending heartbeats
func (h *HeartbeatClient) Stop() {
	close(h.stopCh)
}

func (h *HeartbeatClient) run() {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), h.interval)
			if err := h.heartbeat(ctx, h.sessionID); err != nil {
				h.logger.Error().Err(err).Msg("Failed to send heartbeat")
			}
			cancel()
		}
	}
}
