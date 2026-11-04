package config

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestCheckSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	write(t, path, `apiVersion: tapyr/v1alpha1
kind: Provider
metadata: { name: p, version: 0.1.0 }
spec:
  baseURL: https://api.example.invalid
  secrets: [{ name: apiKey }, { name: password }]
---
apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: a }
spec:
  provider: p
  secrets:
    apiKey: { env: TAPYR_SET }
    password: { env: TAPYR_UNSET }
  targets:
    - name: t
      secrets:
        password: { file: secrets/missing }
---
apiVersion: tapyr/v1alpha1
kind: Output
metadata: { name: vm }
spec:
  type: remote_write
  remoteWrite:
    url: http://vm:8428/api/v1/write
    headers:
      Authorization: { env: TAPYR_EMPTY }
      X-Tenant: tenant-a
    basicAuth:
      username: { file: secrets/user }
      password: { file: secrets/password }
---
apiVersion: tapyr/v1alpha1
kind: Settings
metadata: { name: default }
spec:
  server:
    basicAuth:
      username: { env: TAPYR_SET }
      password: { env: TAPYR_UNSET }
`)
	cfg, err := newTestLoader(t).Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	const secretValue = "s3cr3t-value"
	src := SecretSource{
		LookupEnv: func(name string) (string, bool) {
			switch name {
			case "TAPYR_SET":
				return secretValue, true
			case "TAPYR_EMPTY":
				return " ", true
			}
			return "", false
		},
		ReadFile: func(p string) ([]byte, error) {
			switch p {
			case filepath.Join(dir, "secrets", "user"):
				return []byte(secretValue), nil
			case filepath.Join(dir, "secrets", "password"):
				return []byte("\n"), nil
			}
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
		},
	}
	diags := CheckSecrets(cfg, src)
	got := strings.ReplaceAll(diags.Error(), dir+string(filepath.Separator), "")
	want := strings.Join([]string{
		"config.yaml:15:5: spec.secrets.password: environment variable TAPYR_UNSET is not set",
		"config.yaml:19:9: spec.targets[0].secrets.password: secret file secrets/missing: no such file",
		"config.yaml:29:7: spec.remoteWrite.headers.Authorization: environment variable TAPYR_EMPTY is empty",
		"config.yaml:33:7: spec.remoteWrite.basicAuth.password: secret file secrets/password is empty",
		"config.yaml:42:7: spec.server.basicAuth.password: environment variable TAPYR_UNSET is not set",
	}, "\n")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("diagnostics (-want +got):\n%s", diff)
	}
	if strings.Contains(diags.Error(), secretValue) {
		t.Fatal("a secret value leaked into the diagnostics")
	}
}
