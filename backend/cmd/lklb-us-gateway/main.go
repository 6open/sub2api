package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"github.com/Wei-Shaw/sub2api/internal/pkg/edgegateway"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	file := flag.String("config", "", "private gateway configuration")
	flag.Parse()
	raw, err := os.ReadFile(*file)
	if err != nil {
		log.Fatal("cannot read gateway config")
	}
	var cfg struct {
		MainURL   string `json:"main_url"`
		Token     string `json:"token"`
		PublicKey string `json:"public_key"`
		StateDir  string `json:"state_dir"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		log.Fatal("invalid config")
	}
	pub, err := os.ReadFile(cfg.PublicKey)
	if err != nil {
		log.Fatal("missing public key")
	}
	block, _ := pem.Decode(pub)
	if block == nil {
		log.Fatal("invalid public key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		log.Fatal("invalid public key")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		log.Fatal("Ed25519 public key required")
	}
	gateway, err := edgegateway.New(cfg.MainURL, cfg.Token, key, cfg.StateDir)
	if err != nil {
		log.Fatal("gateway initialization failed")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go gateway.RunRecovery(ctx)
	server := &http.Server{Addr: "127.0.0.1:19445", Handler: gateway, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		end, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		_ = server.Shutdown(end)
	}()
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal("gateway listener failed")
	}
	<-shutdownDone
}
