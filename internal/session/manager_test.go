package session

import (
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestSessionCreate(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	mgr := NewManager(10*time.Second, 60*time.Second, logger)

	sess := mgr.Create("client-1", 0)
	if sess == nil {
		t.Fatal("expected session to be created")
	}

	if sess.ClientID != "client-1" {
		t.Errorf("expected client ID client-1, got %s", sess.ClientID)
	}

	if sess.TTL != 10*time.Second {
		t.Errorf("expected default TTL 10s, got %v", sess.TTL)
	}

	if mgr.Count() != 1 {
		t.Errorf("expected 1 session, got %d", mgr.Count())
	}
}

func TestSessionHeartbeat(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	mgr := NewManager(10*time.Second, 60*time.Second, logger)

	sess := mgr.Create("client-1", 0)

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Heartbeat should succeed
	if !mgr.Heartbeat(sess.ID) {
		t.Error("expected heartbeat to succeed")
	}

	// Heartbeat for non-existent session should fail
	if mgr.Heartbeat("non-existent") {
		t.Error("expected heartbeat for non-existent session to fail")
	}
}

func TestSessionClose(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	mgr := NewManager(10*time.Second, 60*time.Second, logger)

	sess := mgr.Create("client-1", 0)

	if !mgr.Close(sess.ID) {
		t.Error("expected close to succeed")
	}

	if mgr.Count() != 0 {
		t.Errorf("expected 0 sessions after close, got %d", mgr.Count())
	}

	if mgr.Close(sess.ID) {
		t.Error("expected second close to fail")
	}
}

func TestSessionExpiration(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	mgr := NewManager(100*time.Millisecond, 1*time.Second, logger)
	mgr.Start()
	defer mgr.Stop()

	expired := make(chan string, 1)
	mgr.SetOnExpire(func(sess *Session) {
		expired <- sess.ID
	})

	sess := mgr.Create("client-1", 100*time.Millisecond)

	// Wait for expiration
	select {
	case id := <-expired:
		if id != sess.ID {
			t.Errorf("expected expired session %s, got %s", sess.ID, id)
		}
	case <-time.After(2 * time.Second):
		t.Error("session did not expire in time")
	}
}

func TestSessionEphemeralTracking(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	mgr := NewManager(10*time.Second, 60*time.Second, logger)

	sess := mgr.Create("client-1", 0)

	// Register ephemeral keys
	mgr.RegisterEphemeral(sess.ID, "/test/key1")
	mgr.RegisterEphemeral(sess.ID, "/test/key2")

	stored := mgr.Get(sess.ID)
	if len(stored.EphemeralKeys) != 2 {
		t.Errorf("expected 2 ephemeral keys, got %d", len(stored.EphemeralKeys))
	}

	// Unregister one
	mgr.UnregisterEphemeral(sess.ID, "/test/key1")

	stored = mgr.Get(sess.ID)
	if len(stored.EphemeralKeys) != 1 {
		t.Errorf("expected 1 ephemeral key after unregister, got %d", len(stored.EphemeralKeys))
	}
}
