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

	"github.com/p1neappleXpress/OpenFlux/access"
)

func main() {
	store := flag.String("store", "accessd-store.json", "path to the JSON key store")
	pool := flag.String("pool", "pool.txt", "newline-separated file of Telemost call links")
	adminAddr := flag.String("admin", "127.0.0.1:8099", "loopback address for the admin API")
	exitAddr := flag.String("exit", ":8098", "address for the exit WebSocket hub")
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

	go func() {
		log.Printf("accessd: exit hub on %s", *exitAddr)
		log.Fatal(http.ListenAndServe(*exitAddr, srv.ExitHandler()))
	}()
	log.Printf("accessd: admin API on %s (%d pool links)", *adminAddr, len(links))
	log.Fatal(http.ListenAndServe(*adminAddr, srv.AdminHandler()))
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
