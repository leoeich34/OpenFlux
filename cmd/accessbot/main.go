// Command accessbot is the Telegram operator console for the OpenFlux
// access-key service: it long-polls Telegram and performs admin actions
// (issue/revoke/extend/list) against the loopback Admin API, restricted to an
// allowlist of admin user ids. Payment is handled out of band.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/p1neappleXpress/OpenFlux/tgbot"
)

func main() {
	token := flag.String("token", os.Getenv("TELEGRAM_TOKEN"), "Telegram bot token (or TELEGRAM_TOKEN env)")
	adminAddr := flag.String("admin", "127.0.0.1:8099", "accessd Admin API address")
	admins := flag.String("admins", os.Getenv("ACCESSBOT_ADMINS"), "comma-separated Telegram user ids allowed to operate the bot")
	flag.Parse()

	if *token == "" {
		log.Fatalf("accessbot: --token (or TELEGRAM_TOKEN) is required")
	}
	allow, err := parseAdmins(*admins)
	if err != nil {
		log.Fatalf("accessbot: --admins: %v", err)
	}

	tg := tgbot.NewHTTPClient(*token)
	bot := tgbot.NewBot(tg, tgbot.NewAdminClient(*adminAddr), allow)
	log.Printf("accessbot: polling Telegram, %d admin(s), admin API %s", len(allow), *adminAddr)
	run(tg, bot)
}

// run is the long-poll loop: fetch updates, dispatch each, advance the offset.
func run(tg tgbot.Client, bot *tgbot.Bot) {
	offset := 0
	for {
		ups, err := tg.GetUpdates(offset, 30)
		if err != nil {
			log.Printf("accessbot: getUpdates: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range ups {
			bot.Handle(u)
		}
		offset = nextOffset(ups, offset)
	}
}

// nextOffset returns the poll offset for the next getUpdates: one past the
// highest update id handled, or the current offset when there were none. This
// is what guarantees each update is processed exactly once.
func nextOffset(ups []tgbot.Update, cur int) int {
	max := cur - 1
	for _, u := range ups {
		if u.UpdateID > max {
			max = u.UpdateID
		}
	}
	return max + 1
}

// parseAdmins parses a comma-separated list of Telegram user ids. An empty
// list is an error — the bot must not run with no admins (it would admit none,
// but failing loudly avoids a silently useless bot).
func parseAdmins(s string) ([]int64, error) {
	var out []int64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad admin id %q", part)
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one admin id is required")
	}
	return out, nil
}
