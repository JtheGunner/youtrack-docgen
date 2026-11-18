package config

import (
	"strings"
	"testing"
)

func TestParseInsecureHTTP(t *testing.T) {
	withYouTrackURL := func(u string) string {
		return strings.Replace(validYAML, "https://youtrack.example.com", u, 1)
	}
	withNetBoxURL := func(u string) string {
		return strings.Replace(validYAML, "https://netbox.example.com", u, 1)
	}
	tests := []struct {
		name    string
		yaml    string
		wantErr string // empty means the config must be accepted
	}{
		{"https youtrack", withYouTrackURL("https://youtrack.example.com"), ""},
		{"http youtrack remote", withYouTrackURL("http://youtrack.example.com"), "youtrack.url: uses http://"},
		{"http netbox remote", withNetBoxURL("http://netbox.example.com"), "collectors.netbox.url: uses http://"},
		{"http remote IP", withYouTrackURL("http://192.0.2.10:8080"), "youtrack.url: uses http://"},
		{"http localhost", withYouTrackURL("http://localhost:8080"), ""},
		{"http loopback IPv4", withYouTrackURL("http://127.0.0.1:8080"), ""},
		{"http loopback IPv6", withYouTrackURL("http://[::1]:8080"), ""},
		{"explicit opt-in youtrack", "allow_insecure_http: true\n" + withYouTrackURL("http://youtrack.example.com"), ""},
		{"explicit opt-in netbox", "allow_insecure_http: true\n" + withNetBoxURL("http://netbox.example.com"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml), envMap(fullEnv))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
