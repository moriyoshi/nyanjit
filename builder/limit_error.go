package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// limitError is a numeric backend limit the Builder checks while building. It
// wraps ErrBackendLimit; the root nyanjit.Builder reports it as a
// *compile.LimitError through BackendLimit.
type limitError struct {
	kind          string // a compile.LimitKind name, such as "arguments"
	needed, limit int
	detail        string
	message       string
}

func newLimitError(kind string, needed, limit int, detail, format string, args ...any) error {
	return &limitError{kind: kind, needed: needed, limit: limit, detail: detail, message: fmt.Sprintf(format, args...)}
}

func (e *limitError) Error() string { return e.message + ": " + ErrBackendLimit.Error() }

func (e *limitError) Unwrap() error { return ErrBackendLimit }

// BackendLimit returns the limit's compile.LimitKind name, the amount needed,
// the limit and where it was reached.
func (e *limitError) BackendLimit() (kind string, needed, limit int, detail string) {
	return e.kind, e.needed, e.limit, e.detail
}

// programPredicateLimit reports a program whose branch predicates together
// exceed ir.MaxPredicateNodes nodes. needed may be a lower bound.
func programPredicateLimit(operation string, needed int) error {
	return newLimitError("predicate nodes", needed, ir.MaxPredicateNodes, "program", "%s: program predicate budget %d exceeded", operation, ir.MaxPredicateNodes)
}
