package compile

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

// A Go-managed C call reaches C through a Go gateway (cCallBridgeN) and the
// runtime-aware execution.CallForeign, which passes integer words only. A
// call with float passing goes through an adapter instead: a generated
// ContextForeign C entry taking the call's target and then its arguments as
// integer words, which makes the call with its float passing and returns
// the result's bits. The adapter's address is the gateway's first argument,
// so such a call has one argument fewer than an integer managed C call.

// floatAdapterPrefix starts the symbol that names an adapter.
const floatAdapterPrefix = "nyanjit.cFloatAdapter."

// floatAdapterSymbol names the adapter for float passing floats and the
// argument widths bits, 32 or 64. Only the widths of marked arguments and,
// on 386, of every argument change the adapter.
func floatAdapterSymbol(floats abi.FloatPassing, bits []uint8) string {
	var name strings.Builder
	name.WriteString(floatAdapterPrefix)
	name.WriteString(strconv.FormatUint(uint64(floats), 16))
	for _, b := range bits {
		name.WriteByte('.')
		name.WriteString(strconv.Itoa(int(b)))
	}
	return name.String()
}

// floatAdapterBits returns the adapter argument widths for a call whose
// arguments have widths (0 or 1–64 bits): 32 for a 32-bit marked argument,
// and on 386 for any argument of at most 32 bits; otherwise 64.
func floatAdapterBits(architecture string, floats abi.FloatPassing, widths []uint8) []uint8 {
	bits := make([]uint8, len(widths))
	for i, w := range widths {
		bits[i] = 64
		if w != 0 && w <= 32 && (architecture == "386" || floats.Argument(i)) {
			bits[i] = 32
		}
	}
	return bits
}

// parseFloatAdapter reverses floatAdapterSymbol.
func parseFloatAdapter(symbol string) (abi.FloatPassing, []uint8, bool) {
	rest, ok := strings.CutPrefix(symbol, floatAdapterPrefix)
	if !ok {
		return 0, nil, false
	}
	fields := strings.Split(rest, ".")
	floats, err := strconv.ParseUint(fields[0], 16, 64)
	if err != nil {
		return 0, nil, false
	}
	bits := make([]uint8, len(fields)-1)
	for i, field := range fields[1:] {
		n, err := strconv.Atoi(field)
		if err != nil || n != 32 && n != 64 {
			return 0, nil, false
		}
		bits[i] = uint8(n)
	}
	return abi.FloatPassing(floats), bits, true
}

var floatAdapters sync.Map // symbol -> *floatAdapter

type floatAdapter struct {
	once    sync.Once
	code    *execution.Code // retained for the process lifetime
	address uintptr
	err     error
}

// floatAdapterAddress returns the host adapter that symbol names, compiling
// it on first use. Adapters live for the process lifetime.
func floatAdapterAddress(symbol string) (uintptr, error) {
	floats, bits, ok := parseFloatAdapter(symbol)
	if !ok {
		return 0, ErrInvalidFunction
	}
	value, _ := floatAdapters.LoadOrStore(symbol, new(floatAdapter))
	adapter := value.(*floatAdapter)
	adapter.once.Do(func() {
		adapter.code, adapter.err = compileFloatAdapter(floats, bits)
		if adapter.err == nil {
			adapter.address, adapter.err = adapter.code.EntryAddress()
		}
	})
	return adapter.address, adapter.err
}

// compileFloatAdapter lowers the adapter for this host: f(target, a0, …)
// returns CallCWithFloats(target, floats, a0, …).
func compileFloatAdapter(floats abi.FloatPassing, bits []uint8) (*execution.Code, error) {
	program := &ir.Program{Arity: len(bits) + 1}
	args := make([]int, 0, len(bits)+1)
	callee := ir.Instruction{Op: ir.OpParam}
	if runtime.GOARCH == "386" {
		callee.Bits = 32 // a native word
	}
	program.Instructions = append(program.Instructions, callee)
	args = append(args, 0)
	for _, b := range bits {
		width := b
		if width == 64 {
			width = 0
		}
		args = append(args, len(program.Instructions))
		program.Instructions = append(program.Instructions, ir.Instruction{Op: ir.OpParam, Bits: width})
	}
	width := floats.ResultBits()
	if width != 32 {
		width = 0
	}
	program.Result = len(program.Instructions)
	program.Instructions = append(program.Instructions, ir.Instruction{Op: ir.OpCallC, Bits: width, Imm: uint64(floats), Args: args})
	sealed, err := ir.SealOwned(program)
	if err != nil {
		return nil, err
	}
	target, err := NewTarget(TargetSpec{Architecture: runtime.GOARCH})
	if err != nil {
		return nil, err
	}
	unit, err := Lower(target, sealed, Func{ABI: abi.C, Context: ContextForeign, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}})
	if err != nil {
		return nil, fmt.Errorf("float call adapter: %w", err)
	}
	template, err := unit.Template()
	if err != nil {
		return nil, err
	}
	return template.Bind(nil)
}
