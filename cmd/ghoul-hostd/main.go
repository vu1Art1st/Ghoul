//go:build linux

// Command ghoul-hostd is the optional Linux privileged catalog installer.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/hosttools"
	"github.com/Autumn-27/artex/shared"
)

func main() {
	socket := flag.String("socket", "/run/ghoul/hostd.sock", "Unix socket path")
	group := flag.String("group", "ghoul", "Socket group")
	data := flag.String("data", "/var/lib/ghoul", "Data directory")
	flag.Parse()
	if os.Geteuid() != 0 {
		log.Fatal("ghoul-hostd requires root for system package installation")
	}
	pg, err := db.Open(os.Getenv("GHOUL_PG_DSN"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pg.Close()
	svc, err := shared.New(*data, pg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	installer := hosttools.NewInstaller(ctx, pg, svc, func() string { return os.Getenv("GHOUL_INSTALL_PROXY") })
	if err := pg.RecoverHostInstallJobs(ctx); err != nil {
		log.Fatalf("recover jobs: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(*socket), 0o755); err != nil {
		log.Fatal(err)
	}
	_ = os.Remove(*socket)
	ln, err := net.Listen("unix", *socket)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.Chmod(*socket, 0o660); err != nil {
		log.Fatal(err)
	}
	if gid := lookupGID(*group); gid >= 0 {
		_ = os.Chown(*socket, 0, gid)
	}
	srv := &http.Server{Handler: hosttools.Handler(installer), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("ghoul-hostd listening on %s", *socket)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	_ = srv.Shutdown(context.Background())
	_ = os.Remove(*socket)
}

func lookupGID(name string) int {
	// Numeric groups are supported without cgo. Named groups use the standard os/user resolver.
	if id, err := strconv.Atoi(name); err == nil {
		return id
	}
	group, err := user.LookupGroup(name)
	if err != nil {
		return -1
	}
	id, _ := strconv.Atoi(group.Gid)
	return id
}
