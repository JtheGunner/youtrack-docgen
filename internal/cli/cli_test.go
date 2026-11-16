package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `
youtrack:
  url: https://youtrack.example.com
  token: ${YOUTRACK_TOKEN}
  project: INFRA
collectors:
  kubernetes:
    paths: [./k8s]
  netbox:
    url: https://netbox.example.com
    token: ${NETBOX_TOKEN}
sinks:
  youtrack: {}
  markdown:
    dir: ./out
`

const (
	fakeYouTrackToken = "fake-youtrack-token-123"
	fakeNetBoxToken   = "fake-netbox-token-456"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docgen.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	cmd := NewRootCmd(&out, &errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestValidateConfigSucceeds(t *testing.T) {
	t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	path := writeConfig(t, validConfig)

	stdout, stderr, err := run("validate-config", "--config", path)
	if err != nil {
		t.Fatalf("validate-config error = %v\nstderr: %s", err, stderr)
	}
	for _, want := range []string{"config ok", "collectors: kubernetes, netbox", "sinks: markdown, youtrack"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
	for _, secret := range []string{fakeYouTrackToken, fakeNetBoxToken} {
		if strings.Contains(stdout+stderr, secret) {
			t.Errorf("output leaks %q", secret)
		}
	}
}

func TestValidateConfigReportsAllProblems(t *testing.T) {
	// An empty value counts as missing; this keeps the test independent of the
	// environment it runs in.
	t.Setenv("YOUTRACK_TOKEN", "")
	t.Setenv("NETBOX_TOKEN", "")
	path := writeConfig(t, validConfig)

	stdout, stderr, err := run("validate-config", "--config", path)
	if err == nil {
		t.Fatal("validate-config error = nil, want an error")
	}
	if strings.Contains(stdout, "config ok") {
		t.Errorf("stdout claims success:\n%s", stdout)
	}
	text := err.Error() + stderr
	for _, want := range []string{"YOUTRACK_TOKEN", "NETBOX_TOKEN"} {
		if !strings.Contains(text, want) {
			t.Errorf("output does not mention %q:\n%s", want, text)
		}
	}
}

func TestValidateConfigUsesDocgenYAMLByDefault(t *testing.T) {
	t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docgen.yaml"), []byte(validConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if _, stderr, err := run("validate-config"); err != nil {
		t.Fatalf("validate-config error = %v\nstderr: %s", err, stderr)
	}
}

func TestValidateConfigMissingFile(t *testing.T) {
	_, _, err := run("validate-config", "--config", filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("validate-config error = nil, want an error")
	}
}

func TestUnknownCommandFails(t *testing.T) {
	if _, _, err := run("does-not-exist"); err == nil {
		t.Fatal("unknown command error = nil, want an error")
	}
}
