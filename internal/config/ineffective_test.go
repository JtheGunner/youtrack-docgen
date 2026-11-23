package config

import (
	"errors"
	"strings"
	"testing"
)

// withKubernetes replaces the kubernetes collector block of validYAML.
func withKubernetes(block string) string {
	return strings.Replace(validYAML, "  kubernetes:\n    paths: [{path: ./kubernetes-config, cluster: prod}, {path: ./gitops, cluster: prod}]", "  kubernetes:\n"+block, 1)
}

func TestParseRejectsIneffectiveConfig(t *testing.T) {
	const paths = "    paths: [{path: ./k8s, cluster: prod}]\n"
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "unknown kind names the valid ones",
			yaml: withKubernetes(paths + "    deny:\n      - kinds: [Deployment]\n"),
			want: []string{`collectors.kubernetes.deny[0]: kinds: "Deployment" matches no kind`, "cluster, host, ingress, namespace, service, workload"},
		},
		{
			name: "kind of another collector",
			yaml: strings.Replace(validYAML, "      - kinds: [host]\n        tenants: [lab]", "      - kinds: [workload]", 1),
			want: []string{`kinds: "workload" matches no kind`, "host, interface, ipaddress, network, site"},
		},
		{
			name: "tags on kubernetes",
			yaml: withKubernetes(paths + "    allow:\n      - tags: [documented]\n"),
			want: []string{"collectors.kubernetes.allow[0]: tags: this collector does not provide it"},
		},
		{
			name: "roles and tenants on kubernetes",
			yaml: withKubernetes(paths + "    deny:\n      - roles: [lab]\n      - tenants: [lab]\n"),
			want: []string{"deny[0]: roles: this collector", "deny[1]: tenants: this collector"},
		},
		{
			name: "namespaces on netbox",
			yaml: strings.Replace(validYAML, "      - roles: [lab, test]", "      - namespaces: [team-a]", 1),
			want: []string{"collectors.netbox.deny[0]: namespaces: this collector does not provide it"},
		},
		{
			name: "unknown label key",
			yaml: withKubernetes(paths + "    deny:\n      - labels: { team: payments }\n"),
			want: []string{`labels: this collector never sets a label "team"`},
		},
		{
			name: "kubernetes url",
			yaml: withKubernetes(paths + "    url: https://k8s.example.com\n"),
			want: []string{"collectors.kubernetes.url: has no effect"},
		},
		{
			name: "kubernetes token",
			yaml: withKubernetes(paths + "    token: ${NETBOX_TOKEN}\n"),
			want: []string{"collectors.kubernetes.token: has no effect"},
		},
		{
			name: "youtrack sink token",
			yaml: strings.Replace(validYAML, "    root_article: Infrastructure", "    root_article: Infrastructure\n    token: ${YOUTRACK_TOKEN}", 1),
			want: []string{"sinks.youtrack.token: has no effect"},
		},
		{
			name: "markdown sink deny",
			yaml: strings.Replace(validYAML, "    dir: ./out", "    dir: ./out\n    deny:\n      - kinds: [host]", 1),
			want: []string{"sinks.markdown.allow/deny: have no effect"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml), envMap(fullEnv))
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("Parse() error = %v, want a *ValidationError", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not mention %q:\n%s", want, err)
				}
			}
		})
	}
}

func TestParseAcceptsEffectiveRules(t *testing.T) {
	const yaml = `
collectors:
  netbox:
    url: https://netbox.example.com
    token: ${NETBOX_TOKEN}
    allow:
      - tags: [documented]
      - kinds: ["*"]
        labels: { role: router, tag.core: "true", tenant: acme }
    deny:
      - kinds: [Host, ip*]
  kubernetes:
    paths:
      - path: ./gitops
        cluster: prod
        repo_url: https://git.example.com/ops/gitops
    allow:
      - kinds: [cluster]
    deny:
      - kinds: [workload]
        namespaces: [kube-*]
        labels: { label.team: payments, namespace: team-a }
  overlay:
    allow:
      - kinds: [cluster]
        roles: [anything]
sinks:
  markdown:
    dir: ./out
`
	if _, err := Parse([]byte(yaml), envMap(fullEnv)); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseChecksKindsOfUnknownModulesAgainstTheModel(t *testing.T) {
	const yaml = `
collectors:
  overlay:
    deny:
      - kinds: [Deployment]
sinks:
  markdown:
    dir: ./out
`
	_, err := Parse([]byte(yaml), envMap(fullEnv))
	if err == nil || !strings.Contains(err.Error(), `collectors.overlay.deny[0]: kinds: "Deployment" matches no kind`) {
		t.Fatalf("Parse() error = %v", err)
	}
}
