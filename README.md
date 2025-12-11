# SkyLock

A distributed lock service inspired by Apache Zookeeper, built from scratch in Go. SkyLock provides coordination primitives for distributed systems including leader election, distributed locks, session management, and ephemeral nodes.

## Overview

SkyLock implements a simplified version of the Zookeeper coordination service, demonstrating core distributed systems concepts:

- Raft-based consensus for leader election and state replication
- Distributed locking with fair FIFO ordering
- Session-based client connections with heartbeat-driven liveness detection
- Ephemeral nodes that automatically clean up when clients disconnect
- Watch notifications for state changes

## Architecture

```
                        SkyLock Cluster
    +--------------------------------------------------+
    |                                                  |
    |   Node 1          Node 2          Node 3        |
    |   (Leader)        (Follower)      (Follower)    |
    |      |                |               |         |
    |      +--------+-------+-------+-------+         |
    |               |                                 |
    |         Raft Consensus                          |
    |                                                 |
    +--------------------------------------------------+
                         |
            +------------+-------------+
            |            |             |
         Session      Store         Lock
         Manager   (Ephemeral)     Manager
            |            |             |
            +------------+-------------+
                         |
                    REST API
                         |
                   Client Apps
```

## Features

### Leader Election
The cluster uses a Raft-lite implementation for consensus. Nodes start as followers and trigger elections when heartbeats timeout. A candidate needs majority votes to become leader. The leader maintains authority through periodic heartbeats.

### Distributed Locks
Locks use the Zookeeper-style algorithm with ephemeral sequential nodes:
1. Client creates an ephemeral sequential node under the lock path
2. Client checks if its node has the lowest sequence number
3. If yes, lock is acquired; otherwise, client waits for the predecessor to release
4. Locks are automatically released when the owning session expires

### Sessions and Heartbeats
Clients establish sessions with configurable TTLs (default 10 seconds). The server tracks heartbeats and expires sessions that fail to send timely keepalives. All ephemeral nodes owned by an expired session are automatically deleted.

### Ephemeral Nodes
Nodes can be marked as ephemeral, tying their lifecycle to a client session. When the session ends (either explicitly or via timeout), all associated ephemeral nodes are removed. This enables use cases like service discovery and distributed locks.

### Hierarchical Data Store
Data is organized in a tree structure similar to a filesystem. Nodes can have data payloads and children. Sequential nodes automatically receive monotonically increasing sequence numbers for ordering.

## Getting Started

### Prerequisites
- Go 1.21 or later

### Building

```bash
cd c:\PROJECTS\SkyLock
go mod tidy
go build -o skylock.exe ./cmd/skylock
go build -o skylockctl.exe ./cmd/skylockctl
```

### Running a Single Node

```bash
./skylock.exe --node-id node-1 --grpc-port 5001 --http-port 8080
```

### Running a 3-Node Cluster

Using the provided script:

```powershell
.\start_cluster.ps1
```

Or manually in separate terminals:

```bash
./skylock.exe --config configs/node1.yaml
./skylock.exe --config configs/node2.yaml
./skylock.exe --config configs/node3.yaml
```

## API Reference

### Session Management

Create a session:
```bash
curl -X POST http://localhost:8080/v1/session \
  -H "Content-Type: application/json" \
  -d '{"client_id": "my-client", "ttl_seconds": 30}'
```

Send heartbeat:
```bash
curl -X POST http://localhost:8080/v1/session/{session_id}/heartbeat
```

Close session:
```bash
curl -X DELETE http://localhost:8080/v1/session/{session_id}
```

### Node Operations

Create a node:
```bash
curl -X PUT http://localhost:8080/v1/nodes/my/path \
  -H "Content-Type: application/json" \
  -d '{"data": "hello", "ephemeral": false}'
```

Get a node:
```bash
curl http://localhost:8080/v1/nodes/my/path
```

Update a node:
```bash
curl -X POST http://localhost:8080/v1/nodes/my/path \
  -H "Content-Type: application/json" \
  -d '{"data": "updated"}'
```

Delete a node:
```bash
curl -X DELETE http://localhost:8080/v1/nodes/my/path
```

Get children:
```bash
curl http://localhost:8080/v1/children/my/path
```

### Distributed Locks

Acquire a lock:
```bash
curl -X POST http://localhost:8080/v1/locks/my-lock \
  -H "Content-Type: application/json" \
  -d '{"owner": "client-1", "session_id": "...", "timeout_ms": 30000}'
```

Release a lock:
```bash
curl -X DELETE http://localhost:8080/v1/locks/my-lock \
  -H "Content-Type: application/json" \
  -d '{"lock_id": "..."}'
```

Get lock info:
```bash
curl http://localhost:8080/v1/locks/my-lock
```

### Cluster Operations

Get current leader:
```bash
curl http://localhost:8080/v1/cluster/leader
```

Get cluster status:
```bash
curl http://localhost:8080/v1/cluster/status
```

## CLI Tool

```bash
# Cluster operations
./skylockctl status
./skylockctl leader

# Session management
./skylockctl session create my-client
./skylockctl session heartbeat {session-id}
./skylockctl session close {session-id}

# Node operations
./skylockctl create /my/path "some data"
./skylockctl get /my/path
./skylockctl set /my/path "new data"
./skylockctl delete /my/path
./skylockctl children /my/path

# Lock operations
./skylockctl lock my-lock {session-id}
./skylockctl unlock my-lock {lock-id}
./skylockctl lockinfo my-lock
```

## Go Client SDK

```go
package main

import (
    "context"
    "log"
    "os"

    "github.com/rs/zerolog"
    "github.com/skylock/pkg/client"
)

func main() {
    logger := zerolog.New(os.Stdout)
    
    // Create and connect client
    c := client.NewClient([]string{"http://localhost:8080"}, logger)
    if err := c.Connect(context.Background(), "my-app"); err != nil {
        log.Fatal(err)
    }
    defer c.Close()
    
    // Acquire a distributed lock
    lock, err := c.Lock(context.Background(), "my-resource")
    if err != nil {
        log.Fatal(err)
    }
    
    // Do work while holding the lock
    // ...
    
    // Release the lock
    lock.Unlock(context.Background())
    
    // Create an ephemeral node for service discovery
    c.Create(context.Background(), "/services/my-app", []byte("running"), true)
}
```

## Project Structure

```
SkyLock/
├── cmd/
│   ├── skylock/              # Server binary
│   └── skylockctl/           # CLI tool
├── internal/
│   ├── api/                  # REST API server
│   ├── config/               # Configuration management
│   ├── lock/                 # Distributed lock manager
│   ├── raft/                 # Raft consensus implementation
│   │   ├── state.go          # Node state machine
│   │   ├── log.go            # Append-only log
│   │   ├── node.go           # Main raft node
│   │   └── errors.go         # Error definitions
│   ├── session/              # Session manager
│   ├── store/                # Hierarchical data store
│   ├── transport/            # gRPC transport layer
│   └── watch/                # Watch notification system
├── pkg/
│   └── client/               # Go client SDK
├── configs/                  # Node configuration files
├── start_cluster.ps1         # Cluster startup script
└── README.md
```

## Configuration

Configuration files use YAML format:

```yaml
cluster:
  node_id: "node-1"
  peers:
    - "localhost:5002"
    - "localhost:5003"

server:
  grpc_port: 5001
  http_port: 8081

raft:
  election_timeout_min: 150ms
  election_timeout_max: 300ms
  heartbeat_interval: 50ms

session:
  default_ttl: 10s
  max_ttl: 60s

storage:
  data_dir: "./data/node1"
  wal_enabled: false
  snapshot_interval: 5m
```

## Testing

Run unit tests:
```bash
go test ./... -v
```

Run with race detector:
```bash
go test ./... -race
```

## Technical Details

### Raft Implementation
- Election timeout: 150-300ms (randomized to prevent split votes)
- Heartbeat interval: 50ms
- Majority quorum required for leader election
- Log replication with term-based consistency

### Lock Algorithm
Based on the Zookeeper recipe for distributed locks:
1. Create ephemeral sequential node: /locks/{name}/lock-{seq}
2. Get all children of /locks/{name}
3. If our node has lowest sequence, lock acquired
4. Otherwise, set watch on predecessor and wait
5. Lock automatically released when session expires

### Session Management
- Sessions identified by UUID
- TTL enforced via background expiration check (1s interval)
- Heartbeat resets TTL countdown
- All ephemeral nodes deleted on session expiry

## License

MIT License
