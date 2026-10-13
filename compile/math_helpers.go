package compile

import (
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// resolveMathBindings adds the process-local tables read by inline math
// expansions to bindings, unless the caller bound those symbols.
func resolveMathBindings(relocations []machine.Relocation, bindings map[string]uint64) (map[string]uint64, error) {
	var result map[string]uint64
	for _, relocation := range relocations {
		if relocation.NeedsCodeBase() {
			continue
		}
		if _, ok := bindings[relocation.Symbol]; ok {
			continue
		}
		address, ok := mathTableAddress(relocation.Symbol)
		if !ok {
			continue
		}
		if result == nil {
			result = make(map[string]uint64, len(bindings)+1)
			for name, value := range bindings {
				result[name] = value
			}
		}
		result[relocation.Symbol] = address
	}
	if result != nil {
		return result, nil
	}
	return bindings, nil
}

// pi4 holds the binary digits of 4/π: 4/π = Σ pi4[i]·2^(-64i).
// The digits are from Go's math/trig_reduce.go.
var pi4 = [...]uint64{
	0x0000000000000001,
	0x45f306dc9c882a53,
	0xf84eafa3ea69bb81,
	0xb6c52b3278872083,
	0xfca2c757bd778ac3,
	0x6e48dc74849ba5c0,
	0x0c925dd413a32439,
	0xfc3bd63962534e7d,
	0xd1046bea5d768909,
	0xd338e04d68befc82,
	0x7323ac7306a673e9,
	0x3908bf177bf25076,
	0x3ff12fffbc0b301f,
	0xde5e2316b414da3e,
	0xda6cfd9e4f96136e,
	0x9e8c7ecd3cbfd45a,
	0xea4f758fd7cbe2f6,
	0x7a0e73ef14a525d4,
	0xd7f6bf623f1aba10,
	0xac06608df8f6d757,
}

// mathTableAddress resolves process-local tables used by inline expansions.
// The static table remains valid for the lifetime of the process.
func mathTableAddress(symbol string) (uint64, bool) {
	if symbol != ir.MathPi4Symbol {
		return 0, false
	}
	return uint64(uintptr(unsafe.Pointer(&pi4[0]))), true
}
