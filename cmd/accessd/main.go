// Command accessd is the OpenFlux access-key control daemon: it serves the
// loopback admin API and the exit-facing WebSocket hub over one key store.
package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/p1neappleXpress/OpenFlux/access"
	"github.com/p1neappleXpress/OpenFlux/orchestrator"
)

func main() {
	store := flag.String("store", "accessd-store.json", "path to the JSON key store")
	pool := flag.String("pool", "pool.txt", "newline-separated file of Telemost call links")
	adminAddr := flag.String("admin", "127.0.0.1:8099", "loopback address for the admin API")
	exitAddr := flag.String("exit", ":8098", "address for the exit WebSocket hub")
	dockerImage := flag.String("docker-image", "", "OpenFlux image to launch exits from; empty disables the pool (Plan-1 behavior)")
	warm := flag.Int("warm", 2, "number of pre-joined exits to keep warm (with --docker-image)")
	licenseWS := flag.String("license-ws", "", "exit WebSocket URL the launched exits report to, e.g. ws://host.docker.internal:8098/exit/ws")
	stateDir := flag.String("state-dir", "accessd-secrets", "directory for per-key secret files bind-mounted into exit containers")
	readyTimeout := flag.Duration("ready-timeout", 60*time.Second, "how long a cold-launched exit may take to join its call before the key is rolled back")
	flag.Parse()

	if !isLoopbackAddr(*adminAddr) {
		log.Fatalf("accessd: --admin must be a loopback address, got %q", *adminAddr)
	}

	links, err := loadPool(*pool)
	if err != nil {
		log.Fatalf("accessd: load pool: %v", err)
	}
	st, err := access.Open(*store, links)
	if err != nil {
		log.Fatalf("accessd: open store: %v", err)
	}
	srv := access.NewServer(st)

	if *dockerImage != "" {
		if *licenseWS == "" {
			log.Fatalf("accessd: --license-ws is required with --docker-image")
		}
		launcher, err := orchestrator.NewDockerLauncher(*dockerImage, orchestrator.WithStateDir(*stateDir))
		if err != nil {
			log.Fatalf("accessd: docker launcher: %v", err)
		}
		p := orchestrator.NewPool(st, launcher, *licenseWS, *warm)
		p.SetReady(srv.Connected, *readyTimeout)
		srv.SetLifecycle(p)

		if stopped, err := orchestrator.Reconcile(st, launcher, time.Now()); err != nil {
			log.Printf("accessd: reconcile at startup: %v", err)
		} else if len(stopped) > 0 {
			log.Printf("accessd: reconcile stopped %d orphaned exit(s)", len(stopped))
		}
		go maintainLoop(st, launcher, p)
		log.Printf("accessd: pool enabled (image %s, warm %d)", *dockerImage, *warm)
	}

	go func() {
		log.Printf("accessd: exit hub on %s", *exitAddr)
		log.Fatal(http.ListenAndServe(*exitAddr, srv.ExitHandler()))
	}()
	log.Printf("accessd: admin API on %s (%d pool links)", *adminAddr, len(links))
	log.Fatal(http.ListenAndServe(*adminAddr, srv.AdminHandler()))
}

// maintainLoop periodically reconciles orphaned exits and tops up the warm set.
func maintainLoop(st *access.Store, launcher orchestrator.ExitLauncher, p *orchestrator.Pool) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	p.EnsureWarm()
	for range t.C {
		if _, err := orchestrator.Reconcile(st, launcher, time.Now()); err != nil {
			log.Printf("accessd: reconcile: %v", err)
		}
		p.EnsureWarm()
	}
}

// loadPool reads a newline-separated call-link file, trimming whitespace and
// skipping blank and #-comment lines.
func loadPool(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

// isLoopbackAddr reports whether host:port binds only to loopback.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
