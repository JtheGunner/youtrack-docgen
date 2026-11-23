package collector

const secretMask = "[REDACTED]"

// Secret is a credential handed to a collector. Formatting, logging and
// marshalling it prints a mask; only Value returns the value. The zero value
// is an unset secret.
//
// The value sits behind a pointer, so even a struct that embeds a Secret in an
// unexported field prints an address instead of the credential.
type Secret struct {
	value *string
}

// NewSecret wraps a credential. An empty string gives an unset secret.
func NewSecret(value string) Secret {
	if value == "" {
		return Secret{}
	}
	return Secret{value: &value}
}

// Value returns the secret value. Use it only to build an outgoing request.
func (s Secret) Value() string {
	if s.value == nil {
		return ""
	}
	return *s.value
}

// IsSet reports whether the secret has a value.
func (s Secret) IsSet() bool { return s.value != nil }

// String implements fmt.Stringer. It returns "[REDACTED]" for a set secret and
// "" for an unset one.
func (s Secret) String() string {
	if !s.IsSet() {
		return ""
	}
	return secretMask
}

// GoString implements fmt.GoStringer, which fmt uses for %#v.
func (s Secret) GoString() string { return s.String() }

// MarshalText implements encoding.TextMarshaler, used by JSON and YAML.
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Settings is the configuration of one collector block in docgen.yaml.
//
// Allow and deny rules are not part of it: the filter applies them centrally
// to the entities of every collector.
type Settings struct {
	// URL is the url key of the block.
	URL string
	// Token is the token key of the block, usually expanded from ${ENV}.
	Token Secret
	// Options holds the module-specific keys of the block, exactly as decoded
	// from YAML. It is nil when the block has none.
	Options map[string]any
}

// Factory builds a collector from its settings. It is called once per run for
// every enabled collector block whose name it is registered under; the
// collector's Name must equal that name.
type Factory func(Settings) (Collector, error)
