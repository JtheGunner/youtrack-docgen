package collector_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/collector"
)

const fakeToken = "seeded-fake-token-0123456789"

func TestSecretNeverPrintsItsValue(t *testing.T) {
	settings := collector.Settings{URL: "https://netbox.example.com", Token: collector.NewSecret(fakeToken)}
	tests := []struct {
		name string
		got  func() string
	}{
		{"String", func() string { return settings.Token.String() }},
		{"GoString", func() string { return settings.Token.GoString() }},
		{"%v", func() string { return fmt.Sprintf("%v", settings.Token) }},
		{"%+v", func() string { return fmt.Sprintf("%+v", settings.Token) }},
		{"%#v", func() string { return fmt.Sprintf("%#v", settings.Token) }},
		{"%q", func() string { return fmt.Sprintf("%q", settings.Token) }},
		{"%v settings", func() string { return fmt.Sprintf("%v", settings) }},
		{"%+v settings", func() string { return fmt.Sprintf("%+v", settings) }},
		{"%#v settings", func() string { return fmt.Sprintf("%#v", settings) }},
		{"%+v settings pointer", func() string { return fmt.Sprintf("%+v", &settings) }},
		{"json", func() string {
			b, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.got()
			if strings.Contains(got, fakeToken) {
				t.Fatalf("output leaks the secret: %s", got)
			}
		})
	}
}

func TestSecretMask(t *testing.T) {
	tests := []struct {
		name   string
		secret collector.Secret
		want   string
		isSet  bool
	}{
		{"set", collector.NewSecret(fakeToken), "[REDACTED]", true},
		{"empty", collector.NewSecret(""), "", false},
		{"zero value", collector.Secret{}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.secret.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
			if got := tt.secret.GoString(); got != tt.want {
				t.Errorf("GoString() = %q, want %q", got, tt.want)
			}
			if got := tt.secret.IsSet(); got != tt.isSet {
				t.Errorf("IsSet() = %v, want %v", got, tt.isSet)
			}
		})
	}
}

func TestSecretValue(t *testing.T) {
	tests := []struct {
		name   string
		secret collector.Secret
		want   string
	}{
		{"set", collector.NewSecret(fakeToken), fakeToken},
		{"empty", collector.NewSecret(""), ""},
		{"zero value", collector.Secret{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.secret.Value(); got != tt.want {
				t.Errorf("Value() = %q, want %q", got, tt.want)
			}
		})
	}
}
