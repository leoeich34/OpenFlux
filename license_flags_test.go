package main

import "testing"

func TestLicenseValidate(t *testing.T) {
	cases := []struct {
		name, server, keyID string
		wantEnabled         bool
		wantErr             bool
	}{
		{"neither set", "", "", false, false},
		{"both set", "ws://127.0.0.1:8098/exit/ws", "ABC123", true, false},
		{"only server", "ws://127.0.0.1:8098/exit/ws", "", false, true},
		{"only key id", "", "ABC123", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enabled, err := licenseValidate(c.server, c.keyID)
			if enabled != c.wantEnabled {
				t.Errorf("enabled = %v, want %v", enabled, c.wantEnabled)
			}
			if (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}
