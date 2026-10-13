package execution

import (
	"errors"
	"reflect"
	"runtime"
	"unsafe"

	"github.com/moriyoshi/nyanjit/abi"
)

// ErrSignature means a Go function type does not match a generated entry.
var ErrSignature = errors.New("execution: Go function signature does not match code")

// funcval mirrors the runtime's closure layout: a func value points to a
// record whose first word is the code address. Generated code has no
// captured variables, so it ignores the closure context register.
type funcval struct{ fn uintptr }

// Func returns a Go function value of type F that calls c's entry directly,
// without the Call trampoline. The code must use the Go ABI with no
// workspace (compile with StackPlatform), and F must be a function whose
// parameters are integer types matching the compiled parameter widths in
// order. F may return nothing or one integer of at most 64 bits; bits wider
// than the compiled result are unspecified. Floating point values travel as
// bit patterns, so use math.Float64bits rather than float64 parameters.
//
// The function value does not keep c mapped. Code registered with the Go
// runtime is never unmapped; other code must stay open while F may be
// called. Do not pass Go pointers as arguments: generated frames are
// pointer-free, so pin the memory and pass its address as a uintptr.
func Func[F any](c *Code) (F, error) {
	var zero F
	if c == nil {
		return zero, ErrClosed
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.entry == 0 {
		return zero, ErrClosed
	}
	switch runtime.GOARCH {
	case "amd64", "arm64", "riscv64", "ppc64", "ppc64le", "386":
	default:
		return zero, ErrUnsupported
	}
	if c.options.ABI != abi.Go || c.options.Foreign || c.options.WorkspaceBytes != 0 || c.options.ResultWords > 1 {
		return zero, ErrInvalidCodeOptions
	}
	if err := checkFuncType(reflect.TypeFor[F](), c.options); err != nil {
		return zero, err
	}
	var f F
	*(*unsafe.Pointer)(unsafe.Pointer(&f)) = unsafe.Pointer(&funcval{fn: c.entry})
	return f, nil
}

func checkFuncType(t reflect.Type, options CodeOptions) error {
	if t.Kind() != reflect.Func || t.IsVariadic() || t.NumIn() != options.Arity ||
		len(options.ParamBits) != options.Arity || t.NumOut() > 1 {
		return ErrSignature
	}
	for i := range t.NumIn() {
		in := t.In(i)
		if !integerKind(in.Kind()) || options.ParamBits[i] == 0 || uint8(in.Size()*8) != options.ParamBits[i] {
			return ErrSignature
		}
	}
	if t.NumOut() == 1 && !integerKind(t.Out(0).Kind()) {
		return ErrSignature
	}
	return nil
}

func integerKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return false
}
