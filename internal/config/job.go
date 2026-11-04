package config

import (
	"slices"

	"github.com/invopop/jsonschema"
)

// JobTemplate is a reusable job definition shipped by a provider package.
type JobTemplate struct {
	APIVersion string          `json:"apiVersion" jsonschema:"required"`
	Kind       string          `json:"kind" jsonschema:"required"`
	Metadata   ObjectMeta      `json:"metadata" jsonschema:"required"`
	Spec       JobTemplateSpec `json:"spec" jsonschema:"required"`

	Source *Source `json:"-"`
}

// JSONSchemaExtend implements the schema reflector hook.
func (JobTemplate) JSONSchemaExtend(s *jsonschema.Schema) { extendEnvelope(s, KindJobTemplate) }

func (t *JobTemplate) kind() Kind      { return KindJobTemplate }
func (t *JobTemplate) name() string    { return string(t.Metadata.Name) }
func (t *JobTemplate) source() *Source { return t.Source }

// JobTemplateSpec is the specification of a JobTemplate.
type JobTemplateSpec struct {
	// Provider the template belongs to.
	Provider Name `json:"provider" jsonschema:"required"`
	// DefaultInterval is the desired interval of jobs using the template.
	DefaultInterval Duration `json:"defaultInterval" jsonschema:"required"`
	// DefaultPriority of jobs using the template.
	DefaultPriority Priority `json:"defaultPriority,omitempty"`

	JobDefinition
}

// JSONSchemaExtend implements the schema reflector hook.
func (JobTemplateSpec) JSONSchemaExtend(s *jsonschema.Schema) {
	s.Required = append(s.Required, "request")
}

// Job collects data for an account, from a template or from an inline
// definition.
type Job struct {
	APIVersion string     `json:"apiVersion" jsonschema:"required"`
	Kind       string     `json:"kind" jsonschema:"required"`
	Metadata   ObjectMeta `json:"metadata" jsonschema:"required"`
	Spec       JobSpec    `json:"spec" jsonschema:"required"`

	Source *Source `json:"-"`
}

// JSONSchemaExtend implements the schema reflector hook.
func (Job) JSONSchemaExtend(s *jsonschema.Schema) { extendEnvelope(s, KindJob) }

func (j *Job) kind() Kind      { return KindJob }
func (j *Job) name() string    { return string(j.Metadata.Name) }
func (j *Job) source() *Source { return j.Source }

// JobSpec is the specification of a Job. With template, only interval,
// priority, outputs, labels and staleAfter may be set; without template,
// the definition fields are inline and interval is required.
type JobSpec struct {
	// Account the job collects for.
	Account Name `json:"account" jsonschema:"required"`
	// Template the job instantiates.
	Template Name `json:"template,omitempty"`
	// Interval is the desired interval; the planner may stretch it.
	// Defaults to the template's defaultInterval.
	Interval Duration `json:"interval,omitempty"`
	// Priority of the job. Defaults to the template's defaultPriority, else normal.
	Priority Priority `json:"priority,omitempty"`
	// Outputs receiving the job's samples. Default: all outputs.
	Outputs []Name `json:"outputs,omitempty"`
	// Labels added to every sample of the job.
	Labels map[string]string `json:"labels,omitempty"`
	// StaleAfter removes series of failing runs after this delay. Default:
	// 3x the effective interval.
	StaleAfter Duration `json:"staleAfter,omitempty"`

	JobDefinition
}

// JobDefinition holds the fields shared by JobTemplate and inline Job.
type JobDefinition struct {
	// Role of the job: inventory items, metrics, or both.
	Role JobRole `json:"role,omitempty"`
	// Request to send.
	Request *Request `json:"request,omitempty"`
	// Pagination overrides the provider default.
	Pagination *Pagination `json:"pagination,omitempty"`
	// ForEach fans the job out over the inventory items of a parent.
	ForEach *ForEach `json:"forEach,omitempty"`
	// Estimate gives hints to the planner before run history exists.
	Estimate *Estimate `json:"estimate,omitempty"`
	// Extract maps responses to samples and inventory items.
	Extract *Extract `json:"extract,omitempty"`
}

// definitionFields returns the JSON names of the definition fields that are set.
func (d *JobDefinition) definitionFields() []string {
	var set []string
	if d.Role != "" {
		set = append(set, "role")
	}
	if d.Request != nil {
		set = append(set, "request")
	}
	if d.Pagination != nil {
		set = append(set, "pagination")
	}
	if d.ForEach != nil {
		set = append(set, "forEach")
	}
	if d.Estimate != nil {
		set = append(set, "estimate")
	}
	if d.Extract != nil {
		set = append(set, "extract")
	}
	return set
}

// JobRole is the role of a job in discovery.
type JobRole string

// Job roles.
const (
	RoleInventory JobRole = "inventory"
	RoleMetrics   JobRole = "metrics"
	RoleBoth      JobRole = "both"
)

// JSONSchema implements the schema reflector hook.
func (JobRole) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Enum: []any{"inventory", "metrics", "both"}, Default: "metrics"}
}

// producesInventory reports whether a job with this role can be a parent.
func (r JobRole) producesInventory() bool { return r == RoleInventory || r == RoleBoth }

// Priority orders jobs when the budget is tight.
type Priority string

// Priorities, from highest to lowest.
const (
	PriorityCritical Priority = "critical"
	PriorityHigh     Priority = "high"
	PriorityNormal   Priority = "normal"
	PriorityLow      Priority = "low"
)

// JSONSchema implements the schema reflector hook.
func (Priority) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Enum: []any{"critical", "high", "normal", "low"}}
}

// Request describes the HTTP request of a job.
type Request struct {
	// Method of the request. POST is allowed only for read-only queries.
	Method string `json:"method,omitempty" jsonschema:"enum=GET,enum=HEAD,enum=POST,default=GET"`
	// Path relative to the base URL, with {var} placeholders resolved from
	// discovery variables, target params and account params.
	Path string `json:"path" jsonschema:"required,pattern=^/"`
	// Query parameters; values are literals, secrets or CEL expressions.
	Query map[string]Value `json:"query,omitempty"`
	// Headers merged over the provider defaults (the job wins).
	Headers map[string]string `json:"headers,omitempty"`
	// Body is a JSON object for POST queries; values are literals, secrets or
	// CEL expressions.
	Body map[string]Value `json:"body,omitempty"`
	// ReadOnly must be true for POST requests: Tapyr never writes to providers.
	ReadOnly bool `json:"readOnly,omitempty"`
	// Idempotent allows retrying POST requests.
	Idempotent bool `json:"idempotent,omitempty"`
	// Timeout of a single request. Defaults to the provider default.
	Timeout Duration `json:"timeout,omitempty"`
}

// PaginationType selects a pagination strategy.
type PaginationType string

// Pagination types.
const (
	PaginationNone       PaginationType = "none"
	PaginationOffset     PaginationType = "offset"
	PaginationPage       PaginationType = "page"
	PaginationLinkHeader PaginationType = "link_header"
	PaginationCursor     PaginationType = "cursor"
	PaginationExpression PaginationType = "expression"
)

// paginationBlocks maps each pagination type to its configuration block.
var paginationBlocks = map[PaginationType]string{
	PaginationOffset:     "offset",
	PaginationPage:       "page",
	PaginationLinkHeader: "linkHeader",
	PaginationCursor:     "cursor",
	PaginationExpression: "expression",
}

// Pagination configures how successive pages are requested. Every page is a
// budgeted request.
type Pagination struct {
	// Type of pagination.
	Type PaginationType `json:"type" jsonschema:"required,enum=none,enum=offset,enum=page,enum=link_header,enum=cursor,enum=expression"`
	// MaxPages is a hard safety cap on pages per run.
	MaxPages int `json:"maxPages,omitempty" jsonschema:"minimum=1,default=100"`
	// Offset pagination (type offset).
	Offset *OffsetPagination `json:"offset,omitempty"`
	// Page pagination (type page).
	Page *PagePagination `json:"page,omitempty"`
	// LinkHeader pagination following RFC 8288 Link headers (type link_header).
	LinkHeader *LinkHeaderPagination `json:"linkHeader,omitempty"`
	// Cursor pagination (type cursor).
	Cursor *CursorPagination `json:"cursor,omitempty"`
	// Expression pagination computed by CEL (type expression).
	Expression *ExpressionPagination `json:"expression,omitempty"`
}

// JSONSchemaExtend implements the schema reflector hook.
func (Pagination) JSONSchemaExtend(s *jsonschema.Schema) {
	for _, t := range []PaginationType{PaginationOffset, PaginationPage, PaginationCursor, PaginationExpression} {
		requireWhen(s, "type", string(t), paginationBlocks[t])
	}
}

// OffsetPagination requests pages by item offset.
type OffsetPagination struct {
	// OffsetParam is the query parameter carrying the offset.
	OffsetParam string `json:"offsetParam" jsonschema:"required,minLength=1"`
	// LimitParam is the query parameter carrying the page size.
	LimitParam string `json:"limitParam" jsonschema:"required,minLength=1"`
	// PageSize is the number of items per page.
	PageSize int `json:"pageSize" jsonschema:"required,minimum=1"`
	// Start is the first offset.
	Start int `json:"start,omitempty" jsonschema:"minimum=0,default=0"`
	// Stop is a CEL expression; pagination stops when it returns true.
	Stop Expr `json:"stop,omitempty"`
	// Total is a CEL expression returning the total number of items.
	Total Expr `json:"total,omitempty"`
}

// PagePagination requests pages by page number.
type PagePagination struct {
	// PageParam is the query parameter carrying the page number.
	PageParam string `json:"pageParam" jsonschema:"required,minLength=1"`
	// SizeParam is the query parameter carrying the page size.
	SizeParam string `json:"sizeParam,omitempty"`
	// PageSize is the number of items per page.
	PageSize int `json:"pageSize,omitempty" jsonschema:"minimum=1"`
	// Start is the first page number.
	Start *int `json:"start,omitempty" jsonschema:"minimum=0,default=1"`
	// Stop is a CEL expression; pagination stops when it returns true.
	Stop Expr `json:"stop,omitempty"`
	// Total is a CEL expression returning the total number of items.
	Total Expr `json:"total,omitempty"`
}

// LinkHeaderPagination follows RFC 8288 Link headers.
type LinkHeaderPagination struct {
	// PageSizeParam is the query parameter carrying the page size.
	PageSizeParam string `json:"pageSizeParam,omitempty"`
	// PageSize is the number of items per page.
	PageSize int `json:"pageSize,omitempty" jsonschema:"minimum=1"`
	// Rel is the link relation of the next page.
	Rel string `json:"rel,omitempty" jsonschema:"default=next"`
}

// CursorPagination passes a cursor extracted from each response.
type CursorPagination struct {
	// Cursor is a CEL expression extracting the next cursor; empty or null stops.
	Cursor Expr `json:"cursor" jsonschema:"required"`
	// Param is the query parameter carrying the cursor.
	Param string `json:"param,omitempty"`
	// Header carrying the cursor, instead of a query parameter.
	Header string `json:"header,omitempty"`
	// PageSizeParam is the query parameter carrying the page size.
	PageSizeParam string `json:"pageSizeParam,omitempty"`
	// PageSize is the number of items per page.
	PageSize int `json:"pageSize,omitempty" jsonschema:"minimum=1"`
}

// ExpressionPagination computes the next request with CEL.
type ExpressionPagination struct {
	// Next is a CEL expression returning {query: {...}} for the next page, or null.
	Next Expr `json:"next" jsonschema:"required"`
}

// ForEach fans a job out over the inventory items of a parent job.
type ForEach struct {
	// Template is the parent JobTemplate, instantiated implicitly for the
	// account when no job references it.
	Template Name `json:"template,omitempty"`
	// Job is the parent Job (same account), for inline jobs.
	Job Name `json:"job,omitempty"`
	// Vars maps request variables to CEL expressions on parent.
	Vars map[string]Expr `json:"vars,omitempty"`
	// Filter is a CEL expression on parent selecting a subset of items.
	Filter Expr `json:"filter,omitempty"`
	// MaxInstances caps the fan-out, against runaway discovery.
	MaxInstances int `json:"maxInstances,omitempty" jsonschema:"minimum=1,default=10000"`
}

// JSONSchemaExtend implements the schema reflector hook.
func (ForEach) JSONSchemaExtend(s *jsonschema.Schema) {
	if p, ok := s.Properties.Get("vars"); ok {
		p.PropertyNames = &jsonschema.Schema{Pattern: identifierPattern}
	}
}

// Estimate gives hints to the planner.
type Estimate struct {
	// PagesPerRun is the expected number of pages per run.
	PagesPerRun int `json:"pagesPerRun,omitempty" jsonschema:"minimum=1"`
}

// Extract maps responses to samples and inventory items.
type Extract struct {
	// Items is a CEL expression returning the list of items of a page.
	// Default: response.body when it is a list.
	Items Expr `json:"items,omitempty"`
	// Filter is a CEL expression on item; false skips the item.
	Filter Expr `json:"filter,omitempty"`
	// Partial emits the samples of a run even when a later page fails.
	Partial bool `json:"partial,omitempty"`
	// Metrics produced per item.
	Metrics []Metric `json:"metrics,omitempty"`
	// Info metrics (value 1, descriptive labels) produced per item.
	Info []InfoMetric `json:"info,omitempty"`
	// Inventory items produced per item (role inventory or both).
	Inventory *Inventory `json:"inventory,omitempty"`
	// Post metrics computed once per run over all pages (responses).
	Post []Metric `json:"post,omitempty"`
}

// MetricType is the type of a metric.
type MetricType string

// Metric types.
const (
	MetricGauge   MetricType = "gauge"
	MetricCounter MetricType = "counter"
)

// Metric maps items to samples of one metric.
type Metric struct {
	// Name of the metric (Prometheus naming rules).
	Name string `json:"name" jsonschema:"required"`
	// Type of the metric.
	Type MetricType `json:"type,omitempty" jsonschema:"enum=gauge,enum=counter,default=gauge"`
	// Help text of the metric.
	Help string `json:"help,omitempty"`
	// Unit of the metric, preferably a Prometheus base unit.
	Unit string `json:"unit,omitempty"`
	// Value is a CEL expression returning a number or a bool.
	Value Expr `json:"value" jsonschema:"required"`
	// Labels maps label names to CEL expressions returning strings.
	Labels map[string]Expr `json:"labels,omitempty"`
	// Timestamp is a CEL expression returning the source timestamp.
	Timestamp Expr `json:"timestamp,omitempty"`
	// SkipIf is a CEL expression; true skips the sample.
	SkipIf Expr `json:"skipIf,omitempty"`
	// Accumulate sums successive window values into a persisted counter
	// (type counter only).
	Accumulate bool `json:"accumulate,omitempty"`
	// WindowField is a CEL expression identifying the window of a value,
	// used by accumulate to avoid counting a window twice.
	WindowField Expr `json:"windowField,omitempty"`
}

// JSONSchemaExtend implements the schema reflector hook.
func (Metric) JSONSchemaExtend(s *jsonschema.Schema) {
	setPattern(s, "name", metricNamePattern)
	labelKeys(s)
}

// InfoMetric produces an _info metric: value 1 and descriptive labels.
type InfoMetric struct {
	// Name of the metric, ending with _info.
	Name string `json:"name" jsonschema:"required"`
	// Help text of the metric.
	Help string `json:"help,omitempty"`
	// Labels maps label names to CEL expressions returning strings.
	Labels map[string]Expr `json:"labels" jsonschema:"required"`
	// SkipIf is a CEL expression; true skips the sample.
	SkipIf Expr `json:"skipIf,omitempty"`
}

// JSONSchemaExtend implements the schema reflector hook.
func (InfoMetric) JSONSchemaExtend(s *jsonschema.Schema) {
	setPattern(s, "name", infoNamePattern)
	labelKeys(s)
}

// Inventory produces inventory items for child jobs.
type Inventory struct {
	// Key is a CEL expression returning the unique key of an item.
	Key Expr `json:"key" jsonschema:"required"`
	// Fields maps field names to CEL expressions, available as parent.<field>.
	Fields map[string]Expr `json:"fields,omitempty"`
	// TTL of cached items. Default: 3x the job's effective interval.
	TTL Duration `json:"ttl,omitempty"`
}

// JSONSchemaExtend implements the schema reflector hook.
func (Inventory) JSONSchemaExtend(s *jsonschema.Schema) {
	if p, ok := s.Properties.Get("fields"); ok {
		p.PropertyNames = &jsonschema.Schema{Pattern: identifierPattern}
	}
}

func labelKeys(s *jsonschema.Schema) {
	if p, ok := s.Properties.Get("labels"); ok {
		p.PropertyNames = &jsonschema.Schema{Pattern: labelNamePattern}
	}
}

// sortedKeys returns the keys of m in sorted order, for deterministic schemas.
func sortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
