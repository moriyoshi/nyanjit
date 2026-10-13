package builder

import "fmt"

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
