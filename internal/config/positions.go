package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// maxTreeNodes bounds the size of a document after alias expansion, against
// "billion laughs" documents. Real configurations are orders of magnitude
// smaller.
const maxTreeNodes = 200_000

// maxTreeDepth bounds the nesting of a document, so that converting it never
// exhausts the stack. Real configurations nest a few dozen levels.
const maxTreeDepth = 1000

// positionIndex maps JSON pointers of a document to YAML source positions.
type positionIndex struct {
	file  string
	nodes map[string]*nodeInfo
}

type nodeInfo struct {
	key    *token.Position // position of the mapping key, if the node is a map value
	value  token.Position  // position of the node itself
	scalar bool
	raw    string // source text of a scalar
	inSeq  bool   // the node is a sequence item
}

// pointer builds an escaped JSON pointer from unescaped tokens.
func pointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

// parsePointer splits an escaped JSON pointer into unescaped tokens.
func parsePointer(ptr string) []string {
	if ptr == "" {
		return nil
	}
	tokens := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for i, t := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}
	return tokens
}

// lookup returns the node at tokens, or its closest indexed ancestor.
func (p *positionIndex) lookup(tokens []string) (*nodeInfo, bool) {
	if p == nil {
		return nil, false
	}
	for i := len(tokens); i >= 0; i-- {
		if n, ok := p.nodes[pointer(tokens[:i])]; ok {
			return n, i == len(tokens)
		}
	}
	return nil, false
}

// position returns the source position for the value at tokens. Scalars
// point to the value; mappings and sequences to their key. preferKey points
// to the key even for scalars (used for unknown fields).
func (p *positionIndex) position(tokens []string, preferKey bool) Position {
	pos := Position{}
	if p == nil {
		return pos
	}
	pos.File = p.file
	n, _ := p.lookup(tokens)
	if n == nil {
		return pos
	}
	tp := n.value
	if n.key != nil && (preferKey || !n.scalar) {
		tp = *n.key
	}
	pos.Line, pos.Column = tp.Line, tp.Column
	return pos
}

// raw returns the source text of the scalar at tokens.
func (p *positionIndex) raw(tokens []string) (string, bool) {
	n, exact := p.lookup(tokens)
	if !exact || !n.scalar {
		return "", false
	}
	return n.raw, true
}

var plainKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// path formats tokens in dotted form: spec.limits[0].rate, labels["a.b"].
func (p *positionIndex) path(tokens []string) string {
	var b strings.Builder
	for i, t := range tokens {
		inSeq := false
		if p != nil {
			if n, ok := p.nodes[pointer(tokens[:i+1])]; ok {
				inSeq = n.inSeq
			}
		}
		switch {
		case inSeq:
			b.WriteString("[" + t + "]")
		case plainKeyRe.MatchString(t):
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(t)
		default:
			b.WriteString("[" + strconv.Quote(t) + "]")
		}
	}
	return b.String()
}

// treeBuilder converts a YAML AST into a JSON-compatible tree (maps, slices,
// strings, json.Number, bools and nil) while recording positions.
type treeBuilder struct {
	index   *positionIndex
	anchors map[string]ast.Node
	// expanding holds the anchors whose node is being converted: an alias
	// to one of them is a cycle.
	expanding map[string]bool
	nodes     int
	maxNodes  int
	depth     int
}

func newTreeBuilder(file string) *treeBuilder {
	return &treeBuilder{
		index:     &positionIndex{file: file, nodes: make(map[string]*nodeInfo)},
		anchors:   make(map[string]ast.Node),
		expanding: make(map[string]bool),
		maxNodes:  maxTreeNodes,
	}
}

// treeError is a conversion error located at a YAML token.
type treeError struct {
	tok *token.Token
	msg string
}

func (e *treeError) Error() string { return e.msg }

func errAt(n ast.Node, format string, args ...any) error {
	return &treeError{tok: n.GetToken(), msg: fmt.Sprintf(format, args...)}
}

func (b *treeBuilder) record(tokens []string, n ast.Node, key *token.Position, inSeq bool) *nodeInfo {
	info := &nodeInfo{key: key, inSeq: inSeq}
	if tk := firstToken(n); tk != nil {
		info.value = *tk.Position
	}
	b.index.nodes[pointer(tokens)] = info
	return info
}

func (b *treeBuilder) convert(n ast.Node, tokens []string, key *token.Position, inSeq bool) (any, error) {
	b.nodes++
	if b.nodes > b.maxNodes {
		return nil, errAt(n, "document too large (more than %d nodes after alias expansion)", b.maxNodes)
	}
	b.depth++
	defer func() { b.depth-- }()
	if b.depth > maxTreeDepth {
		return nil, errAt(n, "document nested more than %d levels deep", maxTreeDepth)
	}
	switch n := n.(type) {
	case *ast.AnchorNode:
		name := n.Name.String()
		b.anchors[name] = n.Value
		b.expanding[name] = true
		defer delete(b.expanding, name)
		return b.convert(n.Value, tokens, key, inSeq)
	case *ast.AliasNode:
		name := n.Value.String()
		target, ok := b.anchors[name]
		if !ok {
			return nil, errAt(n, "unknown alias %q", name)
		}
		if b.expanding[name] {
			return nil, errAt(n, "alias *%s is inside the node it refers to", name)
		}
		b.expanding[name] = true
		v, err := b.convert(target, tokens, key, inSeq)
		delete(b.expanding, name)
		if err != nil {
			return nil, err
		}
		// Point at the alias itself; children keep the anchor positions.
		if info, ok := b.index.nodes[pointer(tokens)]; ok && n.GetToken() != nil {
			info.value = *n.GetToken().Position
		}
		return v, nil
	case *ast.TagNode:
		if n.Start.Value != "!!str" {
			return nil, errAt(n, "YAML tag %s is not supported (only !!str)", n.Start.Value)
		}
		info := b.record(tokens, n, key, inSeq)
		info.scalar = true
		s := n.Value.GetToken().Value
		info.raw = s
		return s, nil
	case *ast.MappingNode:
		b.record(tokens, n, key, inSeq)
		return b.mapping(n.Values, tokens)
	case *ast.MappingValueNode:
		b.record(tokens, n, key, inSeq)
		return b.mapping([]*ast.MappingValueNode{n}, tokens)
	case *ast.SequenceNode:
		b.record(tokens, n, key, inSeq)
		arr := make([]any, 0, len(n.Values))
		for i, item := range n.Values {
			v, err := b.convert(item, child(tokens, strconv.Itoa(i)), nil, true)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case *ast.MappingKeyNode:
		return b.convert(n.Value, tokens, key, inSeq)
	}
	return b.scalar(n, tokens, key, inSeq)
}

func (b *treeBuilder) scalar(n ast.Node, tokens []string, key *token.Position, inSeq bool) (any, error) {
	info := b.record(tokens, n, key, inSeq)
	info.scalar = true
	if tk := n.GetToken(); tk != nil {
		info.raw = tk.Value
	}
	switch n := n.(type) {
	case *ast.StringNode:
		return n.Value, nil
	case *ast.LiteralNode:
		return n.Value.Value, nil
	case *ast.IntegerNode:
		return json.Number(fmt.Sprint(n.Value)), nil
	case *ast.FloatNode:
		return json.Number(strconv.FormatFloat(n.Value, 'g', -1, 64)), nil
	case *ast.BoolNode:
		return n.Value, nil
	case *ast.NullNode:
		return nil, nil
	case *ast.InfinityNode, *ast.NanNode:
		return nil, errAt(n, "infinity and NaN are not supported")
	}
	return nil, errAt(n, "unsupported YAML node %s", n.Type())
}

func (b *treeBuilder) mapping(values []*ast.MappingValueNode, tokens []string) (any, error) {
	obj := make(map[string]any, len(values))
	var merges []map[string]any
	for _, mv := range values {
		if mv.Key.IsMergeKey() {
			merged, err := b.mergeSources(mv.Value, tokens)
			if err != nil {
				return nil, err
			}
			merges = append(merges, merged...)
			continue
		}
		k, err := keyText(mv.Key)
		if err != nil {
			return nil, err
		}
		var keyPos *token.Position
		if tk := mv.Key.GetToken(); tk != nil {
			keyPos = tk.Position
		}
		v, err := b.convert(mv.Value, child(tokens, k), keyPos, false)
		if err != nil {
			return nil, err
		}
		obj[k] = v
	}
	// Explicit keys win over merged keys; earlier merge sources win over
	// later ones (YAML merge key semantics).
	for _, m := range merges {
		for k, v := range m {
			if _, exists := obj[k]; !exists {
				obj[k] = v
			}
		}
	}
	return obj, nil
}

// mergeSources resolves the value of a "<<" key: a mapping, an alias to a
// mapping, or a sequence of those.
func (b *treeBuilder) mergeSources(n ast.Node, tokens []string) ([]map[string]any, error) {
	var nodes []ast.Node
	if seq, ok := n.(*ast.SequenceNode); ok {
		nodes = seq.Values
	} else {
		nodes = []ast.Node{n}
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, item := range nodes {
		// Merged entries are recorded under a scratch builder so they do not
		// overwrite positions of explicit keys; lookups fall back to the
		// enclosing mapping.
		scratch := &treeBuilder{
			index:     &positionIndex{file: b.index.file, nodes: map[string]*nodeInfo{}},
			anchors:   b.anchors,
			expanding: b.expanding,
			nodes:     b.nodes,
			maxNodes:  b.maxNodes,
			depth:     b.depth,
		}
		v, err := scratch.convert(item, tokens, nil, false)
		b.nodes = scratch.nodes
		if err != nil {
			return nil, err
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errAt(item, "merge key value must be a mapping")
		}
		out = append(out, m)
	}
	return out, nil
}

func keyText(k ast.MapKeyNode) (string, error) {
	var n ast.Node = k
	if mk, ok := n.(*ast.MappingKeyNode); ok {
		n = mk.Value
	}
	switch n := n.(type) {
	case *ast.StringNode:
		return n.Value, nil
	case *ast.IntegerNode, *ast.FloatNode, *ast.BoolNode, *ast.NullNode:
		return n.GetToken().Value, nil
	}
	return "", errAt(n, "mapping keys must be scalars")
}

// firstToken returns the token a node is reported at: the first key of a
// block mapping, the node token otherwise.
func firstToken(n ast.Node) *token.Token {
	switch n := n.(type) {
	case *ast.MappingNode:
		if len(n.Values) > 0 && !n.IsFlowStyle {
			return n.Values[0].Key.GetToken()
		}
	case *ast.MappingValueNode:
		return n.Key.GetToken()
	}
	return n.GetToken()
}

// child returns tokens extended with t, never sharing the backing array.
func child(tokens []string, t string) []string {
	return append(slices.Clip(tokens), t)
}

// asTreeError extracts the position of a conversion error.
func asTreeError(err error) (*treeError, bool) {
	var te *treeError
	ok := errors.As(err, &te)
	return te, ok
}
