package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const fakeToken = "perm:fake-token-for-tests"

func TestSecretNeverPrintsItsValue(t *testing.T) {
	s := Secret(fakeToken)

	outputs := map[string]string{
		"String":     s.String(),
		"%s":         fmt.Sprintf("[%s]", s),
		"%v":         fmt.Sprintf("%v", s),
		"%+v":        fmt.Sprintf("%+v", struct{ T Secret }{s}),
		"%#v":        fmt.Sprintf("%#v", s),
		"%q":         fmt.Sprintf("%q", s),
		"GoString":   s.GoString(),
		"MarshalTxt": mustMarshalText(t, s),
	}
	if b, err := json.Marshal(struct{ T Secret }{s}); err != nil {
		t.Fatal(err)
	} else {
		outputs["json"] = string(b)
	}
	if b, err := yaml.Marshal(struct{ T Secret }{s}); err != nil {
		t.Fatal(err)
	} else {
		outputs["yaml"] = string(b)
	}

	for name, out := range outputs {
		if strings.Contains(out, fakeToken) {
			t.Errorf("%s leaks the secret: %q", name, out)
		}
		if !strings.Contains(out, "***") {
			t.Errorf("%s does not show the mask: %q", name, out)
		}
	}
}

func TestSecretReveal(t *testing.T) {
	if got := Secret(fakeToken).Reveal(); got != fakeToken {
		t.Errorf("Reveal() = %q, want %q", got, fakeToken)
	}
}

func TestSecretIsSet(t *testing.T) {
	if Secret("").IsSet() {
		t.Error("empty secret reports IsSet")
	}
	if !Secret("x").IsSet() {
		t.Error("non-empty secret does not report IsSet")
	}
}

func mustMarshalText(t *testing.T, s Secret) string {
	t.Helper()
	b, err := s.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
