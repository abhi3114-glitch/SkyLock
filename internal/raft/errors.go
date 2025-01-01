package raft

import "errors"

var (
	// ErrNotLeader is returned when an operation requires leader but node is not leader
	ErrNotLeader = errors.New("not the leader")

	// ErrLeaderUnknown is returned when the leader is not known
	ErrLeaderUnknown = errors.New("leader unknown")

	// ErrTimeout is returned when an operation times out
	ErrTimeout = errors.New("operation timed out")
)
