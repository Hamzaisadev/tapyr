package config

import "github.com/invopop/jsonschema"

// Account instantiates a provider with credentials, params and labels.
type Account struct {
	APIVersion string      `json:"apiVersion" jsonschema:"required"`
	Kind       string      `json:"kind" jsonschema:"required"`
	Metadata   ObjectMeta  `json:"metadata" jsonschema:"required"`
	Spec       AccountSpec `json:"spec" jsonschema:"required"`

	Source *Source `json:"-"`
}

// JSONSchemaExtend implements the schema reflector hook.
func (Account) JSONSchemaExtend(s *jsonschema.Schema) { extendEnvelope(s, KindAccount) }

func (a *Account) kind() Kind      { return KindAccount }
func (a *Account) name() string    { return string(a.Metadata.Name) }
func (a *Account) source() *Source { return a.Source }

// AccountSpec is the specification of an Account.
type AccountSpec struct {
	// Provider instantiated by the account.
	Provider Name `json:"provider" jsonschema:"required"`
	// Params declared by the provider.
	Params map[string]string `json:"params,omitempty"`
	// Secrets declared by the provider, as references to environment
	// variables or files.
	Secrets map[string]SecretRef `json:"secrets,omitempty"`
	// Labels added to every sample from this account.
	Labels map[string]string `json:"labels,omitempty"`
	// Targets of a multi-target account (for example one entry per device).
	// Jobs run once per target.
	Targets []Target `json:"targets,omitempty"`
	// TLS settings for connections to the provider.
	TLS *TLSClient `json:"tls,omitempty"`
	// Budget share of this account.
	Budget *AccountBudget `json:"budget,omitempty"`
}

// JSONSchemaExtend implements the schema reflector hook.
func (AccountSpec) JSONSchemaExtend(s *jsonschema.Schema) { paramMaps(s) }

// Target is one endpoint of a multi-target account.
type Target struct {
	// Name of the target, unique within the account.
	Name Name `json:"name" jsonschema:"required"`
	// Params overriding the account params.
	Params map[string]string `json:"params,omitempty"`
	// Labels added to every sample from this target.
	Labels map[string]string `json:"labels,omitempty"`
	// Secrets overriding the account secrets.
	Secrets map[string]SecretRef `json:"secrets,omitempty"`
}

// JSONSchemaExtend implements the schema reflector hook.
func (Target) JSONSchemaExtend(s *jsonschema.Schema) { paramMaps(s) }

// TLSClient configures TLS towards the provider.
type TLSClient struct {
	// InsecureSkipVerify disables certificate verification. Logged loudly.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
	// CAFile is a PEM bundle of trusted CAs, relative to the declaring file.
	CAFile string `json:"caFile,omitempty"`
}

// AccountBudget configures the share of quotas the account may use.
type AccountBudget struct {
	// Share of every quota limit Tapyr may use, when other consumers share
	// the same credentials. In (0, 1].
	Share *float64 `json:"share,omitempty" jsonschema:"exclusiveMinimum=0,maximum=1,default=1"`
	// ScaleRates also scales rate and concurrency limits by the share.
	ScaleRates bool `json:"scaleRates,omitempty"`
}

// paramMaps constrains the keys of params, secrets and labels maps.
func paramMaps(s *jsonschema.Schema) {
	for _, k := range []string{"params", "secrets"} {
		if p, ok := s.Properties.Get(k); ok {
			p.PropertyNames = &jsonschema.Schema{Pattern: identifierPattern}
		}
	}
	labelKeys(s)
}
