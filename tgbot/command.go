package tgbot

import (
	"fmt"
	"strconv"
	"strings"
)

// defaultIssueDays is the validity used by a bare "/issue".
const defaultIssueDays = 30

// Command is a parsed operator instruction.
type Command struct {
	Kind string // issue | revoke | extend | list | help | unknown
	ID   string
	Days int
}

// ParseCommand parses a Telegram message into a Command. A bare word that is
// not a known command is Kind "unknown"; a known command with bad arguments
// returns an error so the dispatcher can reply with usage.
func ParseCommand(text string) (Command, error) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return Command{Kind: "unknown"}, nil
	}
	// Strip a "@botname" suffix Telegram appends in groups.
	verb := fields[0]
	if i := strings.IndexByte(verb, '@'); i >= 0 {
		verb = verb[:i]
	}
	args := fields[1:]

	switch verb {
	case "/issue":
		days := defaultIssueDays
		if len(args) >= 1 {
			n, err := strconv.Atoi(args[0])
			if err != nil || n <= 0 {
				return Command{}, fmt.Errorf("usage: /issue [days]")
			}
			days = n
		}
		return Command{Kind: "issue", Days: days}, nil
	case "/revoke":
		if len(args) != 1 {
			return Command{}, fmt.Errorf("usage: /revoke <id>")
		}
		return Command{Kind: "revoke", ID: args[0]}, nil
	case "/extend":
		if len(args) != 2 {
			return Command{}, fmt.Errorf("usage: /extend <id> <days>")
		}
		n, err := strconv.Atoi(args[1])
		if err != nil || n <= 0 {
			return Command{}, fmt.Errorf("usage: /extend <id> <days>")
		}
		return Command{Kind: "extend", ID: args[0], Days: n}, nil
	case "/list":
		return Command{Kind: "list"}, nil
	case "/help", "/start":
		return Command{Kind: "help"}, nil
	default:
		return Command{Kind: "unknown"}, nil
	}
}

// IsAdmin reports whether userID is in the allowlist. An empty allowlist
// admits no one.
func IsAdmin(userID int64, allow []int64) bool {
	for _, a := range allow {
		if a == userID {
			return true
		}
	}
	return false
}
