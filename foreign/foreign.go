package foreign

import (
	"reflect"
	"runtime"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/regalloc"
)

// ForeignFunc is a C ABI scalar function target. A callback created with
// RegisterCallback is retained by purego for the process lifetime.
type ForeignFunc struct {
	address  uintptr
	argCount int
	effect   *abi.CallEffect
	callback any
}

// New describes an externally managed C ABI function pointer.
// The caller must ensure the target accepts argCount uintptr-sized words and
// returns one uintptr-sized word. A nil effect uses the conservative platform
// C ABI call contract.
func New(address uintptr, argCount int, effect *abi.CallEffect) (ForeignFunc, error) {
	if address == 0 || argCount < 0 || argCount > abi.MaxArguments() {
		return ForeignFunc{}, regalloc.ErrInvalidFunction
	}
	var copyEffect *abi.CallEffect
	if effect != nil {
		value := effect.Clone()
		copyEffect = &value
	}
	return ForeignFunc{address: address, argCount: argCount, effect: copyEffect}, nil
}

// RegisterCallback registers a Go function as a C ABI callback. Arguments
// and the single result must be uintptr; the function must not be variadic.
// Registration is process-long because purego has no unregister API.
func RegisterCallback(fn any) (ForeignFunc, error) {
	if fn == nil {
		return ForeignFunc{}, regalloc.ErrInvalidFunction
	}
	typeOf := reflect.TypeOf(fn)
	if typeOf.Kind() != reflect.Func || reflect.ValueOf(fn).IsNil() || typeOf.IsVariadic() ||
		typeOf.NumIn() > abi.MaxArguments() || typeOf.NumOut() != 1 || typeOf.Out(0).Kind() != reflect.Uintptr {
		return ForeignFunc{}, regalloc.ErrInvalidFunction
	}
	for i := 0; i < typeOf.NumIn(); i++ {
		if typeOf.In(i).Kind() != reflect.Uintptr {
			return ForeignFunc{}, regalloc.ErrInvalidFunction
		}
	}
	address, err := execution.RegisterForeignCallback(fn)
	if err != nil {
		return ForeignFunc{}, regalloc.ErrInvalidFunction
	}
	return ForeignFunc{address: address, argCount: typeOf.NumIn(), callback: fn}, nil
}

// Call emits a call to a signature-checked C ABI target.
func Call(b *nyanjit.Builder, target ForeignFunc, args ...nyanjit.Value) (nyanjit.Value, error) {
	if target.address == 0 {
		return nyanjit.Value{}, regalloc.ErrInvalidFunction
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
		return nyanjit.Value{}, regalloc.ErrInvalidFunction
	}
	address := b.ConstNative(target.address)
	if target.effect != nil {
		return b.CallCWithEffect(address, *target.effect, args...), nil
	}
	return b.CallC(address, args...), nil
}
