// Package abicheck calls generated code through an independent assembly
// caller and reports registers its ABI requires it to preserve but that it
// changed.
//
// It is instrumentation: tests, examples and other instrument packages may
// import it, production code must not. The caller seeds every preserved
// register with a distinct sentinel, so it checks only leaf entries that
// neither call out nor grow the Go stack, and whose frame fits the caller's
// NOSPLIT budget.
//
// Supported targets are linux/386, linux/amd64, linux/arm64, linux/ppc64le,
// linux/riscv64 and linux/loong64. CheckPreserved returns ErrUnsupported
// elsewhere.
package abicheck

import (
	"errors"

	"github.com/moriyoshi/nyanjit/backend/execution"
)

// ErrUnsupported means the target, ABI, argument count, stack size or entry
// kind cannot be checked.
var ErrUnsupported = errors.New("abicheck: entry cannot be checked on this target")

// Result reports one checked call.
type Result struct {
	// Return is the first result word, as execution.Code.Call returns it.
	Return uintptr
	// Clobbered lists preserved registers whose values changed, in a fixed
	// per-target order. Names are spelled like abi.Report's register names;
	// the stack pointer is "SP" and the goroutine register (on 386 the TLS
	// slot holding g) is "g".
	Clobbered []string
}

// CheckPreserved binds t with t.Bind(nil) and calls its entry once through
// an independent assembly caller. The caller seeds every register the
// entry's ABI requires it to preserve with a distinct sentinel (a register the
// ABI fixes at zero with zero), records the ones it cannot seed, such as SP
// and g, and reports which changed. args are native ABI words, as for
// execution.Code.Call; a count the entry does not take gives an error
// wrapping execution.ErrArity. CheckPreserved closes the code before
// returning.
//
// Entries that call functions, need more stack than a NOSPLIT caller may use,
// take arguments outside the integer argument registers (on 386, the stack
// words execution's trampoline passes), or target another architecture give
// ErrUnsupported.
func CheckPreserved(t *execution.Template, args ...uintptr) (Result, error) {
	return checkPreserved(t, args)
}
