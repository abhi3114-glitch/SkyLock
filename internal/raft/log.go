package raft

import (
	"sync"
)

// LogEntryType represents the type of log entry
type LogEntryType int

const (
	LogEntryCommand LogEntryType = iota
	LogEntryConfig
	LogEntryNoop
)

// LogEntry represents a single entry in the Raft log
type LogEntry struct {
	Index   uint64
	Term    uint64
	Type    LogEntryType
	Command []byte
}

// Log is an append-only log of entries
type Log struct {
	mu      sync.RWMutex
	entries []LogEntry
}

// NewLog creates a new empty log
func NewLog() *Log {
	return &Log{
		entries: make([]LogEntry, 0),
	}
}

// Append adds a new entry to the log
func (l *Log) Append(entry LogEntry) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry.Index = uint64(len(l.entries) + 1)
	l.entries = append(l.entries, entry)
	return entry.Index
}

// AppendEntries appends multiple entries, potentially overwriting conflicting entries
func (l *Log) AppendEntries(prevIndex uint64, entries []LogEntry) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	// If prevIndex is beyond our log, we can't append
	if prevIndex > uint64(len(l.entries)) {
		return false
	}

	// Truncate conflicting entries
	l.entries = l.entries[:prevIndex]

	// Append new entries
	for i, entry := range entries {
		entry.Index = prevIndex + uint64(i) + 1
		l.entries = append(l.entries, entry)
	}

	return true
}

// Get returns the entry at the given index (1-indexed)
func (l *Log) Get(index uint64) *LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if index == 0 || index > uint64(len(l.entries)) {
		return nil
	}
	entry := l.entries[index-1]
	return &entry
}

// GetRange returns entries in the range [start, end] (inclusive, 1-indexed)
func (l *Log) GetRange(start, end uint64) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if start == 0 || start > end || start > uint64(len(l.entries)) {
		return nil
	}

	if end > uint64(len(l.entries)) {
		end = uint64(len(l.entries))
	}

	result := make([]LogEntry, end-start+1)
	copy(result, l.entries[start-1:end])
	return result
}

// LastIndex returns the index of the last entry
func (l *Log) LastIndex() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return uint64(len(l.entries))
}

// LastTerm returns the term of the last entry
func (l *Log) LastTerm() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.entries) == 0 {
		return 0
	}
	return l.entries[len(l.entries)-1].Term
}

// TermAt returns the term at the given index
func (l *Log) TermAt(index uint64) uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if index == 0 || index > uint64(len(l.entries)) {
		return 0
	}
	return l.entries[index-1].Term
}

// Length returns the number of entries in the log
func (l *Log) Length() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}

// EntriesAfter returns all entries after the given index
func (l *Log) EntriesAfter(index uint64) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if index >= uint64(len(l.entries)) {
		return nil
	}

	result := make([]LogEntry, len(l.entries)-int(index))
	copy(result, l.entries[index:])
	return result
}
