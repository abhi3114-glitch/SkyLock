package raft

import (
	"sync"
	"time"
)

// NodeState represents the current state of a Raft node
type NodeState int

const (
	Follower NodeState = iota
	Candidate
	Leader
)

func (s NodeState) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

// State holds the persistent and volatile state of a Raft node
type State struct {
	mu sync.RWMutex

	// Persistent state (would be persisted to disk in production)
	currentTerm uint64
	votedFor    string
	log         *Log

	// Volatile state
	state       NodeState
	leaderID    string
	commitIndex uint64
	lastApplied uint64

	// Leader-specific volatile state
	nextIndex  map[string]uint64
	matchIndex map[string]uint64

	// Timing
	lastHeartbeat time.Time
}

// NewState creates a new Raft state
func NewState() *State {
	return &State{
		currentTerm: 0,
		votedFor:    "",
		log:         NewLog(),
		state:       Follower,
		leaderID:    "",
		commitIndex: 0,
		lastApplied: 0,
		nextIndex:   make(map[string]uint64),
		matchIndex:  make(map[string]uint64),
	}
}

// CurrentTerm returns the current term
func (s *State) CurrentTerm() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentTerm
}

// SetCurrentTerm updates the current term
func (s *State) SetCurrentTerm(term uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentTerm = term
	s.votedFor = "" // Reset vote when term changes
}

// IncrementTerm increments the term and returns the new value
func (s *State) IncrementTerm() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentTerm++
	s.votedFor = ""
	return s.currentTerm
}

// VotedFor returns the node this node voted for in current term
func (s *State) VotedFor() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.votedFor
}

// SetVotedFor sets the vote for this term
func (s *State) SetVotedFor(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.votedFor = nodeID
}

// GetState returns the current node state
func (s *State) GetState() NodeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// SetState sets the node state
func (s *State) SetState(state NodeState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

// LeaderID returns the current leader ID
func (s *State) LeaderID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.leaderID
}

// SetLeaderID sets the leader ID
func (s *State) SetLeaderID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leaderID = id
}

// CommitIndex returns the current commit index
func (s *State) CommitIndex() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.commitIndex
}

// SetCommitIndex sets the commit index
func (s *State) SetCommitIndex(index uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitIndex = index
}

// LastApplied returns the last applied index
func (s *State) LastApplied() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastApplied
}

// SetLastApplied sets the last applied index
func (s *State) SetLastApplied(index uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastApplied = index
}

// LastHeartbeat returns the time of the last heartbeat
func (s *State) LastHeartbeat() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastHeartbeat
}

// UpdateHeartbeat updates the last heartbeat time
func (s *State) UpdateHeartbeat() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHeartbeat = time.Now()
}

// BecomeFollower transitions to follower state
func (s *State) BecomeFollower(term uint64, leaderID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = Follower
	s.currentTerm = term
	s.leaderID = leaderID
	s.votedFor = ""
	s.lastHeartbeat = time.Now()
}

// BecomeCandidate transitions to candidate state
func (s *State) BecomeCandidate() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = Candidate
	s.currentTerm++
	s.leaderID = ""
	return s.currentTerm
}

// BecomeLeader transitions to leader state
func (s *State) BecomeLeader(nodeID string, peers []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = Leader
	s.leaderID = nodeID

	// Initialize leader state
	lastLogIndex := s.log.LastIndex()
	for _, peer := range peers {
		s.nextIndex[peer] = lastLogIndex + 1
		s.matchIndex[peer] = 0
	}
}

// GetLog returns the log
func (s *State) GetLog() *Log {
	return s.log
}
