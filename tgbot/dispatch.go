package tgbot

import (
	"errors"
	"fmt"
	"strings"
)

const helpText = `OpenFlux access-key operator bot. Commands:
/issue [days]        issue a new key (default 30 days)
/revoke <id>         revoke a key immediately
/extend <id> <days>  extend a key's validity
/list                list all keys
/help                this message`

// Bot turns Telegram updates into admin actions and replies.
type Bot struct {
	tg    Client
	admin *AdminClient
	allow []int64
}

// NewBot builds a Bot. allow is the list of admin Telegram user ids.
func NewBot(tg Client, admin *AdminClient, allow []int64) *Bot {
	return &Bot{tg: tg, admin: admin, allow: allow}
}

// Handle authorizes, parses, performs the admin action, and sends exactly one
// reply. It never logs or echoes anything but the reply.
func (b *Bot) Handle(u Update) {
	if !IsAdmin(u.UserID, b.allow) {
		b.reply(u.ChatID, "You are not authorized to use this bot.")
		return
	}
	cmd, err := ParseCommand(u.Text)
	if err != nil {
		b.reply(u.ChatID, err.Error())
		return
	}
	b.reply(u.ChatID, b.run(cmd))
}

// run executes a parsed command against the admin API and returns the reply
// text. Admin-API errors are phrased for the operator, never dropped.
func (b *Bot) run(cmd Command) string {
	switch cmd.Kind {
	case "issue":
		id, key, err := b.admin.Issue(cmd.Days)
		if err != nil {
			return "Issue failed: " + phrase(err)
		}
		return fmt.Sprintf("Issued key %s (%d days).\n%s\n\n⚠️ This key contains the secret — treat it like a password.", id, cmd.Days, key)
	case "revoke":
		if err := b.admin.Revoke(cmd.ID); err != nil {
			return "Revoke failed: " + phrase(err)
		}
		return "Revoked " + cmd.ID + "."
	case "extend":
		if err := b.admin.Extend(cmd.ID, cmd.Days); err != nil {
			return "Extend failed: " + phrase(err)
		}
		return fmt.Sprintf("Extended %s by %d days.", cmd.ID, cmd.Days)
	case "list":
		keys, err := b.admin.List()
		if err != nil {
			return "List failed: " + phrase(err)
		}
		return formatList(keys)
	default: // help, unknown
		return helpText
	}
}

func formatList(keys []KeyInfo) string {
	if len(keys) == 0 {
		return "No keys."
	}
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s  %s  exp %s\n", k.ID, k.Status, k.ExpiresAt)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// phrase renders an admin-API error for the operator.
func phrase(err error) string {
	switch {
	case errors.Is(err, ErrPoolExhausted):
		return "the call pool is exhausted."
	case errors.Is(err, ErrNotFound):
		return "no such key id."
	default:
		return err.Error()
	}
}

func (b *Bot) reply(chatID int64, text string) {
	_ = b.tg.SendMessage(chatID, text)
}
