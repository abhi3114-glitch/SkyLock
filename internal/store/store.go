package store

import (
	"errors"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

var (
	// ErrNodeExists is returned when trying to create a node that already exists
	ErrNodeExists = errors.New("node already exists")

	// ErrNodeNotFound is returned when a node is not found
	ErrNodeNotFound = errors.New("node not found")

	// ErrVersionMismatch is returned when the version doesn't match for CAS operations
	ErrVersionMismatch = errors.New("version mismatch")

	// ErrNotEmpty is returned when trying to delete a non-empty node
	ErrNotEmpty = errors.New("node has children")

	// ErrInvalidPath is returned for invalid node paths
	ErrInvalidPath = errors.New("invalid path")
)

// Node represents a data node in the hierarchical namespace
type Node struct {
	Path       string
	Data       []byte
	Version    uint64
	Ephemeral  bool
	SessionID  string
	Sequential bool
	Sequence   uint64
	Children   []string
	CreateTime time.Time
	ModifyTime time.Time
}

// Store is an in-memory hierarchical key-value store
type Store struct {
	mu     sync.RWMutex
	nodes  map[string]*Node
	logger zerolog.Logger

	// Sequence counter for sequential nodes
	seqCounter uint64

	// Watch callbacks
	onNodeCreate func(path string, node *Node)
	onNodeDelete func(path string)
	onNodeUpdate func(path string, node *Node)
}

// NewStore creates a new store
func NewStore(logger zerolog.Logger) *Store {
	s := &Store{
		nodes:  make(map[string]*Node),
		logger: logger.With().Str("component", "store").Logger(),
	}

	// Create root node
	s.nodes["/"] = &Node{
		Path:       "/",
		Data:       nil,
		Version:    0,
		Ephemeral:  false,
		Children:   make([]string, 0),
		CreateTime: time.Now(),
		ModifyTime: time.Now(),
	}

	return s
}

// SetWatchCallbacks sets the watch callbacks
func (s *Store) SetWatchCallbacks(onCreate, onUpdate func(path string, node *Node), onDelete func(path string)) {
	s.onNodeCreate = onCreate
	s.onNodeUpdate = onUpdate
	s.onNodeDelete = onDelete
}

// normalizePath normalizes a path
func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = path.Clean(p)
	return p
}

// parentPath returns the parent path of a given path
func parentPath(p string) string {
	if p == "/" {
		return ""
	}
	parent := path.Dir(p)
	if parent == "." {
		return "/"
	}
	return parent
}

// baseName returns the base name of a path
func baseName(p string) string {
	return path.Base(p)
}

// Create creates a new node
func (s *Store) Create(nodePath string, data []byte, ephemeral bool, sessionID string, sequential bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nodePath = normalizePath(nodePath)

	// Check parent exists
	parent := parentPath(nodePath)
	if parent != "" {
		parentNode, ok := s.nodes[parent]
		if !ok {
			return "", ErrNodeNotFound
		}
		if parentNode.Ephemeral {
			return "", errors.New("cannot create child under ephemeral node")
		}
	}

	// Handle sequential nodes
	actualPath := nodePath
	if sequential {
		seq := atomic.AddUint64(&s.seqCounter, 1)
		actualPath = nodePath + formatSequence(seq)
	}

	// Check if node already exists
	if _, ok := s.nodes[actualPath]; ok {
		return "", ErrNodeExists
	}

	// Create the node
	node := &Node{
		Path:       actualPath,
		Data:       data,
		Version:    1,
		Ephemeral:  ephemeral,
		SessionID:  sessionID,
		Sequential: sequential,
		Sequence:   s.seqCounter,
		Children:   make([]string, 0),
		CreateTime: time.Now(),
		ModifyTime: time.Now(),
	}

	s.nodes[actualPath] = node

	// Update parent's children
	if parent != "" {
		parentNode := s.nodes[parent]
		parentNode.Children = append(parentNode.Children, baseName(actualPath))
	}

	s.logger.Debug().
		Str("path", actualPath).
		Bool("ephemeral", ephemeral).
		Msg("Created node")

	// Trigger watch callback
	if s.onNodeCreate != nil {
		go s.onNodeCreate(actualPath, node)
	}

	return actualPath, nil
}

// formatSequence formats a sequence number with zero-padding
func formatSequence(seq uint64) string {
	return strings.Replace(strings.Replace(
		strings.Replace("%010d", "%d", "", 1),
		"%010d", "", 1), "", "", 1) +
		padLeft(itoa(seq), 10, '0')
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var result []byte
	for n > 0 {
		result = append([]byte{byte('0' + n%10)}, result...)
		n /= 10
	}
	return string(result)
}

func padLeft(s string, length int, pad byte) string {
	if len(s) >= length {
		return s
	}
	result := make([]byte, length)
	for i := 0; i < length-len(s); i++ {
		result[i] = pad
	}
	copy(result[length-len(s):], s)
	return string(result)
}

// Get retrieves a node by path
func (s *Store) Get(nodePath string) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodePath = normalizePath(nodePath)
	node, ok := s.nodes[nodePath]
	if !ok {
		return nil, ErrNodeNotFound
	}

	// Return a copy to prevent modification
	nodeCopy := *node
	nodeCopy.Children = make([]string, len(node.Children))
	copy(nodeCopy.Children, node.Children)

	return &nodeCopy, nil
}

// Set updates a node's data
func (s *Store) Set(nodePath string, data []byte, expectedVersion int64) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nodePath = normalizePath(nodePath)
	node, ok := s.nodes[nodePath]
	if !ok {
		return nil, ErrNodeNotFound
	}

	// Check version for CAS
	if expectedVersion >= 0 && uint64(expectedVersion) != node.Version {
		return nil, ErrVersionMismatch
	}

	node.Data = data
	node.Version++
	node.ModifyTime = time.Now()

	s.logger.Debug().
		Str("path", nodePath).
		Uint64("version", node.Version).
		Msg("Updated node")

	// Trigger watch callback
	if s.onNodeUpdate != nil {
		nodeCopy := *node
		go s.onNodeUpdate(nodePath, &nodeCopy)
	}

	nodeCopy := *node
	return &nodeCopy, nil
}

// Delete removes a node
func (s *Store) Delete(nodePath string, expectedVersion int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	nodePath = normalizePath(nodePath)
	if nodePath == "/" {
		return errors.New("cannot delete root")
	}

	node, ok := s.nodes[nodePath]
	if !ok {
		return ErrNodeNotFound
	}

	// Check version for CAS
	if expectedVersion >= 0 && uint64(expectedVersion) != node.Version {
		return ErrVersionMismatch
	}

	// Check if node has children
	if len(node.Children) > 0 {
		return ErrNotEmpty
	}

	// Remove from parent's children
	parent := parentPath(nodePath)
	if parent != "" {
		parentNode := s.nodes[parent]
		name := baseName(nodePath)
		for i, child := range parentNode.Children {
			if child == name {
				parentNode.Children = append(parentNode.Children[:i], parentNode.Children[i+1:]...)
				break
			}
		}
	}

	delete(s.nodes, nodePath)

	s.logger.Debug().
		Str("path", nodePath).
		Msg("Deleted node")

	// Trigger watch callback
	if s.onNodeDelete != nil {
		go s.onNodeDelete(nodePath)
	}

	return nil
}

// GetChildren returns the children of a node
func (s *Store) GetChildren(nodePath string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodePath = normalizePath(nodePath)
	node, ok := s.nodes[nodePath]
	if !ok {
		return nil, ErrNodeNotFound
	}

	children := make([]string, len(node.Children))
	copy(children, node.Children)
	sort.Strings(children)

	return children, nil
}

// Exists checks if a node exists
func (s *Store) Exists(nodePath string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodePath = normalizePath(nodePath)
	_, ok := s.nodes[nodePath]
	return ok
}

// DeleteEphemerals deletes all ephemeral nodes for a session
func (s *Store) DeleteEphemerals(sessionID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	deleted := make([]string, 0)

	// Find all ephemeral nodes for this session
	for path, node := range s.nodes {
		if node.Ephemeral && node.SessionID == sessionID {
			deleted = append(deleted, path)
		}
	}

	// Sort by path length descending to delete children first
	sort.Slice(deleted, func(i, j int) bool {
		return len(deleted[i]) > len(deleted[j])
	})

	// Delete each node
	for _, nodePath := range deleted {

		// Remove from parent's children
		parent := parentPath(nodePath)
		if parent != "" {
			if parentNode, ok := s.nodes[parent]; ok {
				name := baseName(nodePath)
				for i, child := range parentNode.Children {
					if child == name {
						parentNode.Children = append(parentNode.Children[:i], parentNode.Children[i+1:]...)
						break
					}
				}
			}
		}

		delete(s.nodes, nodePath)

		s.logger.Debug().
			Str("path", nodePath).
			Str("sessionID", sessionID).
			Msg("Deleted ephemeral node")

		// Trigger watch callback
		if s.onNodeDelete != nil {
			go s.onNodeDelete(nodePath)
		}
	}

	return deleted
}

// GetByPrefix returns all nodes with the given prefix
func (s *Store) GetByPrefix(prefix string) []*Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix = normalizePath(prefix)
	nodes := make([]*Node, 0)

	for path, node := range s.nodes {
		if strings.HasPrefix(path, prefix) {
			nodeCopy := *node
			nodes = append(nodes, &nodeCopy)
		}
	}

	return nodes
}

// Count returns the total number of nodes
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.nodes)
}
