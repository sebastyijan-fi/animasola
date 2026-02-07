package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") error: %v", err)
	}
	if cfg.Port == 0 {
		t.Fatalf("expected default port to be set")
	}
	if cfg.DatabasePath == "" {
		t.Fatalf("expected default database_path to be set")
	}
	if cfg.HostKeyPath == "" {
		t.Fatalf("expected default host_key_path to be set")
	}
}

func TestLoad_FromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(`
host: 127.0.0.1
port: 2222
register_port: 2223
domain: example.org
register_domain: register.example.org
database_path: ./data/x.db
host_key_path: ./data/host_key
max_message_length: 123
max_communities_per_user: 4
max_rooms_per_community: 5
messages_per_page: 6
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load(file) error: %v", err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 2222 || cfg.RegisterPort != 2223 {
		t.Fatalf("unexpected host/port: %#v", cfg)
	}
	if cfg.Domain != "example.org" || cfg.RegisterDomain != "register.example.org" {
		t.Fatalf("unexpected domains: %#v", cfg)
	}
	if cfg.DatabasePath != "./data/x.db" || cfg.HostKeyPath != "./data/host_key" {
		t.Fatalf("unexpected paths: %#v", cfg)
	}
	if cfg.MaxMessageLength != 123 || cfg.MessagesPerPage != 6 {
		t.Fatalf("unexpected limits: %#v", cfg)
	}
}
