package config

import (
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	RegisterPort int    `yaml:"register_port"`

	Domain         string `yaml:"domain"`
	RegisterDomain string `yaml:"register_domain"`

	DatabasePath string `yaml:"database_path"`
	HostKeyPath  string `yaml:"host_key_path"`

	MaxMessageLength      int `yaml:"max_message_length"`
	MaxCommunitiesPerUser int `yaml:"max_communities_per_user"`
	MaxRoomsPerCommunity  int `yaml:"max_rooms_per_community"`
	MessagesPerPage       int `yaml:"messages_per_page"`
}

func Load(path string) (Config, error) {
	// Defaults for local dev.
	var cfg Config
	cfg.Host = "0.0.0.0"
	cfg.Port = 23234
	cfg.RegisterPort = 23235
	cfg.Domain = "animasola.org"
	cfg.RegisterDomain = "register.animasola.org"
	cfg.DatabasePath = "./data/animasola.db"
	cfg.HostKeyPath = "./data/host_key"
	cfg.MaxMessageLength = 5000
	cfg.MaxCommunitiesPerUser = 5
	cfg.MaxRoomsPerCommunity = 20
	cfg.MessagesPerPage = 30

	if path == "" {
		return cfg, nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.DatabasePath == "" {
		return Config{}, errors.New("database_path is required")
	}
	if cfg.Port == 0 {
		return Config{}, errors.New("port is required")
	}
	if cfg.HostKeyPath == "" {
		return Config{}, errors.New("host_key_path is required")
	}
	return cfg, nil
}
