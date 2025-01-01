package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Client is a SkyLock client SDK
type Client struct {
	mu        sync.RWMutex
	baseURL   string
	sessionID string
	http      *http.Client
	logger    zerolog.Logger

	// Background heartbeat
	heartbeatStop chan struct{}
	heartbeatWG   sync.WaitGroup
}

// NewClient creates a new SkyLock client
func NewClient(servers []string, logger zerolog.Logger) *Client {
	// For now, just use the first server
	baseURL := "http://localhost:8080"
	if len(servers) > 0 {
		baseURL = servers[0]
	}

	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger.With().Str("component", "client").Logger(),
	}
}

// Connect establishes a session with the SkyLock cluster
func (c *Client) Connect(ctx context.Context, clientID string) error {
	resp, err := c.doRequest(ctx, "POST", "/v1/session", map[string]interface{}{
		"client_id":   clientID,
		"ttl_seconds": 30,
	})
	if err != nil {
		return err
	}

	var result struct {
		SessionID string  `json:"session_id"`
		TTL       float64 `json:"ttl"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return err
	}

	c.mu.Lock()
	c.sessionID = result.SessionID
	c.mu.Unlock()

	// Start background heartbeat
	c.startHeartbeat(time.Duration(result.TTL/3) * time.Second)

	c.logger.Info().
		Str("sessionID", result.SessionID).
		Msg("Connected to SkyLock")

	return nil
}

// Close closes the client connection
func (c *Client) Close() error {
	// Stop heartbeat
	if c.heartbeatStop != nil {
		close(c.heartbeatStop)
		c.heartbeatWG.Wait()
	}

	// Close session
	c.mu.RLock()
	sessionID := c.sessionID
	c.mu.RUnlock()

	if sessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.doRequest(ctx, "DELETE", "/v1/session/"+sessionID, nil)
	}

	c.logger.Info().Msg("Disconnected from SkyLock")
	return nil
}

func (c *Client) startHeartbeat(interval time.Duration) {
	c.heartbeatStop = make(chan struct{})
	c.heartbeatWG.Add(1)

	go func() {
		defer c.heartbeatWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-c.heartbeatStop:
				return
			case <-ticker.C:
				c.mu.RLock()
				sessionID := c.sessionID
				c.mu.RUnlock()

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, err := c.doRequest(ctx, "POST", "/v1/session/"+sessionID+"/heartbeat", nil)
				cancel()

				if err != nil {
					c.logger.Error().Err(err).Msg("Heartbeat failed")
				}
			}
		}
	}()
}

// Lock represents an acquired distributed lock
type Lock struct {
	client *Client
	Name   string
	LockID string
	Owner  string
}

// Unlock releases the lock
func (l *Lock) Unlock(ctx context.Context) error {
	_, err := l.client.doRequest(ctx, "DELETE", "/v1/locks/"+l.Name, map[string]interface{}{
		"lock_id": l.LockID,
	})
	return err
}

// Lock acquires a distributed lock
func (c *Client) Lock(ctx context.Context, name string) (*Lock, error) {
	return c.TryLock(ctx, name, 0)
}

// TryLock attempts to acquire a lock with a timeout
func (c *Client) TryLock(ctx context.Context, name string, timeout time.Duration) (*Lock, error) {
	c.mu.RLock()
	sessionID := c.sessionID
	c.mu.RUnlock()

	if sessionID == "" {
		return nil, fmt.Errorf("not connected")
	}

	timeoutMs := int(timeout.Milliseconds())
	if timeoutMs == 0 {
		timeoutMs = 30000
	}

	resp, err := c.doRequest(ctx, "POST", "/v1/locks/"+name, map[string]interface{}{
		"owner":      "sdk-client",
		"session_id": sessionID,
		"timeout_ms": timeoutMs,
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		LockID string `json:"lock_id"`
		Name   string `json:"name"`
		Owner  string `json:"owner"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}

	return &Lock{
		client: c,
		Name:   result.Name,
		LockID: result.LockID,
		Owner:  result.Owner,
	}, nil
}

// Create creates a node
func (c *Client) Create(ctx context.Context, path string, data []byte, ephemeral bool) (string, error) {
	c.mu.RLock()
	sessionID := c.sessionID
	c.mu.RUnlock()

	if ephemeral && sessionID == "" {
		return "", fmt.Errorf("not connected")
	}

	resp, err := c.doRequest(ctx, "PUT", "/v1/nodes/"+path, map[string]interface{}{
		"data":       string(data),
		"ephemeral":  ephemeral,
		"session_id": sessionID,
	})
	if err != nil {
		return "", err
	}

	var result struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", err
	}

	return result.Path, nil
}

// Get retrieves a node's data
func (c *Client) Get(ctx context.Context, path string) ([]byte, uint64, error) {
	resp, err := c.doRequest(ctx, "GET", "/v1/nodes/"+path, nil)
	if err != nil {
		return nil, 0, err
	}

	var result struct {
		Data    string `json:"data"`
		Version uint64 `json:"version"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, 0, err
	}

	return []byte(result.Data), result.Version, nil
}

// Set updates a node's data
func (c *Client) Set(ctx context.Context, path string, data []byte, version int64) (uint64, error) {
	resp, err := c.doRequest(ctx, "POST", "/v1/nodes/"+path, map[string]interface{}{
		"data":    string(data),
		"version": version,
	})
	if err != nil {
		return 0, err
	}

	var result struct {
		Version uint64 `json:"version"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return 0, err
	}

	return result.Version, nil
}

// Delete removes a node
func (c *Client) Delete(ctx context.Context, path string) error {
	_, err := c.doRequest(ctx, "DELETE", "/v1/nodes/"+path, nil)
	return err
}

// GetChildren returns a node's children
func (c *Client) GetChildren(ctx context.Context, path string) ([]string, error) {
	resp, err := c.doRequest(ctx, "GET", "/v1/children/"+path, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Children []string `json:"children"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}

	return result.Children, nil
}

// GetLeader returns the current leader
func (c *Client) GetLeader(ctx context.Context) (string, error) {
	resp, err := c.doRequest(ctx, "GET", "/v1/cluster/leader", nil)
	if err != nil {
		return "", err
	}

	var result struct {
		LeaderID string `json:"leader_id"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", err
	}

	return result.LeaderID, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}
