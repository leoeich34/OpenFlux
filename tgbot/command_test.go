package tgbot

import "testing"

func TestParseCommand(t *testing.T) {
	cases := []struct {
		text    string
		kind    string
		id      string
		days    int
		wantErr bool
	}{
		{"/issue 30", "issue", "", 30, false},
		{"/issue", "issue", "", defaultIssueDays, false},
		{"/issue abc", "", "", 0, true},
		{"/revoke K1", "revoke", "K1", 0, false},
		{"/revoke", "", "", 0, true},
		{"/extend K1 7", "extend", "K1", 7, false},
		{"/extend K1", "", "", 0, true},
		{"/extend K1 abc", "", "", 0, true},
		{"/list", "list", "", 0, false},
		{"/help", "help", "", 0, false},
		{"hello there", "unknown", "", 0, false},
		{"/issue@mybot 30", "issue", "", 30, false}, // bot-suffixed command
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			cmd, err := ParseCommand(c.text)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if cmd.Kind != c.kind || cmd.ID != c.id || cmd.Days != c.days {
				t.Errorf("ParseCommand(%q) = %+v, want {%s %s %d}", c.text, cmd, c.kind, c.id, c.days)
			}
		})
	}
}

func TestIsAdmin(t *testing.T) {
	allow := []int64{7, 42}
	if !IsAdmin(42, allow) {
		t.Errorf("42 should be admin")
	}
	if IsAdmin(9, allow) {
		t.Errorf("9 should not be admin")
	}
	if IsAdmin(7, nil) {
		t.Errorf("empty allowlist should admit no one")
	}
}
