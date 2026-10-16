package access

import "github.com/p1neappleXpress/OpenFlux/share"

// BuildKey returns an openflux:// link for a single Telemost call. callLink
// is the conference URL the client joins; secret is the per-user encryption
// secret. The encryption context is the call link itself, so both peers
// derive the same context without extra configuration.
func BuildKey(callLink, secret string) (string, error) {
	cfg := share.Config{
		Secret:  secret,
		Context: callLink,
		Transports: []share.Transport{
			{Type: "telemost", URL: callLink},
		},
	}
	return share.Encode(cfg)
}
