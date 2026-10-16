// Command accessctl is a thin CLI over the accessd admin API:
//
//	accessctl issue  --admin 127.0.0.1:8099 --days 30
//	accessctl revoke --admin 127.0.0.1:8099 --id ABC
//	accessctl extend --admin 127.0.0.1:8099 --id ABC --days 7
//	accessctl list   --admin 127.0.0.1:8099
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "usage: accessctl <issue|revoke|extend|list> [flags]")
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "issue":
		return cmdIssue(rest, stdout)
	case "revoke":
		return cmdRevoke(rest, stdout)
	case "extend":
		return cmdExtend(rest, stdout)
	case "list":
		return cmdList(rest, stdout)
	default:
		fmt.Fprintf(stdout, "unknown command %q\n", cmd)
		return 2
	}
}

// adminBase normalizes --admin into an http base URL.
func adminBase(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + addr
}

func post(stdout io.Writer, base, path string, body any) (map[string]any, int) {
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		fmt.Fprintf(stdout, "request failed: %v\n", err)
		return nil, 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		msg, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stdout, "server error %d: %s\n", resp.StatusCode, strings.TrimSpace(string(msg)))
		return nil, 1
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out, 0
}

func cmdIssue(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	admin := fs.String("admin", "127.0.0.1:8099", "admin API address")
	days := fs.Int("days", 30, "validity in days")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	out, code := post(stdout, adminBase(*admin), "/admin/issue", map[string]int{"ttlSeconds": *days * 86400})
	if code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "id:   %v\n", out["id"])
	fmt.Fprintf(stdout, "key:  %v\n", out["key"])
	fmt.Fprintln(stdout, "(the key contains the encryption secret — treat it like a password)")
	return 0
}

func cmdRevoke(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("revoke", flag.ContinueOnError)
	admin := fs.String("admin", "127.0.0.1:8099", "admin API address")
	id := fs.String("id", "", "key id to revoke")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, code := post(stdout, adminBase(*admin), "/admin/revoke", map[string]string{"id": *id}); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "revoked %s\n", *id)
	return 0
}

func cmdExtend(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("extend", flag.ContinueOnError)
	admin := fs.String("admin", "127.0.0.1:8099", "admin API address")
	id := fs.String("id", "", "key id to extend")
	days := fs.Int("days", 7, "days to add")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, code := post(stdout, adminBase(*admin), "/admin/extend", map[string]any{"id": *id, "ttlSeconds": *days * 86400}); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "extended %s by %d days\n", *id, *days)
	return 0
}

func cmdList(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	admin := fs.String("admin", "127.0.0.1:8099", "admin API address")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	resp, err := http.Get(adminBase(*admin) + "/admin/list")
	if err != nil {
		fmt.Fprintf(stdout, "request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Fprintln(stdout, strings.TrimSpace(string(body)))
	return 0
}
