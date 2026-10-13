//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package nyanjit_test

import (
	"bytes"
	"errors"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// buildLaneMoves stores, from the 16-byte vectors at buffer+0 and +16, a
// two-input byte shuffle, a halfword broadcast and both widenings of the
// first vector at buffer+32, +48, +64 and +96, and returns lane 3 of the
// signed widening.
func buildLaneMoves(b *nyanjit.Builder) {
	buffer := b.ParamNative()
	at := func(offset uintptr) nyanjit.Value { return b.Add(buffer, b.ConstNative(offset)) }
	x, y := b.VLoad(ir.Vec(16, 8), buffer), b.VLoad(ir.Vec(16, 8), at(16))
	b.VStore(at(32), b.ShuffleLanes(x, y, []uint16{31, 0, 17, 2, 3, 3, 16, 9, 8, 30, 1, 5, 7, 6, 24, 12}))
	b.VStore(at(48), b.BroadcastLane(b.VLoad(ir.Vec(8, 16), buffer), 5))
	b.VStore(at(64), b.WidenLanes(x))
	signed := b.WidenLanesSigned(x)
	b.VStore(at(96), signed)
	b.Return(b.CastUnsigned(b.ExtractLane(signed, 3), uint8(8*unsafe.Sizeof(uintptr(0)))))
}

func expectedLaneMoves(memory []byte) []byte {
	want := bytes.Clone(memory)
	x, y := memory[:16], memory[16:32]
	both := append(append([]byte(nil), x...), y...)
	for i, index := range []int{31, 0, 17, 2, 3, 3, 16, 9, 8, 30, 1, 5, 7, 6, 24, 12} {
		want[32+i] = both[index]
	}
	for i := 0; i < 8; i++ {
		want[48+2*i], want[48+2*i+1] = x[10], x[11]
	}
	for i := 0; i < 16; i++ {
		want[64+2*i], want[64+2*i+1] = x[i], 0
		want[96+2*i], want[96+2*i+1] = x[i], byte(int8(x[i])>>7)
	}
	return want
}

func TestLaneMovesAcrossTiers(t *testing.T) {
	specs := map[string]compile.TargetSpec{
		"optimized":   {},
		"vectors-off": {Vectors: compile.VectorsOff()},
		"baseline":    {Tier: compile.TierBaseline},
	}
	f := compile.Func{ABI: abi.C}
	for name, spec := range specs {
		target, err := compile.NewTarget(spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, bound := range []bool{false, true} {
			var unit *compile.Unit
			if bound {
				b := nyanjit.NewBuilderFor(target, f)
				buildLaneMoves(b)
				unit, err = b.Lower()
			} else {
				b := nyanjit.NewBuilder()
				buildLaneMoves(b)
				program, ferr := b.Finish()
				if ferr != nil {
					t.Fatal(ferr)
				}
				unit, err = compile.Lower(target, program, f)
			}
			if err != nil {
				t.Fatalf("%s bound=%v: %v", name, bound, err)
			}
			template, err := unit.Template()
			if err != nil {
				t.Fatal(err)
			}
			code, err := template.Bind(nil)
			if err != nil {
				t.Fatal(err)
			}
			memory := make([]byte, 128)
			for i := range memory {
				memory[i] = byte(i*29 + 0x83)
			}
			want := expectedLaneMoves(memory)
			got, err := code.Call(uintptr(unsafe.Pointer(&memory[0])))
			code.Close()
			lane3 := uint64(memory[3]) | uint64(byte(int8(memory[3])>>7))<<8
			if err != nil || uint64(got)&0xffff != lane3 || !bytes.Equal(memory, want) {
				t.Fatalf("%s bound=%v: got %#x, %v\nmemory %x\nwant   %x", name, bound, got, err, memory, want)
			}
		}
	}
}

func TestLaneMoveErrorsAreSticky(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.VLoad(ir.Vec(4, 32), b.ParamNative())
	if v := b.ShuffleLanes(x, x, []uint16{0, 1, 2}); v.Type().Valid() {
		t.Fatal("short index list accepted")
	}
	b.WidenLanes(x)
	b.Return(b.Const(0))
	if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("Finish=%v", err)
	}
	for _, f := range []func(*nyanjit.Builder, nyanjit.Value) nyanjit.Value{
		func(b *nyanjit.Builder, v nyanjit.Value) nyanjit.Value { return b.BroadcastLane(v, 4) },
		func(b *nyanjit.Builder, v nyanjit.Value) nyanjit.Value {
			return b.WidenLanes(b.VLoad(ir.Vec(4, 64), b.ConstNative(0)))
		},
		func(b *nyanjit.Builder, v nyanjit.Value) nyanjit.Value { return b.WidenLanesSigned(b.Const(1)) },
	} {
		b := nyanjit.NewBuilder()
		if v := f(b, b.VLoad(ir.Vec(4, 32), b.ParamNative())); v.Type().Valid() {
			t.Fatal("invalid lane move accepted")
		}
		b.Return(b.Const(0))
		if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("Finish=%v", err)
		}
	}
}
