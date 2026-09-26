package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/fsblob"
	"github.com/jackc/moneybags/backend/jedstore"
	"github.com/jackc/moneybags/backend/pgstore"
	"github.com/jackc/moneybags/backend/safefetch"
	"github.com/jackc/moneybags/backend/server"
)

func env(name, fallback string) string {
	if s, ok := os.LookupEnv(name); ok {
		return s
	}
	return fallback
}
func main() {
	if err := run(); err != nil {
		slog.Error("moneybags stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	command := "server"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command != "server" && command != "migrate" && command != "action" {
		return errors.New("usage: moneybags {server|migrate|action <administrative-action> <json>}")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	var store core.Store
	var closeStore func() error
	switch env("DATABASE_BACKEND", "postgresql") {
	case "postgresql":
		databaseURL := os.Getenv("DATABASE_URL")
		if databaseURL == "" {
			return errors.New("DATABASE_URL is required for PostgreSQL")
		}
		s, e := pgstore.Open(ctx, databaseURL)
		if e != nil {
			return fmt.Errorf("open PostgreSQL: %w", e)
		}
		store = s
		closeStore = s.Close
		if command == "migrate" {
			defer closeStore()
			return s.Migrate(ctx)
		}
	case "jed":
		dir := env("JED_DATA_DIR", "data/jed")
		s, e := jedstore.Open(dir)
		if e != nil {
			return fmt.Errorf("open Jed: %w", e)
		}
		store = s
		closeStore = s.Close
		if command == "migrate" {
			return closeStore()
		}
	default:
		return errors.New("DATABASE_BACKEND must be postgresql or jed; selecting a backend does not migrate data")
	}
	defer closeStore()
	port := env("PORT", "4000")
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return errors.New("PORT must be between 1 and 65535")
	}
	origin := strings.TrimSuffix(env("MCP_CANONICAL_URL", "http://localhost:"+port), "/")
	parsed, e := url.Parse(origin)
	if e != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("MCP_CANONICAL_URL must be an HTTP(S) origin")
	}
	ip, _ := netip.ParseAddr(parsed.Hostname())
	loopback := parsed.Hostname() == "localhost" || ip.IsLoopback()
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return errors.New("MCP_CANONICAL_URL requires HTTPS outside loopback")
	}
	secure, e := strconv.ParseBool(env("SECURE_COOKIES", strconv.FormatBool(parsed.Scheme == "https")))
	if e != nil || (!loopback && !secure) {
		return errors.New("SECURE_COOKIES must be true for a public deployment")
	}
	rpID := env("WEBAUTHN_RP_ID", parsed.Hostname())
	if rpID == "" || strings.ContainsAny(rpID, "/:") {
		return errors.New("WEBAUTHN_RP_ID must be a hostname")
	}
	if configured := env("WEBAUTHN_ORIGIN", origin); configured != origin {
		return errors.New("WEBAUTHN_ORIGIN must match MCP_CANONICAL_URL")
	}
	if parsed.Hostname() != rpID && !strings.HasSuffix(parsed.Hostname(), "."+rpID) {
		return errors.New("WEBAUTHN_RP_ID must be the canonical hostname or a parent domain")
	}
	if parsed.Scheme == "http" {
		bind := env("BIND_ADDRESS", "127.0.0.1")
		address, err := netip.ParseAddr(bind)
		if bind != "localhost" && (err != nil || !address.IsLoopback()) {
			return errors.New("HTTP development servers must bind to a loopback address")
		}
	}
	blobs, e := fsblob.New(env("ATTACHMENTS_DIR", "data/attachments"))
	if e != nil {
		return e
	}
	quota, e := strconv.ParseInt(env("FAMILY_STORAGE_QUOTA_BYTES", "1073741824"), 10, 64)
	if e != nil || quota < 1 {
		return errors.New("FAMILY_STORAGE_QUOTA_BYTES must be positive")
	}
	fetcher := safefetch.New()
	app := core.New(core.Config{Store: store, Blobs: blobs, Fetcher: fetcher, OAuthResolver: fetcher, Origin: origin, RPID: rpID, RPName: "Money Bags", StorageQuotaBytes: quota})
	if command == "action" {
		if len(os.Args) != 4 {
			return errors.New("usage: moneybags action <administrative-action> '<json>'")
		}
		name := os.Args[2]
		allowed := false
		for _, info := range app.Actions() {
			if info.Name == name && info.Administrative {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("only explicitly administrative actions are available through this command")
		}
		out, e := app.InvokeJSON(core.WithPrincipal(ctx, core.Principal{Admin: true, Source: "cli"}), name, json.RawMessage(os.Args[3]))
		if e != nil {
			return e
		}
		fmt.Println(string(out))
		return nil
	}
	assets := env("ASSETS_DIR", "build/assets")
	if _, e := os.Stat(assets); e != nil {
		if _, e = os.Stat("assets"); e == nil {
			assets = "assets"
		}
	}
	srv := &http.Server{Addr: net.JoinHostPort(env("BIND_ADDRESS", "127.0.0.1"), port), Handler: server.New(app, server.Config{Origin: origin, SecureCookies: secure, AssetsDir: assets, Logger: logger}), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 45 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10}
	stopped := make(chan struct{})
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
			shutdown, done := context.WithTimeout(context.Background(), 15*time.Second)
			defer done()
			_ = srv.Shutdown(shutdown)
		case <-stopped:
		}
	}()
	logger.Info("server listening", "address", srv.Addr, "origin", origin, "backend", env("DATABASE_BACKEND", "postgresql"))
	e = srv.ListenAndServe()
	close(stopped)
	<-shutdownDone
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
