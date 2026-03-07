package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
)

func TestBootstrapAppCmdRemovesNewLocalProfileWhenRegistryRejects(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/challenge":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"nonce":"nonce"}`))
		case "/v1/profiles/register":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"username_taken","error":"profile name is already taken"}`))
		default:
			t.Fatalf("unexpected registry path: %s", r.URL.Path)
		}
	}))
	defer registry.Close()

	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("ANIMASOLA_REGISTRY_URL", registry.URL)

	model := &RootModel{ctx: context.Background()}
	msg := model.bootstrapAppCmd("takenname")()
	if _, ok := msg.(BootstrapErrMsg); !ok {
		t.Fatalf("expected bootstrap error, got %T", msg)
	}

	configDir := filepath.Join(configRoot, "animasola", "takenname")
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("expected rejected profile directory to be removed, stat err=%v", err)
	}
}

func TestBootstrapAppCmdPreservesExistingLocalProfileWhenRegistryRejects(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/challenge":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"nonce":"nonce"}`))
		case "/v1/profiles/register":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"username_taken","error":"profile name is already taken"}`))
		default:
			t.Fatalf("unexpected registry path: %s", r.URL.Path)
		}
	}))
	defer registry.Close()

	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("ANIMASOLA_REGISTRY_URL", registry.URL)

	if _, err := keys.GetOrGenerateKey("existingname"); err != nil {
		t.Fatalf("precreate key: %v", err)
	}

	model := &RootModel{ctx: context.Background()}
	msg := model.bootstrapAppCmd("existingname")()
	if _, ok := msg.(BootstrapErrMsg); !ok {
		t.Fatalf("expected bootstrap error, got %T", msg)
	}

	if !keys.HasKey("existingname") {
		t.Fatalf("expected existing local key to remain after registry rejection")
	}
}
