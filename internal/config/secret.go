// Package config loads and validates the docgen.yaml configuration.
package config

const secretMask = "***"

// Secret is a credential. Formatting, logging and marshalling it prints a mask;
// only Reveal returns the value.
type Secret string

// Reveal returns the secret value. Use it only to build an outgoing request.
func (s Secret) Reveal() string { return string(s) }

// IsSet reports whether the secret has a value.
func (s Secret) IsSet() bool { return s != "" }

// String implements fmt.Stringer.
func (Secret) String() string { return secretMask }

// GoString implements fmt.GoStringer, which fmt uses for %#v.
func (Secret) GoString() string { return secretMask }

// MarshalText implements encoding.TextMarshaler, used by JSON and YAML.
func (Secret) MarshalText() ([]byte, error) { return []byte(secretMask), nil }
