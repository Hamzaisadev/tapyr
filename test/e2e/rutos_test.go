//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestTeltonikaRutOS runs every template of the teltonika-rutos package
// against a real router, once a minute for a minute.
func TestTeltonikaRutOS(t *testing.T) {
	creds := credentials(t, "TAPYR_E2E_RUTOS_URL", "TAPYR_E2E_RUTOS_USERNAME", "TAPYR_E2E_RUTOS_PASSWORD")
	tls := "tls: { insecureSkipVerify: true }"
	if ca := os.Getenv("TAPYR_E2E_RUTOS_CA_FILE"); ca != "" {
		tls = fmt.Sprintf("tls: { caFile: %q }", ca)
	}
	config := fmt.Sprintf(`apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: routers }
spec:
  provider: teltonika-rutos
  secrets:
    username: { env: TAPYR_E2E_RUTOS_USERNAME }
    password: { env: TAPYR_E2E_RUTOS_PASSWORD }
  %s
  targets:
    - { name: e2e, params: { baseURL: %q } }
`, tls, creds["TAPYR_E2E_RUTOS_URL"])
	for _, tmpl := range []string{"rutos-system", "rutos-device", "rutos-modems", "rutos-interfaces", "rutos-failover"} {
		config += fmt.Sprintf("---\napiVersion: tapyr/v1alpha1\nkind: Job\nmetadata: { name: %s }\nspec: { account: routers, template: %s, interval: 1m }\n", tmpl, tmpl)
	}
	secrets := map[string]string{"TAPYR_E2E_RUTOS_PASSWORD": creds["TAPYR_E2E_RUTOS_PASSWORD"]}
	addr := run(t, config, secrets)
	waitFor(t, "http://"+addr+"/metrics", 2*time.Minute,
		`(?m)^rutos_system_uptime_seconds\{router="e2e"\} \d`,
		`(?m)^rutos_memory_total_bytes\{router="e2e"\} \d`,
		`(?m)^rutos_device_info\{[^}]*firmware="[^"]+"[^}]*router="e2e"[^}]*\} 1$`,
		`(?m)^rutos_interface_up\{interface="[^"]+",router="e2e"\} [01]$`,
		`(?m)^rutos_interface_received_bytes_total\{interface="[^"]+",router="e2e"\} \d`,
	)
	waitFor(t, "http://"+addr+"/metrics/self", time.Minute,
		`(?m)^tapyr_auth_token_requests_total\{account="routers",provider="teltonika-rutos",result="success"\} [1-9]`,
		`(?m)^tapyr_job_runs_total\{[^}]*job="rutos-failover"[^}]*result="success"[^}]*\} [1-9]`,
	)
}
