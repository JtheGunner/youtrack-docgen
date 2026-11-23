package model

import "testing"

func TestNewID(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		kind    Kind
		key     string
		want    ID
		wantErr bool
	}{
		{name: "valid", source: "netbox", kind: KindHost, key: "42", want: "netbox:host:42"},
		{name: "key may contain slash and dot", source: "k8s", kind: KindWorkload, key: "default/web.v1", want: "k8s:workload:default/web.v1"},
		{name: "empty source", source: "", kind: KindHost, key: "1", wantErr: true},
		{name: "empty kind", source: "netbox", kind: "", key: "1", wantErr: true},
		{name: "empty key", source: "netbox", kind: KindHost, key: "", wantErr: true},
		{name: "colon in source", source: "net:box", kind: KindHost, key: "1", wantErr: true},
		{name: "colon in key", source: "netbox", kind: KindHost, key: "a:b", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewID(tt.source, tt.kind, tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("NewID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseID(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantSource string
		wantKind   Kind
		wantKey    string
		wantErr    bool
	}{
		{name: "valid", in: "netbox:host:42", wantSource: "netbox", wantKind: KindHost, wantKey: "42"},
		{name: "too few parts", in: "netbox:host", wantErr: true},
		{name: "too many parts", in: "netbox:host:1:2", wantErr: true},
		{name: "empty part", in: "netbox::42", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, kind, key, err := ParseID(ID(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if source != tt.wantSource || kind != tt.wantKind || key != tt.wantKey {
				t.Errorf("ParseID() = (%q, %q, %q), want (%q, %q, %q)",
					source, kind, key, tt.wantSource, tt.wantKind, tt.wantKey)
			}
		})
	}
}
