package graph

import (
	"context"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/jlentink/bekup/internal/graph/apierror"
)

func init() {
	// Limit violations are protocol errors (HTTP 422), like validation.
	errcode.RegisterErrorType(string(apierror.QueryTooDeep), errcode.KindProtocol)
	errcode.RegisterErrorType(string(apierror.IntrospectionDisabled), errcode.KindProtocol)
	errcode.RegisterErrorType(string(apierror.QueryTooComplex), errcode.KindProtocol)
}

func protocolError(code apierror.Code, msg string) *gqlerror.Error {
	return &gqlerror.Error{Message: msg, Extensions: map[string]any{ExtCode: string(code)}}
}

// --- depth limit -----------------------------------------------------------

// depthLimit rejects operations nested deeper than Max fields. Fragments
// count at the depth where they are spread. Introspection meta fields
// (__schema, __type, __typename) are not counted: they are only reachable
// when introspection is allowed and are bounded by the parser token limit
// and the complexity limit.
type depthLimit struct{ Max int }

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = depthLimit{}

func (depthLimit) ExtensionName() string { return "DepthLimit" }

func (depthLimit) Validate(graphql.ExecutableSchema) error { return nil }

func (d depthLimit) MutateOperationContext(_ context.Context, opCtx *graphql.OperationContext) *gqlerror.Error {
	op := opCtx.Doc.Operations.ForName(opCtx.OperationName)
	if op == nil {
		return nil
	}
	w := depthWalker{doc: opCtx.Doc, memo: map[string]int{}, active: map[string]bool{}}
	if got := w.depth(op.SelectionSet); got > d.Max {
		return protocolError(apierror.QueryTooDeep, "operation exceeds the maximum depth")
	}
	return nil
}

// QueryDepth returns the depth of op as the depth limit counts it.
func QueryDepth(doc *ast.QueryDocument, op *ast.OperationDefinition) int {
	w := depthWalker{doc: doc, memo: map[string]int{}, active: map[string]bool{}}
	return w.depth(op.SelectionSet)
}

type depthWalker struct {
	doc    *ast.QueryDocument
	memo   map[string]int
	active map[string]bool
}

func (w *depthWalker) depth(set ast.SelectionSet) int {
	maxDepth := 0
	for _, sel := range set {
		var d int
		switch s := sel.(type) {
		case *ast.Field:
			if strings.HasPrefix(s.Name, "__") {
				continue
			}
			d = 1 + w.depth(s.SelectionSet)
		case *ast.InlineFragment:
			d = w.depth(s.SelectionSet)
		case *ast.FragmentSpread:
			d = w.fragment(s.Name)
		}
		maxDepth = max(maxDepth, d)
	}
	return maxDepth
}

// fragment memoizes per fragment (its depth does not depend on where it is
// spread), so repeated spreads cost nothing; a cycle (already rejected by
// validation) counts as 0.
func (w *depthWalker) fragment(name string) int {
	if d, ok := w.memo[name]; ok {
		return d
	}
	if w.active[name] {
		return 0
	}
	f := w.doc.Fragments.ForName(name)
	if f == nil {
		return 0
	}
	w.active[name] = true
	d := w.depth(f.SelectionSet)
	delete(w.active, name)
	w.memo[name] = d
	return d
}

// --- introspection gate ----------------------------------------------------

// introspectionGate allows introspection only when Allowed(ctx) (dev mode
// or an authenticated user, D-050). A denied operation that selects
// __schema or __type fails as a whole with INTROSPECTION_DISABLED; gqlgen's
// own DisableIntrospection flag stays set as a backstop.
type introspectionGate struct {
	Allowed func(ctx context.Context) bool
}

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = introspectionGate{}

func (introspectionGate) ExtensionName() string { return "IntrospectionGate" }

func (introspectionGate) Validate(graphql.ExecutableSchema) error { return nil }

func (g introspectionGate) MutateOperationContext(ctx context.Context, opCtx *graphql.OperationContext) *gqlerror.Error {
	if g.Allowed(ctx) {
		opCtx.DisableIntrospection = false
		return nil
	}
	opCtx.DisableIntrospection = true
	op := opCtx.Doc.Operations.ForName(opCtx.OperationName)
	if op != nil && selectsIntrospection(opCtx.Doc, op.SelectionSet, map[string]bool{}) {
		return protocolError(apierror.IntrospectionDisabled, "introspection is disabled")
	}
	return nil
}

// selectsIntrospection reports whether a root selection set selects
// __schema or __type (directly or through fragments).
func selectsIntrospection(doc *ast.QueryDocument, set ast.SelectionSet, seen map[string]bool) bool {
	for _, sel := range set {
		switch s := sel.(type) {
		case *ast.Field:
			if s.Name == "__schema" || s.Name == "__type" {
				return true
			}
		case *ast.InlineFragment:
			if selectsIntrospection(doc, s.SelectionSet, seen) {
				return true
			}
		case *ast.FragmentSpread:
			if seen[s.Name] {
				continue
			}
			seen[s.Name] = true
			if f := doc.Fragments.ForName(s.Name); f != nil && selectsIntrospection(doc, f.SelectionSet, seen) {
				return true
			}
		}
	}
	return false
}
