package transport

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/rs/zerolog"
	"github.com/skylock/internal/raft"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GRPCTransport implements the raft.Transport interface using gRPC
type GRPCTransport struct {
	mu          sync.RWMutex
	server      *grpc.Server
	listener    net.Listener
	connections map[string]*grpc.ClientConn
	logger      zerolog.Logger

	// Handler for incoming RPC requests
	raftNode *raft.Node
}

// NewGRPCTransport creates a new gRPC transport
func NewGRPCTransport(logger zerolog.Logger) *GRPCTransport {
	return &GRPCTransport{
		connections: make(map[string]*grpc.ClientConn),
		logger:      logger.With().Str("component", "transport").Logger(),
	}
}

// SetRaftNode sets the Raft node for handling incoming requests
func (t *GRPCTransport) SetRaftNode(node *raft.Node) {
	t.raftNode = node
}

// Start starts the gRPC server
func (t *GRPCTransport) Start(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	t.listener = listener

	t.server = grpc.NewServer()
	// Register Raft internal service
	RegisterRaftInternalServer(t.server, &raftInternalServer{transport: t})

	go func() {
		if err := t.server.Serve(listener); err != nil {
			t.logger.Error().Err(err).Msg("gRPC server error")
		}
	}()

	t.logger.Info().Str("address", address).Msg("gRPC transport started")
	return nil
}

// Stop stops the gRPC server
func (t *GRPCTransport) Stop() {
	if t.server != nil {
		t.server.GracefulStop()
	}
	if t.listener != nil {
		t.listener.Close()
	}

	t.mu.Lock()
	for _, conn := range t.connections {
		conn.Close()
	}
	t.connections = make(map[string]*grpc.ClientConn)
	t.mu.Unlock()
}

// getConnection gets or creates a connection to a peer
func (t *GRPCTransport) getConnection(target string) (*grpc.ClientConn, error) {
	t.mu.RLock()
	conn, ok := t.connections[target]
	t.mu.RUnlock()

	if ok {
		return conn, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Double-check after acquiring write lock
	if conn, ok := t.connections[target]; ok {
		return conn, nil
	}

	// Create new connection
	conn, err := grpc.Dial(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", target, err)
	}

	t.connections[target] = conn
	return conn, nil
}

// RequestVote sends a RequestVote RPC to a peer
func (t *GRPCTransport) RequestVote(ctx context.Context, target string, req *raft.VoteRequest) (*raft.VoteResponse, error) {
	conn, err := t.getConnection(target)
	if err != nil {
		return nil, err
	}

	client := NewRaftInternalClient(conn)
	resp, err := client.RequestVote(ctx, &VoteRequest{
		Term:         req.Term,
		CandidateId:  req.CandidateID,
		LastLogIndex: req.LastLogIndex,
		LastLogTerm:  req.LastLogTerm,
	})
	if err != nil {
		return nil, err
	}

	return &raft.VoteResponse{
		Term:        resp.Term,
		VoteGranted: resp.VoteGranted,
	}, nil
}

// AppendEntries sends an AppendEntries RPC to a peer
func (t *GRPCTransport) AppendEntries(ctx context.Context, target string, req *raft.AppendEntriesRequest) (*raft.AppendEntriesResponse, error) {
	conn, err := t.getConnection(target)
	if err != nil {
		return nil, err
	}

	client := NewRaftInternalClient(conn)

	// Convert log entries
	entries := make([]*LogEntry, len(req.Entries))
	for i, e := range req.Entries {
		entries[i] = &LogEntry{
			Index:   e.Index,
			Term:    e.Term,
			Type:    int32(e.Type),
			Command: e.Command,
		}
	}

	resp, err := client.AppendEntries(ctx, &AppendEntriesRequest{
		Term:         req.Term,
		LeaderId:     req.LeaderID,
		PrevLogIndex: req.PrevLogIndex,
		PrevLogTerm:  req.PrevLogTerm,
		Entries:      entries,
		LeaderCommit: req.LeaderCommit,
	})
	if err != nil {
		return nil, err
	}

	return &raft.AppendEntriesResponse{
		Term:    resp.Term,
		Success: resp.Success,
	}, nil
}

// raftInternalServer implements the RaftInternal gRPC service
type raftInternalServer struct {
	UnimplementedRaftInternalServer
	transport *GRPCTransport
}

func (s *raftInternalServer) RequestVote(ctx context.Context, req *VoteRequest) (*VoteResponse, error) {
	if s.transport.raftNode == nil {
		return nil, fmt.Errorf("raft node not initialized")
	}

	resp := s.transport.raftNode.HandleRequestVote(&raft.VoteRequest{
		Term:         req.Term,
		CandidateID:  req.CandidateId,
		LastLogIndex: req.LastLogIndex,
		LastLogTerm:  req.LastLogTerm,
	})

	return &VoteResponse{
		Term:        resp.Term,
		VoteGranted: resp.VoteGranted,
	}, nil
}

func (s *raftInternalServer) AppendEntries(ctx context.Context, req *AppendEntriesRequest) (*AppendEntriesResponse, error) {
	if s.transport.raftNode == nil {
		return nil, fmt.Errorf("raft node not initialized")
	}

	// Convert log entries
	entries := make([]raft.LogEntry, len(req.Entries))
	for i, e := range req.Entries {
		entries[i] = raft.LogEntry{
			Index:   e.Index,
			Term:    e.Term,
			Type:    raft.LogEntryType(e.Type),
			Command: e.Command,
		}
	}

	resp := s.transport.raftNode.HandleAppendEntries(&raft.AppendEntriesRequest{
		Term:         req.Term,
		LeaderID:     req.LeaderId,
		PrevLogIndex: req.PrevLogIndex,
		PrevLogTerm:  req.PrevLogTerm,
		Entries:      entries,
		LeaderCommit: req.LeaderCommit,
	})

	return &AppendEntriesResponse{
		Term:    resp.Term,
		Success: resp.Success,
	}, nil
}
