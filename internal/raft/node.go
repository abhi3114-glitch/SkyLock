package raft

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/skylock/internal/config"
)

// Transport defines the interface for Raft RPC communication
type Transport interface {
	RequestVote(ctx context.Context, target string, req *VoteRequest) (*VoteResponse, error)
	AppendEntries(ctx context.Context, target string, req *AppendEntriesRequest) (*AppendEntriesResponse, error)
}

// VoteRequest represents a RequestVote RPC request
type VoteRequest struct {
	Term         uint64
	CandidateID  string
	LastLogIndex uint64
	LastLogTerm  uint64
}

// VoteResponse represents a RequestVote RPC response
type VoteResponse struct {
	Term        uint64
	VoteGranted bool
}

// AppendEntriesRequest represents an AppendEntries RPC request
type AppendEntriesRequest struct {
	Term         uint64
	LeaderID     string
	PrevLogIndex uint64
	PrevLogTerm  uint64
	Entries      []LogEntry
	LeaderCommit uint64
}

// AppendEntriesResponse represents an AppendEntries RPC response
type AppendEntriesResponse struct {
	Term    uint64
	Success bool
}

// Node represents a Raft node
type Node struct {
	mu sync.RWMutex

	id        string
	peers     []string
	state     *State
	transport Transport
	config    config.RaftConfig
	logger    zerolog.Logger

	// Channels
	stopCh   chan struct{}
	applyCh  chan LogEntry
	commitCh chan struct{}

	// Election state
	electionTimer  *time.Timer
	heartbeatTimer *time.Timer

	// State machine apply callback
	applyFunc func(entry LogEntry) error
}

// NewNode creates a new Raft node
func NewNode(id string, peers []string, transport Transport, cfg config.RaftConfig, logger zerolog.Logger) *Node {
	return &Node{
		id:        id,
		peers:     peers,
		state:     NewState(),
		transport: transport,
		config:    cfg,
		logger:    logger.With().Str("component", "raft").Str("node", id).Logger(),
		stopCh:    make(chan struct{}),
		applyCh:   make(chan LogEntry, 100),
		commitCh:  make(chan struct{}, 1),
	}
}

// Start begins the Raft node
func (n *Node) Start() {
	n.logger.Info().Msg("Starting Raft node")
	n.resetElectionTimer()
	go n.run()
	go n.applyLoop()
}

// Stop gracefully stops the Raft node
func (n *Node) Stop() {
	n.logger.Info().Msg("Stopping Raft node")
	close(n.stopCh)
	if n.electionTimer != nil {
		n.electionTimer.Stop()
	}
	if n.heartbeatTimer != nil {
		n.heartbeatTimer.Stop()
	}
}

// run is the main event loop for the Raft node
func (n *Node) run() {
	for {
		select {
		case <-n.stopCh:
			return
		default:
		}

		switch n.state.GetState() {
		case Follower:
			n.runFollower()
		case Candidate:
			n.runCandidate()
		case Leader:
			n.runLeader()
		}
	}
}

// runFollower runs the follower state machine
func (n *Node) runFollower() {
	n.logger.Debug().Msg("Running as follower")

	select {
	case <-n.stopCh:
		return
	case <-n.electionTimer.C:
		n.logger.Info().Msg("Election timeout, becoming candidate")
		n.state.SetState(Candidate)
	}
}

// runCandidate runs the candidate state machine
func (n *Node) runCandidate() {
	n.logger.Info().Msg("Running as candidate")

	term := n.state.BecomeCandidate()
	n.state.SetVotedFor(n.id)
	n.resetElectionTimer()

	// Request votes from all peers
	votesNeeded := (len(n.peers)+1)/2 + 1
	votes := 1 // Vote for self

	ctx, cancel := context.WithTimeout(context.Background(), n.config.ElectionTimeoutMax)
	defer cancel()

	var wg sync.WaitGroup
	var voteMu sync.Mutex

	for _, peer := range n.peers {
		wg.Add(1)
		go func(peer string) {
			defer wg.Done()

			req := &VoteRequest{
				Term:         term,
				CandidateID:  n.id,
				LastLogIndex: n.state.GetLog().LastIndex(),
				LastLogTerm:  n.state.GetLog().LastTerm(),
			}

			resp, err := n.transport.RequestVote(ctx, peer, req)
			if err != nil {
				n.logger.Debug().Err(err).Str("peer", peer).Msg("Failed to request vote")
				return
			}

			voteMu.Lock()
			defer voteMu.Unlock()

			if resp.Term > term {
				n.state.BecomeFollower(resp.Term, "")
				return
			}

			if resp.VoteGranted {
				votes++
				n.logger.Debug().Str("peer", peer).Int("votes", votes).Msg("Received vote")
			}
		}(peer)
	}

	// Wait for votes or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-n.stopCh:
		return
	case <-n.electionTimer.C:
		n.logger.Debug().Msg("Election timeout during candidate phase")
		return
	case <-done:
	}

	voteMu.Lock()
	currentVotes := votes
	voteMu.Unlock()

	if currentVotes >= votesNeeded {
		n.logger.Info().Int("votes", currentVotes).Msg("Won election, becoming leader")
		n.state.BecomeLeader(n.id, n.peers)
	} else {
		n.logger.Debug().Int("votes", currentVotes).Int("needed", votesNeeded).Msg("Failed to win election")
		n.state.SetState(Follower)
		n.resetElectionTimer()
	}
}

// runLeader runs the leader state machine
func (n *Node) runLeader() {
	n.logger.Info().Msg("Running as leader")

	// Send initial empty AppendEntries (heartbeat)
	n.sendHeartbeats()

	// Reset heartbeat timer
	n.heartbeatTimer = time.NewTimer(n.config.HeartbeatInterval)

	for {
		select {
		case <-n.stopCh:
			return
		case <-n.heartbeatTimer.C:
			n.sendHeartbeats()
			n.heartbeatTimer.Reset(n.config.HeartbeatInterval)
		case <-n.commitCh:
			n.updateCommitIndex()
		}

		if n.state.GetState() != Leader {
			if n.heartbeatTimer != nil {
				n.heartbeatTimer.Stop()
			}
			return
		}
	}
}

// sendHeartbeats sends AppendEntries RPCs to all peers
func (n *Node) sendHeartbeats() {
	term := n.state.CurrentTerm()

	for _, peer := range n.peers {
		go func(peer string) {
			ctx, cancel := context.WithTimeout(context.Background(), n.config.HeartbeatInterval)
			defer cancel()

			n.mu.RLock()
			prevLogIndex := n.state.nextIndex[peer] - 1
			n.mu.RUnlock()

			prevLogTerm := n.state.GetLog().TermAt(prevLogIndex)
			entries := n.state.GetLog().EntriesAfter(prevLogIndex)

			req := &AppendEntriesRequest{
				Term:         term,
				LeaderID:     n.id,
				PrevLogIndex: prevLogIndex,
				PrevLogTerm:  prevLogTerm,
				Entries:      entries,
				LeaderCommit: n.state.CommitIndex(),
			}

			resp, err := n.transport.AppendEntries(ctx, peer, req)
			if err != nil {
				n.logger.Debug().Err(err).Str("peer", peer).Msg("Failed to send heartbeat")
				return
			}

			if resp.Term > term {
				n.state.BecomeFollower(resp.Term, "")
				return
			}

			if resp.Success {
				n.mu.Lock()
				if len(entries) > 0 {
					n.state.nextIndex[peer] = entries[len(entries)-1].Index + 1
					n.state.matchIndex[peer] = entries[len(entries)-1].Index
				}
				n.mu.Unlock()

				// Signal to check commit index
				select {
				case n.commitCh <- struct{}{}:
				default:
				}
			} else {
				// Decrement nextIndex and retry
				n.mu.Lock()
				if n.state.nextIndex[peer] > 1 {
					n.state.nextIndex[peer]--
				}
				n.mu.Unlock()
			}
		}(peer)
	}
}

// updateCommitIndex updates the commit index based on matchIndex
func (n *Node) updateCommitIndex() {
	n.mu.RLock()
	matchIndexes := make([]uint64, 0, len(n.peers)+1)
	matchIndexes = append(matchIndexes, n.state.GetLog().LastIndex())
	for _, idx := range n.state.matchIndex {
		matchIndexes = append(matchIndexes, idx)
	}
	n.mu.RUnlock()

	// Sort to find median
	for i := 0; i < len(matchIndexes)-1; i++ {
		for j := i + 1; j < len(matchIndexes); j++ {
			if matchIndexes[i] > matchIndexes[j] {
				matchIndexes[i], matchIndexes[j] = matchIndexes[j], matchIndexes[i]
			}
		}
	}

	majority := len(matchIndexes) / 2
	newCommitIndex := matchIndexes[majority]

	term := n.state.CurrentTerm()
	if newCommitIndex > n.state.CommitIndex() && n.state.GetLog().TermAt(newCommitIndex) == term {
		n.state.SetCommitIndex(newCommitIndex)
		n.logger.Debug().Uint64("commitIndex", newCommitIndex).Msg("Updated commit index")
	}
}

// applyLoop applies committed entries to the state machine
func (n *Node) applyLoop() {
	for {
		select {
		case <-n.stopCh:
			return
		default:
		}

		commitIndex := n.state.CommitIndex()
		lastApplied := n.state.LastApplied()

		if commitIndex > lastApplied {
			for i := lastApplied + 1; i <= commitIndex; i++ {
				entry := n.state.GetLog().Get(i)
				if entry != nil {
					if n.applyFunc != nil {
						if err := n.applyFunc(*entry); err != nil {
							n.logger.Error().Err(err).Uint64("index", i).Msg("Failed to apply entry")
						}
					}
					n.state.SetLastApplied(i)
				}
			}
		}

		time.Sleep(10 * time.Millisecond) // Small delay to prevent busy loop
	}
}

// resetElectionTimer resets the election timer with a random timeout
func (n *Node) resetElectionTimer() {
	if n.electionTimer != nil {
		n.electionTimer.Stop()
	}

	min := n.config.ElectionTimeoutMin
	max := n.config.ElectionTimeoutMax
	timeout := min + time.Duration(rand.Int63n(int64(max-min)))

	n.electionTimer = time.NewTimer(timeout)
}

// HandleRequestVote handles incoming RequestVote RPCs
func (n *Node) HandleRequestVote(req *VoteRequest) *VoteResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	resp := &VoteResponse{
		Term:        n.state.CurrentTerm(),
		VoteGranted: false,
	}

	// If request term is less than current term, reject
	if req.Term < n.state.CurrentTerm() {
		return resp
	}

	// If request term is greater, become follower
	if req.Term > n.state.CurrentTerm() {
		n.state.BecomeFollower(req.Term, "")
		resp.Term = req.Term
	}

	// Check if we can vote for this candidate
	votedFor := n.state.VotedFor()
	if votedFor == "" || votedFor == req.CandidateID {
		// Check if candidate's log is at least as up-to-date
		lastLogIndex := n.state.GetLog().LastIndex()
		lastLogTerm := n.state.GetLog().LastTerm()

		if req.LastLogTerm > lastLogTerm ||
			(req.LastLogTerm == lastLogTerm && req.LastLogIndex >= lastLogIndex) {
			resp.VoteGranted = true
			n.state.SetVotedFor(req.CandidateID)
			n.resetElectionTimer()
			n.logger.Debug().Str("candidate", req.CandidateID).Msg("Granted vote")
		}
	}

	return resp
}

// HandleAppendEntries handles incoming AppendEntries RPCs
func (n *Node) HandleAppendEntries(req *AppendEntriesRequest) *AppendEntriesResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	resp := &AppendEntriesResponse{
		Term:    n.state.CurrentTerm(),
		Success: false,
	}

	// If request term is less than current term, reject
	if req.Term < n.state.CurrentTerm() {
		return resp
	}

	// Reset election timer (valid leader heartbeat)
	n.resetElectionTimer()

	// If request term is greater or equal, recognize leader
	if req.Term >= n.state.CurrentTerm() {
		n.state.BecomeFollower(req.Term, req.LeaderID)
		resp.Term = req.Term
	}

	// Check log consistency
	if req.PrevLogIndex > 0 {
		if n.state.GetLog().TermAt(req.PrevLogIndex) != req.PrevLogTerm {
			return resp
		}
	}

	// Append entries
	if len(req.Entries) > 0 {
		n.state.GetLog().AppendEntries(req.PrevLogIndex, req.Entries)
	}

	// Update commit index
	if req.LeaderCommit > n.state.CommitIndex() {
		lastNewEntry := req.PrevLogIndex
		if len(req.Entries) > 0 {
			lastNewEntry = req.Entries[len(req.Entries)-1].Index
		}
		commitIndex := req.LeaderCommit
		if lastNewEntry < commitIndex {
			commitIndex = lastNewEntry
		}
		n.state.SetCommitIndex(commitIndex)
	}

	resp.Success = true
	return resp
}

// Propose proposes a new command to be replicated
func (n *Node) Propose(command []byte) (uint64, error) {
	if n.state.GetState() != Leader {
		return 0, ErrNotLeader
	}

	entry := LogEntry{
		Term:    n.state.CurrentTerm(),
		Type:    LogEntryCommand,
		Command: command,
	}

	index := n.state.GetLog().Append(entry)
	n.logger.Debug().Uint64("index", index).Msg("Proposed new entry")

	return index, nil
}

// IsLeader returns true if this node is the leader
func (n *Node) IsLeader() bool {
	return n.state.GetState() == Leader
}

// GetLeaderID returns the current leader ID
func (n *Node) GetLeaderID() string {
	return n.state.LeaderID()
}

// SetApplyFunc sets the function to call when entries are committed
func (n *Node) SetApplyFunc(f func(entry LogEntry) error) {
	n.applyFunc = f
}
