// Package mapping compiles and evaluates the CEL expressions of provider
// definitions and jobs (see docs/spec/06-mapping-and-outputs.md and ADR
// 0002). Every expression is compiled in a context that declares only the
// variables meaningful for its field.
package mapping

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/ext"
	"cel.dev/cel-go/interpreter"
)

// Context selects the variables available to an expression.
type Context int

// Expression contexts, see the table in 06-mapping-and-outputs.md.
const (
	// ContextPage: extract.items and pagination expressions.
	ContextPage Context = iota
	// ContextItem: extract.filter, metrics, info and inventory.
	ContextItem
	// ContextPost: extract.post.
	ContextPost
	// ContextParent: forEach.vars and forEach.filter.
	ContextParent
	// ContextRequest: request query and body values.
	ContextRequest
	// ContextLogin: sessionLogin token and expiresIn.
	ContextLogin
)

var contextVars = map[Context][]string{
	ContextPage:    {"response", "vars", "account", "target", "now"},
	ContextItem:    {"response", "vars", "account", "target", "now", "item"},
	ContextPost:    {"responses", "vars", "account", "target", "now"},
	ContextParent:  {"parent", "account", "target", "now"},
	ContextRequest: {"vars", "account", "target", "now"},
	ContextLogin:   {"response", "now"},
}

var varTypes = map[string]*cel.Type{
	"response":  cel.MapType(cel.StringType, cel.DynType),
	"responses": cel.ListType(cel.DynType),
	"item":      cel.DynType,
	"vars":      cel.MapType(cel.StringType, cel.StringType),
	"parent":    cel.MapType(cel.StringType, cel.DynType),
	"account":   cel.MapType(cel.StringType, cel.DynType),
	"target":    cel.MapType(cel.StringType, cel.DynType),
	"now":       cel.TimestampType,
}

// Output constrains the result type of an expression at compile time.
// Expressions returning dyn (for example a field of a JSON body) are always
// accepted and checked at runtime.
type Output int

// Expected outputs.
const (
	OutputAny Output = iota
	OutputList
	OutputBool
	OutputNumber // int, uint, double or bool
	OutputString // string, or a scalar converted to string
	OutputTimestamp
	OutputDuration
	OutputMap
)

var outputTypes = map[Output][]*cel.Type{
	OutputList:      {cel.ListType(cel.DynType)},
	OutputBool:      {cel.BoolType},
	OutputNumber:    {cel.IntType, cel.UintType, cel.DoubleType, cel.BoolType},
	OutputString:    {cel.StringType, cel.IntType, cel.UintType, cel.DoubleType, cel.BoolType, cel.NullType},
	OutputTimestamp: {cel.TimestampType, cel.NullType},
	OutputDuration:  {cel.DurationType},
	OutputMap:       {cel.MapType(cel.StringType, cel.DynType), cel.NullType},
}

var outputNames = map[Output]string{
	OutputList:      "a list",
	OutputBool:      "a bool",
	OutputNumber:    "a number or a bool",
	OutputString:    "a string",
	OutputTimestamp: "a timestamp",
	OutputDuration:  "a duration",
	OutputMap:       "a map or null",
}

// Env compiles expressions. It is safe for concurrent use.
type Env struct {
	envs     map[Context]*cel.Env
	progOpts []cel.ProgramOption
}

// NewEnv returns an environment whose programs stop after maxCost units of
// CEL cost (settings.mapping.maxCost).
func NewEnv(maxCost int64) (*Env, error) {
	base, err := cel.NewEnv(
		cel.OptionalTypes(),
		ext.Strings(),
		ext.Lists(),
		ext.Math(),
		cel.Lib(functions{}),
	)
	if err != nil {
		return nil, fmt.Errorf("creating the CEL environment: %w", err)
	}
	e := &Env{envs: make(map[Context]*cel.Env, len(contextVars))}
	for ctx, names := range contextVars {
		opts := make([]cel.EnvOption, 0, len(names))
		for _, n := range names {
			opts = append(opts, cel.Variable(n, varTypes[n]))
		}
		if e.envs[ctx], err = base.Extend(opts...); err != nil {
			return nil, fmt.Errorf("creating the CEL environment: %w", err)
		}
	}
	if maxCost > 0 {
		e.progOpts = append(e.progOpts, cel.CostLimit(uint64(maxCost)))
	}
	return e, nil
}

// Program is a compiled expression.
type Program struct {
	prg cel.Program
	src string
}

// Source returns the expression text.
func (p *Program) Source() string { return p.src }

// Compile parses and type-checks an expression in a context.
func (e *Env) Compile(ctx Context, src string, out Output) (*Program, error) {
	env := e.envs[ctx]
	ast, iss := env.Compile(src)
	if iss != nil && iss.Err() != nil {
		return nil, compileError(iss)
	}
	if allowed, ok := outputTypes[out]; ok {
		got := ast.OutputType()
		if !isDyn(got) && !slices.ContainsFunc(allowed, func(want *cel.Type) bool { return want.IsAssignableType(got) }) {
			return nil, fmt.Errorf("expression must return %s, not %s", outputNames[out], got)
		}
	}
	prg, err := env.Program(ast, e.progOpts...)
	if err != nil {
		return nil, fmt.Errorf("building the expression: %w", err)
	}
	return &Program{prg: prg, src: src}, nil
}

func isDyn(t *cel.Type) bool {
	return t.Kind() == types.DynKind || t.Kind() == types.AnyKind
}

func compileError(iss *cel.Issues) error {
	msgs := make([]string, 0, len(iss.Errors()))
	for _, e := range iss.Errors() {
		loc := e.Location
		switch {
		case loc == nil || loc.Line() <= 0:
			msgs = append(msgs, e.Message)
		case loc.Line() == 1:
			msgs = append(msgs, fmt.Sprintf("%s (at column %d)", e.Message, loc.Column()+1))
		default:
			msgs = append(msgs, fmt.Sprintf("%s (at line %d, column %d)", e.Message, loc.Line(), loc.Column()+1))
		}
	}
	return fmt.Errorf("invalid expression: %s", strings.Join(msgs, "; "))
}

// Activation holds the variables of an evaluation. Only the variables of
// the program's context are read.
type Activation struct {
	Response  map[string]any
	Responses []any
	Item      any
	Vars      map[string]string
	Parent    map[string]any
	Account   map[string]any
	Target    map[string]any
	Now       time.Time
}

// activation adapts Activation to interpreter.Activation.
type activation struct{ a *Activation }

// ResolveName implements interpreter.Activation.
func (x activation) ResolveName(name string) (any, bool) {
	a := x.a
	switch name {
	case "response":
		return a.Response, true
	case "responses":
		return a.Responses, true
	case "item":
		return a.Item, true
	case "vars":
		if a.Vars == nil {
			return map[string]string{}, true
		}
		return a.Vars, true
	case "parent":
		return orEmpty(a.Parent), true
	case "account":
		return orEmpty(a.Account), true
	case "target":
		return orEmpty(a.Target), true
	case "now":
		return a.Now, true
	}
	return nil, false
}

// Parent implements interpreter.Activation.
func (activation) Parent() interpreter.Activation { return nil }

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Eval evaluates the program.
func (p *Program) Eval(a *Activation) (ref.Val, error) {
	out, _, err := p.prg.Eval(activation{a})
	if err != nil {
		return nil, err
	}
	return out, nil
}
