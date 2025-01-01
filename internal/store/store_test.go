package store

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
)

func TestStoreCreate(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	s := NewStore(logger)

	// Create a node
	path, err := s.Create("/test", []byte("data"), false, "", false)
	if err != nil {
		t.Fatalf("failed to create node: %v", err)
	}
	if path != "/test" {
		t.Errorf("expected path /test, got %s", path)
	}

	// Try to create duplicate
	_, err = s.Create("/test", []byte("data2"), false, "", false)
	if err != ErrNodeExists {
		t.Errorf("expected ErrNodeExists, got %v", err)
	}
}

func TestStoreGet(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	s := NewStore(logger)

	s.Create("/test", []byte("hello"), false, "", false)

	node, err := s.Get("/test")
	if err != nil {
		t.Fatalf("failed to get node: %v", err)
	}

	if string(node.Data) != "hello" {
		t.Errorf("expected data 'hello', got '%s'", string(node.Data))
	}

	if node.Version != 1 {
		t.Errorf("expected version 1, got %d", node.Version)
	}

	// Get non-existent
	_, err = s.Get("/nonexistent")
	if err != ErrNodeNotFound {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestStoreSet(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	s := NewStore(logger)

	s.Create("/test", []byte("original"), false, "", false)

	node, err := s.Set("/test", []byte("updated"), -1)
	if err != nil {
		t.Fatalf("failed to set node: %v", err)
	}

	if string(node.Data) != "updated" {
		t.Errorf("expected data 'updated', got '%s'", string(node.Data))
	}

	if node.Version != 2 {
		t.Errorf("expected version 2, got %d", node.Version)
	}

	// Test version mismatch
	_, err = s.Set("/test", []byte("v3"), 1)
	if err != ErrVersionMismatch {
		t.Errorf("expected ErrVersionMismatch, got %v", err)
	}
}

func TestStoreDelete(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	s := NewStore(logger)

	s.Create("/test", []byte("data"), false, "", false)

	err := s.Delete("/test", -1)
	if err != nil {
		t.Fatalf("failed to delete node: %v", err)
	}

	if s.Exists("/test") {
		t.Error("node should not exist after delete")
	}

	// Delete non-existent
	err = s.Delete("/nonexistent", -1)
	if err != ErrNodeNotFound {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestStoreChildren(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	s := NewStore(logger)

	s.Create("/parent", []byte(""), false, "", false)
	s.Create("/parent/child1", []byte(""), false, "", false)
	s.Create("/parent/child2", []byte(""), false, "", false)

	children, err := s.GetChildren("/parent")
	if err != nil {
		t.Fatalf("failed to get children: %v", err)
	}

	if len(children) != 2 {
		t.Errorf("expected 2 children, got %d", len(children))
	}
}

func TestStoreSequential(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	s := NewStore(logger)

	s.Create("/locks", []byte(""), false, "", false)

	// Create sequential nodes
	path1, _ := s.Create("/locks/lock-", []byte("client1"), false, "", true)
	path2, _ := s.Create("/locks/lock-", []byte("client2"), false, "", true)

	if path1 == path2 {
		t.Error("sequential paths should be different")
	}

	// Check that paths have sequence numbers
	if len(path1) <= len("/locks/lock-") {
		t.Error("sequential path should have sequence suffix")
	}
}

func TestStoreEphemeral(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	s := NewStore(logger)

	// Create ephemeral nodes
	s.Create("/session1-key1", []byte("data"), true, "session-1", false)
	s.Create("/session1-key2", []byte("data"), true, "session-1", false)
	s.Create("/session2-key1", []byte("data"), true, "session-2", false)

	// Delete ephemerals for session-1
	deleted := s.DeleteEphemerals("session-1")

	if len(deleted) != 2 {
		t.Errorf("expected 2 deleted nodes, got %d", len(deleted))
	}

	// Session-2's node should still exist
	if !s.Exists("/session2-key1") {
		t.Error("session-2 node should still exist")
	}
}

func TestStoreDeleteWithChildren(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	s := NewStore(logger)

	s.Create("/parent", []byte(""), false, "", false)
	s.Create("/parent/child", []byte(""), false, "", false)

	// Should fail because parent has children
	err := s.Delete("/parent", -1)
	if err != ErrNotEmpty {
		t.Errorf("expected ErrNotEmpty, got %v", err)
	}

	// Delete child first
	s.Delete("/parent/child", -1)

	// Now parent can be deleted
	err = s.Delete("/parent", -1)
	if err != nil {
		t.Errorf("failed to delete empty parent: %v", err)
	}
}
