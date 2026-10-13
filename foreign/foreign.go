package foreign

import (
	"reflect"
	"runtime"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
)

// ForeignFunc is a C ABI scalar function target. A callback created with
// RegisterCallback is retained by purego for the process lifetime.
type ForeignFunc struct {
	address  uintptr
	argCount int
	floats   abi.FloatPassing
	effect   *abi.CallEffect
	callback any
}

// New describes an externally managed C ABI function pointer.
// The caller must ensure the target accepts argCount uintptr-sized words and
// returns one uintptr-sized word, except where floats marks an argument or
// the result: those are binary32 or binary64 values that travel in floating
// point registers, as in Builder.CallCWithFloats. A nil effect uses the
// conservative platform C ABI call contract; a supplied effect must have the
// same Floats. Invalid marks, or more than eight float arguments, return
// backend.ErrInvalidFunction.
func New(address uintptr, argCount int, floats abi.FloatPassing, effect *abi.CallEffect) (ForeignFunc, error) {
	if address == 0 || argCount < 0 || argCount > abi.MaxArguments() || !validFloats(floats, argCount) ||
		effect != nil && effect.Floats != floats {
		return ForeignFunc{}, backend.ErrInvalidFunction
	}
	var copyEffect *abi.CallEffect
	if effect != nil {
		value := effect.Clone()
		copyEffect = &value
	}
	return ForeignFunc{address: address, argCount: argCount, floats: floats, effect: copyEffect}, nil
}

// validFloats checks floats against argCount arguments: marks only for
// passed arguments, one result width and at most eight float arguments.
func validFloats(floats abi.FloatPassing, argCount int) bool {
	if floats&abi.FloatResult32 != 0 && floats&abi.FloatResult64 != 0 {
		return false
	}
	count := 0
	for i := range 62 {
		if floats.Argument(i) {
			if i >= argCount {
				return false
			}
			count++
		}
	}
	return count <= 8
}

// RegisterCallback registers a Go function as a C ABI callback. Arguments
// and the single result must be uintptr; the function must not be variadic.
// Registration is process-long because purego has no unregister API.
func RegisterCallback(fn any) (ForeignFunc, error) {
	if fn == nil {
		return ForeignFunc{}, backend.ErrInvalidFunction
	}
	typeOf := reflect.TypeOf(fn)
	if typeOf.Kind() != reflect.Func || reflect.ValueOf(fn).IsNil() || typeOf.IsVariadic() ||
		typeOf.NumIn() > abi.MaxArguments() || typeOf.NumOut() != 1 || typeOf.Out(0).Kind() != reflect.Uintptr {
		return ForeignFunc{}, backend.ErrInvalidFunction
	}
	for i := 0; i < typeOf.NumIn(); i++ {
		if typeOf.In(i).Kind() != reflect.Uintptr {
			return ForeignFunc{}, backend.ErrInvalidFunction
		}
	}
	address, err := execution.RegisterForeignCallback(fn)
	if err != nil {
		return ForeignFunc{}, backend.ErrInvalidFunction
	}
	return ForeignFunc{address: address, argCount: typeOf.NumIn(), callback: fn}, nil
}

// Call emits a call to a signature-checked C ABI target. Arguments that the
// target's float passing marks must be 32- or 64-bit Values holding the IEEE
// bit pattern; a float result is a Value of its width.
func Call(b *nyanjit.Builder, target ForeignFunc, args ...nyanjit.Value) (nyanjit.Value, error) {
	if target.address == 0 {
		return nyanjit.Value{}, backend.ErrInvalidFunction
	}
	if len(args) != target.argCount {
		return nyanjit.Value{}, execution.ErrArity
	}
	if runtime.GOARCH == "386" {
		// The 386 builder's C boundary uses uint64 stack slots and EDX:EAX
		// results, while ForeignFunc describes uintptr signatures.
		return nyanjit.Value{}, execution.ErrUnsupported
	}
	if b == nil {
		return nyanjit.Value{}, backend.ErrInvalidFunction
	}
	address := b.ConstNative(target.address)
	if target.effect != nil {
		return b.CallCWithEffect(address, *target.effect, args...), nil
	}
	return b.CallCWithFloats(address, target.floats, args...), nil
}
