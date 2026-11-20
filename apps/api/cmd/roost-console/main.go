package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JingxuanC/roost-console/apps/api/internal/auth"
	"github.com/JingxuanC/roost-console/apps/api/internal/billing"
	"github.com/JingxuanC/roost-console/apps/api/internal/config"
	"github.com/JingxuanC/roost-console/apps/api/internal/db"
	"github.com/JingxuanC/roost-console/apps/api/internal/httpapi"
	"github.com/JingxuanC/roost-console/apps/api/internal/payment"
)

func supabaseHTTPClient(cfg config.Config) *http.Client {
	transport := &http.Transport{}
	if cfg.SupabaseDialIP != "" {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(cfg.SupabaseDialIP, port))
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: transport}
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	if cfg.SupabaseURL == "" || cfg.SupabaseAnonKey == "" {
		log.Error("SUPABASE_URL and SUPABASE_ANON_KEY are required")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database connection failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	store := billing.New(pool)
	verifier := auth.NewVerifier(cfg.SupabaseURL, cfg.SupabaseAnonKey, supabaseHTTPClient(cfg))
	pay := &payment.Client{
		AppID: cfg.XunhuAppID, AppSecret: cfg.XunhuAppSecret, APIBase: cfg.XunhuAPIBase,
		NotifyURL: cfg.XunhuNotifyURL, ReturnURL: cfg.XunhuReturnURL, HTTP: &http.Client{Timeout: 20 * time.Second},
	}
	srv := httpapi.New(cfg, store, verifier, pay, log)
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			n, err := store.ExpireLeases(context.Background())
			if err != nil {
				log.Warn("lease expiry failed", "err", err)
			} else if n > 0 {
				log.Info("expired stale leases", "count", n)
			}
		}
	}()
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		log.Info("roost console api listening", "addr", httpServer.Addr, "env", cfg.Environment)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	_ = httpServer.Shutdown(shutdown)
}
