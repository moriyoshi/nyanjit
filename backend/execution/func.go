package execution

import (
	"errors"
	"reflect"
	"runtime"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// ErrSignature means a Go function type does not match a generated entry.
var ErrSignature = errors.New("execution: Go function signature does not match code")

// funcval mirrors the runtime's closure layout: a func value points to a
// record whose first word is the code address. Generated code has no
// captured variables, so it ignores the closure context register.
type funcval struct {
	fn    uintptr
	owner *Code
}

// Func sets the function variable addressed by fptr to call c's entry directly,
// without the Call trampoline. fptr must be a non-nil pointer to a function
// variable; invalid destinations return ErrSignature. On error, its value is
// unchanged. The code must use the Go ABI with no workspace (StackPlatform).
//
// The function's integer parameters must match the compiled widths in order.
// Pointer parameters must match the exact transferable struct or array type
// recorded by PointerParam. By-value structs, arrays and slices are rejected:
// Go's ABI splits them rather than using the address convention of Words.
// The signature may return nothing or one integer of at most 64 bits; bits wider
// than the compiled result are unspecified. On 386 the generated epilogue
// always stores both 32-bit result words, so the signature must return one
// 64-bit integer. Floating point values travel as
// integer bit patterns rather than float32/float64 parameters.
//
// Func retains c and permanently quarantines its executable mapping. Existing
// function values remain callable after Close; new calls through Code fail.
// Pointer signatures use a checked Go wrapper: nil aggregate pointers panic
// with ErrArgumentType before native entry, and typed arguments remain reachable
// throughout the native call, including Go callbacks. Generated frames have no
// pointer maps. Callbacks must not replace reachable slices whose elements
// generated code is still using. Other Go pointer types are rejected.
func (c *Code) Func(fptr any) error {
	target := reflect.ValueOf(fptr)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() || target.Elem().Kind() != reflect.Func || !target.Elem().CanSet() {
		return ErrSignature
	}
	if c == nil {
		return ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entry == 0 || c.state.Load()&codeClosed != 0 {
		return ErrClosed
	}
	switch runtime.GOARCH {
	case "amd64", "arm64", "riscv64", "ppc64le", "386", "loong64":
	default:
		return ErrUnsupported
	}
	if c.options.ABI != abi.Go || c.options.Foreign || c.options.WorkspaceBytes != 0 || c.options.ResultWords > 1 {
		return ErrInvalidCodeOptions
	}
	typ := target.Elem().Type()
	if err := checkFuncType(typ, c.options); err != nil {
		return err
	}
	// A typed, addressable function word has the same runtime layout as the
	// former generic return value. The funcval owns Code through its typed owner.
	holder := reflect.New(typ)
	*(*unsafe.Pointer)(holder.UnsafePointer()) = unsafe.Pointer(&funcval{fn: c.entry, owner: c})
	native := holder.Elem()
	exposed := native
	hasPointer := false
	for i := range typ.NumIn() {
		hasPointer = hasPointer || typ.In(i).Kind() == reflect.Pointer
	}
	if hasPointer {
		exposed = reflect.MakeFunc(typ, func(args []reflect.Value) []reflect.Value {
			for _, arg := range args {
				if arg.Kind() == reflect.Pointer && arg.IsNil() {
					panic(ErrArgumentType)
				}
			}
			result := native.Call(args)
			runtime.KeepAlive(args)
			return result
		})
	}
	// All validation precedes the only write to the caller's variable.
	c.funcExposed = true
	target.Elem().Set(exposed)
	return nil
}

func checkFuncType(t reflect.Type, options CodeOptions) error {
	return checkFuncTypeFor(runtime.GOARCH, t, options)
}

func checkFuncTypeFor(arch string, t reflect.Type, options CodeOptions) error {
	if t.Kind() != reflect.Func || t.IsVariadic() || t.NumIn() != options.Arity ||
		len(options.ParamBits) != options.Arity || t.NumOut() > 1 ||
		len(options.ParameterTypes) != 0 && len(options.ParameterTypes) != options.Arity {
		return ErrSignature
	}
	for i := range t.NumIn() {
		in := t.In(i)
		var expected reflect.Type
		if len(options.ParameterTypes) != 0 {
			expected = options.ParameterTypes[i]
		}
		if expected != nil && in != expected || in.Kind() == reflect.Pointer && expected == nil {
			return ErrSignature
		}
		allowed := integerKind(in.Kind()) || in.Kind() == reflect.Pointer &&
			(in.Elem().Kind() == reflect.Struct || in.Elem().Kind() == reflect.Array) && transferable(in.Elem())
		if !allowed || options.ParamBits[i] == 0 || uint8(in.Size()*8) != options.ParamBits[i] {
			return ErrSignature
		}
	}
	if t.NumOut() == 1 && !integerKind(t.Out(0).Kind()) {
		return ErrSignature
	}
	// A 386 Go-ABI entry writes an 8-byte result slot; a narrower or absent
	// result would let it overwrite the caller's frame.
	if arch == "386" && (t.NumOut() != 1 || t.Out(0).Size() != 8) {
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
