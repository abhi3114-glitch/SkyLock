package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/skylock/internal/lock"
	"github.com/skylock/internal/raft"
	"github.com/skylock/internal/session"
	"github.com/skylock/internal/store"
	"github.com/skylock/internal/watch"
)

// Server is the HTTP REST API server
type Server struct {
	router         *chi.Mux
	httpServer     *http.Server
	logger         zerolog.Logger
	raftNode       *raft.Node
	sessionManager *session.Manager
	store          *store.Store
	lockManager    *lock.Manager
	watchManager   *watch.Manager
}

// NewServer creates a new API server
func NewServer(
	raftNode *raft.Node,
	sessionManager *session.Manager,
	store *store.Store,
	lockManager *lock.Manager,
	watchManager *watch.Manager,
	logger zerolog.Logger,
) *Server {
	s := &Server{
		router:         chi.NewRouter(),
		logger:         logger.With().Str("component", "api").Logger(),
		raftNode:       raftNode,
		sessionManager: sessionManager,
		store:          store,
		lockManager:    lockManager,
		watchManager:   watchManager,
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.RealIP)
	s.router.Use(middleware.Recoverer)
	s.router.Use(s.loggingMiddleware)

	// API routes
	s.router.Route("/v1", func(r chi.Router) {
		// Session management
		r.Post("/session", s.createSession)
		r.Post("/session/{id}/heartbeat", s.heartbeat)
		r.Delete("/session/{id}", s.closeSession)

		// Node operations
		r.Get("/nodes/*", s.getNode)
		r.Put("/nodes/*", s.createNode)
		r.Post("/nodes/*", s.setNode)
		r.Delete("/nodes/*", s.deleteNode)
		r.Get("/children/*", s.getChildren)

		// Lock operations
		r.Post("/locks/{name}", s.acquireLock)
		r.Delete("/locks/{name}", s.releaseLock)
		r.Get("/locks/{name}", s.getLockInfo)

		// Cluster operations
		r.Get("/cluster/leader", s.getLeader)
		r.Get("/cluster/status", s.getStatus)
	})

	// Health check
	s.router.Get("/health", s.healthCheck)
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Debug().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Dur("duration", time.Since(start)).
			Msg("Request handled")
	})
}

// Start starts the HTTP server
func (s *Server) Start(addr string) error {
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	s.logger.Info().Str("addr", addr).Msg("Starting HTTP server")
	return s.httpServer.ListenAndServe()
}

// Stop gracefully stops the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Response helpers
func (s *Server) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) respondError(w http.ResponseWriter, status int, message string) {
	s.respondJSON(w, status, map[string]string{"error": message})
}

// Session handlers
func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID string `json:"client_id"`
		TTL      int    `json:"ttl_seconds"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ClientID == "" {
		s.respondError(w, http.StatusBadRequest, "client_id required")
		return
	}

	ttl := time.Duration(req.TTL) * time.Second
	sess := s.sessionManager.Create(req.ClientID, ttl)

	s.respondJSON(w, http.StatusCreated, map[string]interface{}{
		"session_id": sess.ID,
		"ttl":        sess.TTL.Seconds(),
	})
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")

	if !s.sessionManager.Heartbeat(sessionID) {
		s.respondError(w, http.StatusNotFound, "session not found or expired")
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) closeSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")

	if !s.sessionManager.Close(sessionID) {
		s.respondError(w, http.StatusNotFound, "session not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Node handlers
func (s *Server) getNode(w http.ResponseWriter, r *http.Request) {
	path := "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")

	node, err := s.store.Get(path)
	if err != nil {
		if err == store.ErrNodeNotFound {
			s.respondError(w, http.StatusNotFound, "node not found")
		} else {
			s.respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"path":        node.Path,
		"data":        string(node.Data),
		"version":     node.Version,
		"ephemeral":   node.Ephemeral,
		"children":    node.Children,
		"created_at":  node.CreateTime,
		"modified_at": node.ModifyTime,
	})
}

func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	path := "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")

	var req struct {
		Data       string `json:"data"`
		Ephemeral  bool   `json:"ephemeral"`
		Sequential bool   `json:"sequential"`
		SessionID  string `json:"session_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Ephemeral && req.SessionID == "" {
		s.respondError(w, http.StatusBadRequest, "session_id required for ephemeral nodes")
		return
	}

	actualPath, err := s.store.Create(path, []byte(req.Data), req.Ephemeral, req.SessionID, req.Sequential)
	if err != nil {
		if err == store.ErrNodeExists {
			s.respondError(w, http.StatusConflict, "node already exists")
		} else if err == store.ErrNodeNotFound {
			s.respondError(w, http.StatusNotFound, "parent node not found")
		} else {
			s.respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// Register ephemeral key with session
	if req.Ephemeral {
		s.sessionManager.RegisterEphemeral(req.SessionID, actualPath)
	}

	s.respondJSON(w, http.StatusCreated, map[string]string{"path": actualPath})
}

func (s *Server) setNode(w http.ResponseWriter, r *http.Request) {
	path := "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")

	var req struct {
		Data    string `json:"data"`
		Version int64  `json:"version"` // -1 for any version
	}
	req.Version = -1

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	node, err := s.store.Set(path, []byte(req.Data), req.Version)
	if err != nil {
		if err == store.ErrNodeNotFound {
			s.respondError(w, http.StatusNotFound, "node not found")
		} else if err == store.ErrVersionMismatch {
			s.respondError(w, http.StatusConflict, "version mismatch")
		} else {
			s.respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"path":    node.Path,
		"version": node.Version,
	})
}

func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	path := "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")

	versionStr := r.URL.Query().Get("version")
	version := int64(-1)
	if versionStr != "" {
		// Parse version
		for _, c := range versionStr {
			version = version*10 + int64(c-'0')
		}
	}

	if err := s.store.Delete(path, version); err != nil {
		if err == store.ErrNodeNotFound {
			s.respondError(w, http.StatusNotFound, "node not found")
		} else if err == store.ErrVersionMismatch {
			s.respondError(w, http.StatusConflict, "version mismatch")
		} else if err == store.ErrNotEmpty {
			s.respondError(w, http.StatusBadRequest, "node has children")
		} else {
			s.respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getChildren(w http.ResponseWriter, r *http.Request) {
	path := "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")

	children, err := s.store.GetChildren(path)
	if err != nil {
		if err == store.ErrNodeNotFound {
			s.respondError(w, http.StatusNotFound, "node not found")
		} else {
			s.respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"path":     path,
		"children": children,
	})
}

// Lock handlers
func (s *Server) acquireLock(w http.ResponseWriter, r *http.Request) {
	lockName := chi.URLParam(r, "name")

	var req struct {
		Owner     string `json:"owner"`
		SessionID string `json:"session_id"`
		Timeout   int    `json:"timeout_ms"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.SessionID == "" {
		s.respondError(w, http.StatusBadRequest, "session_id required")
		return
	}

	timeout := time.Duration(req.Timeout) * time.Millisecond
	if timeout == 0 {
		timeout = 30 * time.Second // Default timeout
	}

	lck, err := s.lockManager.TryLock(lockName, req.Owner, req.SessionID, timeout)
	if err != nil {
		if err == lock.ErrLockTimeout {
			s.respondError(w, http.StatusRequestTimeout, "lock acquisition timed out")
		} else {
			s.respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"lock_id":     lck.Path,
		"name":        lck.Name,
		"acquired":    true,
		"owner":       lck.Owner,
		"acquired_at": lck.Acquired,
	})
}

func (s *Server) releaseLock(w http.ResponseWriter, r *http.Request) {
	lockName := chi.URLParam(r, "name")

	var req struct {
		LockID string `json:"lock_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	lck := &lock.Lock{
		Name: lockName,
		Path: req.LockID,
	}

	if err := s.lockManager.Unlock(lck); err != nil {
		if err == lock.ErrLockNotHeld {
			s.respondError(w, http.StatusNotFound, "lock not held")
		} else {
			s.respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getLockInfo(w http.ResponseWriter, r *http.Request) {
	lockName := chi.URLParam(r, "name")

	holder, err := s.lockManager.GetLockHolder(lockName)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	queueLen := s.lockManager.GetQueueLength(lockName)

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"name":         lockName,
		"locked":       holder != "",
		"holder":       holder,
		"queue_length": queueLen,
	})
}

// Cluster handlers
func (s *Server) getLeader(w http.ResponseWriter, r *http.Request) {
	leaderID := s.raftNode.GetLeaderID()
	isLeader := s.raftNode.IsLeader()

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"leader_id": leaderID,
		"is_leader": isLeader,
	})
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"leader_id": s.raftNode.GetLeaderID(),
		"is_leader": s.raftNode.IsLeader(),
		"sessions":  s.sessionManager.Count(),
		"nodes":     s.store.Count(),
		"watches":   s.watchManager.GetTotalWatchCount(),
	})
}

func (s *Server) healthCheck(w http.ResponseWriter, r *http.Request) {
	s.respondJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}
