package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"animasola/internal/config"
	"animasola/internal/pubsub"
	"animasola/internal/server"
	"animasola/internal/store"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to config yaml")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := os.MkdirAll("./data", 0o755); err != nil {
		log.Fatalf("mkdir data: %v", err)
	}

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer st.Close()

	if err := st.Migrate("./migrations"); err != nil {
		log.Fatalf("db migrate: %v", err)
	}

	broker := pubsub.New[pubsub.Event]()

	mainSrv, err := server.New(cfg, st, broker, server.ModeMain)
	if err != nil {
		log.Fatalf("ssh main server: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- mainSrv.ListenAndServe()
	}()

	log.Printf("ssh main listening on %s:%d", cfg.Host, cfg.Port)

	var regSrv interface {
		ListenAndServe() error
		Close() error
	}
	if cfg.RegisterPort != 0 && cfg.RegisterPort != cfg.Port {
		if cfg.RegisterDomain == "" {
			log.Fatalf("register_domain is required when register_port is enabled")
		}
		s, err := server.New(cfg, st, nil, server.ModeRegister)
		if err != nil {
			log.Fatalf("ssh register server: %v", err)
		}
		regSrv = s
		go func() {
			errCh <- s.ListenAndServe()
		}()
		log.Printf("ssh register listening on %s:%d", cfg.Host, cfg.RegisterPort)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("signal: %s; shutting down", sig)
		if err := mainSrv.Close(); err != nil {
			log.Printf("close: %v", err)
		}
		if regSrv != nil {
			if err := regSrv.Close(); err != nil {
				log.Printf("close: %v", err)
			}
		}
	case err := <-errCh:
		log.Fatalf("listen: %v", err)
	}

	fmt.Println("bye")
}
