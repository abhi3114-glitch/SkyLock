package raft

import (
	"testing"
)

func TestNewLog(t *testing.T) {
	log := NewLog()

	if log.Length() != 0 {
		t.Errorf("expected empty log, got length %d", log.Length())
	}

	if log.LastIndex() != 0 {
		t.Errorf("expected last index 0, got %d", log.LastIndex())
	}

	if log.LastTerm() != 0 {
		t.Errorf("expected last term 0, got %d", log.LastTerm())
	}
}

func TestLogAppend(t *testing.T) {
	log := NewLog()

	// Append first entry
	index := log.Append(LogEntry{Term: 1, Type: LogEntryCommand, Command: []byte("cmd1")})
	if index != 1 {
		t.Errorf("expected index 1, got %d", index)
	}

	// Append second entry
	index = log.Append(LogEntry{Term: 1, Type: LogEntryCommand, Command: []byte("cmd2")})
	if index != 2 {
		t.Errorf("expected index 2, got %d", index)
	}

	if log.Length() != 2 {
		t.Errorf("expected log length 2, got %d", log.Length())
	}

	if log.LastIndex() != 2 {
		t.Errorf("expected last index 2, got %d", log.LastIndex())
	}
}

func TestLogGet(t *testing.T) {
	log := NewLog()

	log.Append(LogEntry{Term: 1, Type: LogEntryCommand, Command: []byte("cmd1")})
	log.Append(LogEntry{Term: 2, Type: LogEntryCommand, Command: []byte("cmd2")})

	// Get existing entry
	entry := log.Get(1)
	if entry == nil {
		t.Fatal("expected entry at index 1")
	}
	if entry.Term != 1 {
		t.Errorf("expected term 1, got %d", entry.Term)
	}
	if string(entry.Command) != "cmd1" {
		t.Errorf("expected command cmd1, got %s", string(entry.Command))
	}

	// Get non-existing entry
	entry = log.Get(0)
	if entry != nil {
		t.Error("expected nil for index 0")
	}

	entry = log.Get(10)
	if entry != nil {
		t.Error("expected nil for index 10")
	}
}

func TestLogTermAt(t *testing.T) {
	log := NewLog()

	log.Append(LogEntry{Term: 1, Type: LogEntryCommand, Command: []byte("cmd1")})
	log.Append(LogEntry{Term: 3, Type: LogEntryCommand, Command: []byte("cmd2")})
	log.Append(LogEntry{Term: 3, Type: LogEntryCommand, Command: []byte("cmd3")})

	if log.TermAt(1) != 1 {
		t.Errorf("expected term 1 at index 1, got %d", log.TermAt(1))
	}

	if log.TermAt(2) != 3 {
		t.Errorf("expected term 3 at index 2, got %d", log.TermAt(2))
	}

	if log.TermAt(0) != 0 {
		t.Errorf("expected term 0 at index 0, got %d", log.TermAt(0))
	}
}

func TestLogGetRange(t *testing.T) {
	log := NewLog()

	for i := 1; i <= 5; i++ {
		log.Append(LogEntry{Term: uint64(i), Type: LogEntryCommand, Command: []byte("cmd")})
	}

	// Get range
	entries := log.GetRange(2, 4)
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Term != 2 {
		t.Errorf("expected first entry term 2, got %d", entries[0].Term)
	}
	if entries[2].Term != 4 {
		t.Errorf("expected last entry term 4, got %d", entries[2].Term)
	}

	// Invalid range
	entries = log.GetRange(0, 2)
	if entries != nil {
		t.Error("expected nil for invalid range starting at 0")
	}
}

func TestLogEntriesAfter(t *testing.T) {
	log := NewLog()

	for i := 1; i <= 5; i++ {
		log.Append(LogEntry{Term: uint64(i), Type: LogEntryCommand, Command: []byte("cmd")})
	}

	entries := log.EntriesAfter(3)
	if len(entries) != 2 {
		t.Errorf("expected 2 entries after index 3, got %d", len(entries))
	}
	if entries[0].Index != 4 {
		t.Errorf("expected first entry index 4, got %d", entries[0].Index)
	}
}
