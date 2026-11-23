package kubernetes

import "testing"

func TestSanitizeImage(t *testing.T) {
	digest := "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name, in, want string
		ok             bool
	}{
		{"plain", "nginx:1.27", "nginx:1.27", true},
		{"registry path", "registry.example.com/team/app:1.2", "registry.example.com/team/app:1.2", true},
		{"registry port", "registry.example.com:5000/app:1", "registry.example.com:5000/app:1", true},
		{"digest", "registry.example.com/app@" + digest, "registry.example.com/app@" + digest, true},
		{"bare digest", "nginx@" + digest, "nginx@" + digest, true},
		{"userinfo", "user:pass@registry.example.com/app:1", "registry.example.com/app:1", true},
		{"scheme and token", "https://token@registry.example.com/app:1", "registry.example.com/app:1", true},
		{"userinfo with digest", "user:pass@registry.example.com/app@" + digest, "registry.example.com/app@" + digest, true},
		{"scheme only", "https://registry.example.com/app:1", "registry.example.com/app:1", true},
		{"userinfo without path", "user:pass@nginx", "", false},
		{"slash in password", "user:p/ss@registry.example.com/app:1", "", false},
		{"whitespace", "app:1 --token=x", "", false},
		{"query", "registry.example.com/app?token=x", "", false},
		{"fragment", "registry.example.com/app#x", "", false},
		{"empty", "", "", false},
		{"only scheme", "https://", "", false},
		{"garbage", "@@@", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := sanitizeImage(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Errorf("sanitizeImage(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}
