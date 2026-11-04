//go:build e2e

package e2e

import (
	"regexp"
	"strconv"
	"testing"
	"time"
)

// TestTeltonikaRMS runs the teltonika-rms package against the real platform
// for one cycle: the device list (one page per 100 devices) and today's data
// usage of every device (one request each). The samples have the expected shape and the monthly quota
// is learned from the response headers.
func TestTeltonikaRMS(t *testing.T) {
	secrets := credentials(t, "TAPYR_E2E_RMS_TOKEN")
	addr := run(t, `apiVersion: tapyr/v1alpha1
kind: Account
metadata: { name: rms }
spec:
  provider: teltonika-rms
  secrets: { token: { env: TAPYR_E2E_RMS_TOKEN } }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: devices }
spec: { account: rms, template: rms-devices, interval: 1h }
---
apiVersion: tapyr/v1alpha1
kind: Job
metadata: { name: data-usage }
spec: { account: rms, template: rms-device-data-usage, interval: 1m } # instances spread over the interval
`, secrets)
	metrics := waitFor(t, "http://"+addr+"/metrics", time.Minute,
		`(?m)^rms_devices [1-9]`,
		`(?m)^rms_device_up\{device_id="\d+"\} [01]$`,
		`(?m)^rms_device_info\{company="[^"]*",device_id="\d+",firmware="[^"]+",model="[^"]+",name="[^"]*",serial="[^"]*"\} 1$`,
		`(?m)^rms_device_last_update_timestamp_seconds\{device_id="\d+"\} \d`,
	)
	devices, _ := strconv.Atoi(regexp.MustCompile(`(?m)^rms_devices (\d+)`).FindStringSubmatch(metrics)[1])
	if n := len(regexp.MustCompile(`(?m)^rms_device_up\{`).FindAllString(metrics, -1)); n != devices {
		t.Errorf("%d devices with a status, rms_devices says %d", n, devices)
	}
	self := waitFor(t, "http://"+addr+"/metrics/self", 2*time.Minute,
		`(?m)^tapyr_budget_limit\{limit="monthly",provider="teltonika-rms",scope="[^"]+"\} 100000$`,
		`(?m)^tapyr_budget_remaining_observed\{limit="monthly",provider="teltonika-rms",scope="[^"]+"\} \d+`,
		`(?m)^tapyr_job_runs_total\{[^}]*job="data-usage"[^}]*result="success"[^}]*\} [1-9]`,
	)
	if regexp.MustCompile(`(?m)^tapyr_job_runs_total\{[^}]*result="failed"`).MatchString(self) {
		t.Errorf("failed runs:\n%s", self)
	}
}
