package watch

import (
	"sync"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// EventType represents the type of watch event
type EventType int

const (
	EventCreated EventType = iota
	EventDeleted
	EventDataChanged
	EventChildrenChanged
)

func (e EventType) String() string {
	switch e {
	case EventCreated:
		return "Created"
	case EventDeleted:
		return "Deleted"
	case EventDataChanged:
		return "DataChanged"
	case EventChildrenChanged:
		return "ChildrenChanged"
	default:
		return "Unknown"
	}
}

// Event represents a watch event
type Event struct {
	ID      string
	Type    EventType
	Path    string
	Data    []byte
	Version uint64
}

// Watcher represents a registered watch
type Watcher struct {
	ID         string
	Path       string
	EventCh    chan Event
	Persistent bool
	triggered  bool
}

// Manager manages watches and event delivery
type Manager struct {
	mu       sync.RWMutex
	watchers map[string][]*Watcher // path -> watchers
	logger   zerolog.Logger
}

// NewManager creates a new watch manager
func NewManager(logger zerolog.Logger) *Manager {
	return &Manager{
		watchers: make(map[string][]*Watcher),
		logger:   logger.With().Str("component", "watch").Logger(),
	}
}

// Watch registers a new watch on a path
func (m *Manager) Watch(path string, persistent bool) *Watcher {
	m.mu.Lock()
	defer m.mu.Unlock()

	watcher := &Watcher{
		ID:         uuid.New().String(),
		Path:       path,
		EventCh:    make(chan Event, 10),
		Persistent: persistent,
	}

	m.watchers[path] = append(m.watchers[path], watcher)

	m.logger.Debug().
		Str("watchID", watcher.ID).
		Str("path", path).
		Bool("persistent", persistent).
		Msg("Watch registered")

	return watcher
}

// Unwatch removes a watch
func (m *Manager) Unwatch(watcher *Watcher) {
	m.mu.Lock()
	defer m.mu.Unlock()

	watchers, ok := m.watchers[watcher.Path]
	if !ok {
		return
	}

	for i, w := range watchers {
		if w.ID == watcher.ID {
			close(w.EventCh)
			m.watchers[watcher.Path] = append(watchers[:i], watchers[i+1:]...)
			m.logger.Debug().
				Str("watchID", watcher.ID).
				Str("path", watcher.Path).
				Msg("Watch removed")
			break
		}
	}

	// Clean up empty paths
	if len(m.watchers[watcher.Path]) == 0 {
		delete(m.watchers, watcher.Path)
	}
}

// TriggerEvent triggers an event for all watchers on a path
func (m *Manager) TriggerEvent(event Event) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event.ID = uuid.New().String()

	watchers, ok := m.watchers[event.Path]
	if !ok {
		return
	}

	// Collect watchers to remove (one-time watches)
	toRemove := make([]*Watcher, 0)

	for _, watcher := range watchers {
		// Send event
		select {
		case watcher.EventCh <- event:
			m.logger.Debug().
				Str("watchID", watcher.ID).
				Str("path", event.Path).
				Str("type", event.Type.String()).
				Msg("Event triggered")
		default:
			m.logger.Warn().
				Str("watchID", watcher.ID).
				Str("path", event.Path).
				Msg("Event channel full, dropping event")
		}

		// Mark one-time watches for removal
		if !watcher.Persistent && !watcher.triggered {
			watcher.triggered = true
			toRemove = append(toRemove, watcher)
		}
	}

	// Remove one-time watches
	for _, w := range toRemove {
		for i, watcher := range m.watchers[event.Path] {
			if watcher.ID == w.ID {
				close(watcher.EventCh)
				m.watchers[event.Path] = append(m.watchers[event.Path][:i], m.watchers[event.Path][i+1:]...)
				break
			}
		}
	}

	// Clean up empty paths
	if len(m.watchers[event.Path]) == 0 {
		delete(m.watchers, event.Path)
	}
}

// TriggerCreate triggers a creation event
func (m *Manager) TriggerCreate(path string, data []byte, version uint64) {
	m.TriggerEvent(Event{
		Type:    EventCreated,
		Path:    path,
		Data:    data,
		Version: version,
	})
}

// TriggerDelete triggers a deletion event
func (m *Manager) TriggerDelete(path string) {
	m.TriggerEvent(Event{
		Type: EventDeleted,
		Path: path,
	})
}

// TriggerDataChange triggers a data change event
func (m *Manager) TriggerDataChange(path string, data []byte, version uint64) {
	m.TriggerEvent(Event{
		Type:    EventDataChanged,
		Path:    path,
		Data:    data,
		Version: version,
	})
}

// TriggerChildrenChange triggers a children change event
func (m *Manager) TriggerChildrenChange(path string) {
	m.TriggerEvent(Event{
		Type: EventChildrenChanged,
		Path: path,
	})
}

// GetWatchCount returns the number of active watches on a path
func (m *Manager) GetWatchCount(path string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	watchers, ok := m.watchers[path]
	if !ok {
		return 0
	}
	return len(watchers)
}

// GetTotalWatchCount returns the total number of active watches
func (m *Manager) GetTotalWatchCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, watchers := range m.watchers {
		count += len(watchers)
	}
	return count
}
