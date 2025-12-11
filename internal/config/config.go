package config

import (
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for SkyLock
type Config struct {
	Cluster ClusterConfig `mapstructure:"cluster"`
	Server  ServerConfig  `mapstructure:"server"`
	Raft    RaftConfig    `mapstructure:"raft"`
	Session SessionConfig `mapstructure:"session"`
	Storage StorageConfig `mapstructure:"storage"`
}

// ClusterConfig holds cluster-related configuration
type ClusterConfig struct {
	NodeID string   `mapstructure:"node_id"`
	Peers  []string `mapstructure:"peers"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	GRPCPort int `mapstructure:"grpc_port"`
	HTTPPort int `mapstructure:"http_port"`
}

// RaftConfig holds Raft consensus configuration
type RaftConfig struct {
	ElectionTimeoutMin time.Duration `mapstructure:"election_timeout_min"`
	ElectionTimeoutMax time.Duration `mapstructure:"election_timeout_max"`
	HeartbeatInterval  time.Duration `mapstructure:"heartbeat_interval"`
}

// SessionConfig holds session management configuration
type SessionConfig struct {
	DefaultTTL time.Duration `mapstructure:"default_ttl"`
	MaxTTL     time.Duration `mapstructure:"max_ttl"`
}

// StorageConfig holds storage configuration
type StorageConfig struct {
	DataDir          string        `mapstructure:"data_dir"`
	WALEnabled       bool          `mapstructure:"wal_enabled"`
	SnapshotInterval time.Duration `mapstructure:"snapshot_interval"`
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Cluster: ClusterConfig{
			NodeID: "node-1",
			Peers:  []string{},
		},
		Server: ServerConfig{
			GRPCPort: 5001,
			HTTPPort: 8080,
		},
		Raft: RaftConfig{
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  50 * time.Millisecond,
		},
		Session: SessionConfig{
			DefaultTTL: 10 * time.Second,
			MaxTTL:     60 * time.Second,
		},
		Storage: StorageConfig{
			DataDir:          "./data",
			WALEnabled:       false, // Start with in-memory
			SnapshotInterval: 5 * time.Minute,
		},
	}
}

// Load loads configuration from a file
func Load(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	if configPath != "" {
		viper.SetConfigFile(configPath)
		if err := viper.ReadInConfig(); err != nil {
			return nil, err
		}
		if err := viper.Unmarshal(cfg); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}
