//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package nyanjit_test

import (
	"errors"
	"runtime"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// prefetchNativeBits is the host pointer width.
const prefetchNativeBits = uint8(unsafe.Sizeof(uintptr(0)) * 8)

// TestPrefetchBothTiers prefetches nil, an invalid address and a live buffer
// through a bound Builder of each tier: none faults or changes a result.
func TestPrefetchBothTiers(t *testing.T) {
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		b := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.C})
		buffer, x := b.ParamNative(), b.ParamNative()
		b.Prefetch(b.ConstBits(prefetchNativeBits, 0), ir.PrefetchRead)
		b.Prefetch(b.ConstBits(prefetchNativeBits, ^uint64(0)>>(64-prefetchNativeBits)), ir.PrefetchWrite)
		b.Prefetch(buffer, ir.PrefetchWrite)
		b.StoreBits(buffer, x)
		b.Prefetch(buffer, ir.PrefetchRead)
		b.Return(b.CastUnsigned(b.LoadBits(buffer, prefetchNativeBits), 64))
		unit, err := b.Lower()
		if err != nil {
			t.Fatalf("tier %d: %v", tier, err)
		}
		template, err := unit.Template()
		if err != nil {
			t.Fatal(err)
		}
		code, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		var word [2]uintptr
		var pin runtime.Pinner
		pin.Pin(&word)
		got, err := code.Call(uintptr(unsafe.Pointer(&word[0])), 0x5a5a)
		pin.Unpin()
		code.Close()
		if err != nil || got != 0x5a5a || word[0] != 0x5a5a {
			t.Errorf("tier %d: got %#x, %v; stored %#x", tier, got, err, word[0])
		}
	}
}

func TestPrefetchBuilderErrors(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Prefetch(b.Param(), ir.PrefetchKind(2))
	b.Return(b.Const(0))
	if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Errorf("invalid kind: %v", err)
	}
	b = nyanjit.NewBuilder()
	b.Prefetch(b.ConstBits(16, 0), ir.PrefetchRead)
	b.Return(b.Const(0))
	if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Errorf("16-bit address: %v", err)
	}
}
