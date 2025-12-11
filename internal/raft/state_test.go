package raft

import (
	"testing"
	"time"
)

func TestNewState(t *testing.T) {
	state := NewState()

	if state.CurrentTerm() != 0 {
		t.Errorf("expected term 0, got %d", state.CurrentTerm())
	}

	if state.GetState() != Follower {
		t.Errorf("expected Follower state, got %s", state.GetState())
	}

	if state.VotedFor() != "" {
		t.Errorf("expected empty votedFor, got %s", state.VotedFor())
	}
}

func TestStateTransitions(t *testing.T) {
	state := NewState()

	// Test become candidate
	term := state.BecomeCandidate()
	if term != 1 {
		t.Errorf("expected term 1 after becoming candidate, got %d", term)
	}
	if state.GetState() != Candidate {
		t.Errorf("expected Candidate state, got %s", state.GetState())
	}

	// Test become leader
	state.BecomeLeader("node-1", []string{"node-2", "node-3"})
	if state.GetState() != Leader {
		t.Errorf("expected Leader state, got %s", state.GetState())
	}
	if state.LeaderID() != "node-1" {
		t.Errorf("expected leader ID node-1, got %s", state.LeaderID())
	}

	// Test become follower
	state.BecomeFollower(5, "node-2")
	if state.GetState() != Follower {
		t.Errorf("expected Follower state, got %s", state.GetState())
	}
	if state.CurrentTerm() != 5 {
		t.Errorf("expected term 5, got %d", state.CurrentTerm())
	}
	if state.LeaderID() != "node-2" {
		t.Errorf("expected leader ID node-2, got %s", state.LeaderID())
	}
}

func TestHeartbeatTracking(t *testing.T) {
	state := NewState()

	before := time.Now()
	state.UpdateHeartbeat()
	after := time.Now()

	hb := state.LastHeartbeat()
	if hb.Before(before) || hb.After(after) {
		t.Error("heartbeat time outside expected range")
	}
}
