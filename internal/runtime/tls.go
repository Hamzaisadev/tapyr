package runtime

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/kentrow/tapyr/internal/config"
)

// AccountTLS is the TLS configuration of an account towards its provider.
type AccountTLS struct {
	// Config is used by every request of the account, auth requests
	// included.
	Config *tls.Config
	// Insecure reports that certificate verification is disabled.
	Insecure bool
	// Fingerprint identifies the settings and the CA bundle, so that an
	// unchanged account keeps its connections across reloads.
	Fingerprint string
}

// compileTLS builds the TLS configuration of an account: the system roots
// plus caFile, or no verification with insecureSkipVerify.
func (c *compiler) compileTLS(a *config.Account) {
	t := a.Spec.TLS
	if t == nil || (t.CAFile == "" && !t.InsecureSkipVerify) {
		return
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	sum := sha256.New()
	fmt.Fprintf(sum, "insecure=%t\n", t.InsecureSkipVerify)
	if t.CAFile != "" {
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			c.errorf(a.Source, []string{"spec", "tls", "caFile"}, "reading the CA bundle: %v", err)
			return
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			c.errorf(a.Source, []string{"spec", "tls", "caFile"}, "%s holds no PEM certificate", t.CAFile)
			return
		}
		cfg.RootCAs = pool
		sum.Write(pem)
	}
	if t.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true // explicit per-account opt-in, logged at every load
	}
	if c.graph.TLS == nil {
		c.graph.TLS = map[string]*AccountTLS{}
	}
	c.graph.TLS[string(a.Metadata.Name)] = &AccountTLS{
		Config: cfg, Insecure: t.InsecureSkipVerify, Fingerprint: hex.EncodeToString(sum.Sum(nil)[:8]),
	}
}
