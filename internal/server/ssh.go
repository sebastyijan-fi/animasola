package server

import (
	"context"
	"fmt"
	"os"
	"time"

	"animasola/internal/auth"
	"animasola/internal/config"
	"animasola/internal/pubsub"
	"animasola/internal/store"
	"animasola/internal/tui"

	gossh "golang.org/x/crypto/ssh"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
)

type Mode int

const (
	ModeMain Mode = iota
	ModeRegister
)

func New(cfg config.Config, st *store.Store, broker *pubsub.Broker[pubsub.Event], mode Mode) (*ssh.Server, error) {
	hostKeyPath := cfg.HostKeyPath
	if err := ensureHostKey(hostKeyPath); err != nil {
		return nil, err
	}

	port := cfg.Port
	if mode == ModeRegister {
		port = cfg.RegisterPort
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, port)

	s, err := wish.NewServer(
		wish.WithAddress(addr),
		wish.WithHostKeyPath(hostKeyPath),
		// Require public key authentication so identity is always an SSH key.
		// For v1 we allow any key; registration/allowlisting can be layered on later.
		wish.WithPublicKeyAuth(func(_ ssh.Context, _ ssh.PublicKey) bool { return true }),
		wish.WithMiddleware(
			func(next ssh.Handler) ssh.Handler {
				return func(sesh ssh.Session) {
					pk := sesh.PublicKey()
					if pk == nil {
						wish.WriteString(sesh, "public key required\n")
						_ = sesh.Exit(1)
						return
					}

					fp := auth.Fingerprint(pk)
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()

					u, err := st.GetUserByFingerprint(ctx, fp)
					if err != nil {
						wish.WriteString(sesh, "Something went wrong. Try again.\n")
						_ = sesh.Exit(1)
						return
					}

					switch mode {
					case ModeMain:
						if u == nil {
							msg := fmt.Sprintf("No account found for this key. Register at: ssh yourname@%s\n", cfg.RegisterDomain)
							wish.WriteString(sesh, msg)
							_ = sesh.Exit(0)
							return
						}

						var appStore tui.Store = st
						if broker != nil {
							appStore = &storeWithEvents{st: st, broker: broker}
						}
						m := tui.NewApp("animasola", appStore, broker, u)
						p := tea.NewProgram(
							m,
							tea.WithInput(sesh),
							tea.WithOutput(sesh),
							tea.WithAltScreen(),
							tea.WithMouseCellMotion(),
						)
						if _, err := p.Run(); err != nil {
							wish.WriteString(sesh, "Connection lost.\n")
							_ = sesh.Exit(1)
							return
						}
					case ModeRegister:
						if u != nil {
							wish.WriteString(sesh, fmt.Sprintf("This key is already registered as %s\n", u.Username))
							_ = sesh.Exit(0)
							return
						}

						username := sesh.User()
						if err := auth.ValidateUsername(username); err != nil {
							wish.WriteString(sesh, err.Error()+"\n")
							_ = sesh.Exit(0)
							return
						}

						publicKey := string(gossh.MarshalAuthorizedKey(pk))
						created, err := st.CreateUser(ctx, username, fp, publicKey)
						if err != nil {
							wish.WriteString(sesh, "Username taken\n")
							_ = sesh.Exit(0)
							return
						}

						wish.WriteString(sesh, fmt.Sprintf("Welcome to Animasola, %s! Connect anytime: ssh %s\n", created.Username, cfg.Domain))
						_ = sesh.Exit(0)
						return
					default:
						wish.WriteString(sesh, "server misconfigured\n")
						_ = sesh.Exit(1)
						return
					}

					next(sesh)
				}
			},
		),
	)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func ensureHostKey(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	// Wish will generate a host key at the path if it doesn't exist, but only when starting.
	// Creating the directory is the main requirement.
	return nil
}

func dirOf(p string) string {
	// Minimal dirname without importing filepath; path is expected to be Unix-ish in config.
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
