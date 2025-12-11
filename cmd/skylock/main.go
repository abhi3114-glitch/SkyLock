package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/skylock/internal/api"
	"github.com/skylock/internal/config"
	"github.com/skylock/internal/lock"
	"github.com/skylock/internal/raft"
	"github.com/skylock/internal/session"
	"github.com/skylock/internal/store"
	"github.com/skylock/internal/transport"
	"github.com/skylock/internal/watch"
)

func main() {
	// Parse flags
	configFile := flag.String("config", "", "Path to configuration file")
	nodeID := flag.String("node-id", "", "Node ID (overrides config)")
	grpcPort := flag.Int("grpc-port", 0, "gRPC port (overrides config)")
	httpPort := flag.Int("http-port", 0, "HTTP port (overrides config)")
	peers := flag.String("peers", "", "Comma-separated list of peer addresses")
	flag.Parse()

	// Setup logger
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	logger = logger.Level(zerolog.InfoLevel)

	// Load configuration
	cfg, err := config.Load(*configFile)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to load configuration")
	}

	// Apply command line overrides
	if *nodeID != "" {
		cfg.Cluster.NodeID = *nodeID
	}
	if *grpcPort != 0 {
		cfg.Server.GRPCPort = *grpcPort
	}
	if *httpPort != 0 {
		cfg.Server.HTTPPort = *httpPort
	}
	if *peers != "" {
		cfg.Cluster.Peers = splitPeers(*peers)
	}

	logger.Info().
		Str("nodeID", cfg.Cluster.NodeID).
		Int("grpcPort", cfg.Server.GRPCPort).
		Int("httpPort", cfg.Server.HTTPPort).
		Strs("peers", cfg.Cluster.Peers).
		Msg("Starting SkyLock")

	// Create components
	grpcTransport := transport.NewGRPCTransport(logger)

	raftNode := raft.NewNode(
		cfg.Cluster.NodeID,
		cfg.Cluster.Peers,
		grpcTransport,
		cfg.Raft,
		logger,
	)
	grpcTransport.SetRaftNode(raftNode)

	sessionMgr := session.NewManager(cfg.Session.DefaultTTL, cfg.Session.MaxTTL, logger)
	dataStore := store.NewStore(logger)
	watchMgr := watch.NewManager(logger)
	lockMgr := lock.NewManager(dataStore, logger)

	// Wire up session expiration to ephemeral node cleanup
	sessionMgr.SetOnExpire(func(sess *session.Session) {
		logger.Info().
			Str("sessionID", sess.ID).
			Int("ephemeralKeys", len(sess.EphemeralKeys)).
			Msg("Session expired, cleaning up ephemeral nodes")
		dataStore.DeleteEphemerals(sess.ID)
	})

	// Wire up store changes to watch notifications
	dataStore.SetWatchCallbacks(
		func(path string, node *store.Node) {
			watchMgr.TriggerCreate(path, node.Data, node.Version)
		},
		func(path string, node *store.Node) {
			watchMgr.TriggerDataChange(path, node.Data, node.Version)
		},
		func(path string) {
			watchMgr.TriggerDelete(path)
		},
	)

	// Create API server
	apiServer := api.NewServer(raftNode, sessionMgr, dataStore, lockMgr, watchMgr, logger)

	// Start gRPC transport
	grpcAddr := formatAddr(cfg.Server.GRPCPort)
	if err := grpcTransport.Start(grpcAddr); err != nil {
		logger.Fatal().Err(err).Msg("Failed to start gRPC transport")
	}

	// Start Raft node
	raftNode.Start()

	// Start session manager
	sessionMgr.Start()

	// Start HTTP API server
	httpAddr := formatAddr(cfg.Server.HTTPPort)
	go func() {
		if err := apiServer.Start(httpAddr); err != nil {
			logger.Error().Err(err).Msg("HTTP server error")
		}
	}()

	logger.Info().Msg("SkyLock started successfully")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info().Msg("Shutting down SkyLock")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10)
	defer cancel()

	apiServer.Stop(ctx)
	raftNode.Stop()
	sessionMgr.Stop()
	grpcTransport.Stop()

	logger.Info().Msg("SkyLock stopped")
}

func splitPeers(peers string) []string {
	if peers == "" {
		return nil
	}
	result := make([]string, 0)
	current := ""
	for _, c := range peers {
		if c == ',' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func formatAddr(port int) string {
	return ":" + itoa(port)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	result := ""
	for n > 0 {
		result = string('0'+byte(n%10)) + result
		n /= 10
	}
	return result
}
