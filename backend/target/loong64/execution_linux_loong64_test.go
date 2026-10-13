//go:build linux && loong64

package loong64_test

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"syscall"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
)

func callPlan(t *testing.T, a *loong64.ByteAssembler, args ...uintptr) uintptr {
	t.Helper()
	c, e := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: len(args)}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	got, e := c.Call(args...)
	if e != nil {
		t.Fatal(e)
	}
	return got
}
func TestNativeConstants(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 7))
	for n := 0; n < 1000; n++ {
		v := rng.Uint64()
		a := loong64.NewByteAssembler(abi.StackNoUse, 0)
		a.Constant(4, v)
		a.Ret()
		if got := uint64(callPlan(t, a)); got != v {
			t.Fatalf("constant%#x got%#x\n%s", v, got, a.Text())
		}
	}
}
func TestNativeBranchForms(t *testing.T) {
	for _, form := range [][2]bool{{false, false}, {true, false}, {true, true}} {
		for _, zero := range []bool{false, true} {
			for _, take := range []bool{false, true} {
				a := loong64.NewByteAssembler(abi.StackNoUse, 0)
				a.Constant(4, 41)
				if zero {
					a.SetPC(4)
					a.BranchZeroRange(5, 64, true, 16+4*boolInt(form[0])+4*boolInt(form[1]), form[0], form[1])
				} else {
					a.CompareImmediate(5, 0)
					a.SetPC(4)
					a.JumpIfRange(op.BranchNotEqual, 16+4*boolInt(form[0])+4*boolInt(form[1]), form[0], form[1])
				}
				a.Constant(4, 42)
				a.Ret()
				a.Constant(4, 43)
				a.Ret()
				v := uintptr(0)
				if take {
					v = 1
				}
				got := callPlan(t, a, 0, v)
				want := uintptr(42)
				if take {
					want = 43
				}
				if got != want {
					t.Fatalf("form%v zero%v take%v got%d want%d:\n%s", form, zero, take, got, want, a.Text())
				}
			}
		}
	}
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func TestNativeAliases(t *testing.T) {
	for _, operation := range []op.Opcode{op.OpShiftLeft, op.OpShiftRight, op.OpShiftRightArithmetic, op.OpRotateLeft, op.OpRotateRight} {
		for _, dst := range []backend.Register{4, 5, 6} {
			a := loong64.NewByteAssembler(abi.StackNoUse, 0)
			a.ShiftBy(operation, 64, dst, 4, 5)
			a.Move(4, dst)
			a.Ret()
			x, c := uint64(0x8123456789abcdef), uint64(64)
			want := uint64(0)
			if operation == op.OpShiftRightArithmetic {
				want = ^uint64(0)
			}
			if operation == op.OpRotateLeft || operation == op.OpRotateRight {
				want = x
			}
			if got := uint64(callPlan(t, a, uintptr(x), uintptr(c))); got != want {
				t.Fatalf("op%d dst%d got%#x want%#x", operation, dst, got, want)
			}
		}
	}
}

func TestNativeMemoryAddressPlans(t *testing.T) {
	data, err := syscall.Mmap(-1, 0, 32768, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(data)
	base := uintptr(unsafe.Pointer(&data[8192]))
	const value = uint64(0x8123456789abcdef)
	for _, scale := range []uint64{1, 2, 3, 4, 8, 16, 128} {
		for _, disp := range []int64{0, 8, 4096, -4096} {
			for _, order := range []op.ByteOrder{op.ByteOrderNative, op.ByteOrderBigEndian} {
				a := loong64.NewByteAssembler(abi.StackNoUse, 0)
				store := machine.Instruction{Op: machine.OpStoreAddress, A: 4, B: 5, C: 6, Imm: scale, Disp: uint64(disp), ByteOrder: order}
				load := machine.Instruction{Op: machine.OpLoadAddress, A: 4, B: 4, C: 5, Imm: scale, Disp: uint64(disp), ByteOrder: order}
				for _, i := range []machine.Instruction{store, load} {
					a.SetPC(a.AssembledSize())
					if err := machine.Visit(i, a); err != nil {
						t.Fatal(err)
					}
				}
				a.Ret()
				got := uint64(callPlan(t, a, base, 3, uintptr(value)))
				if got != value {
					t.Fatalf("scale%d disp%d order%d got%#x", scale, disp, order, got)
				}
				off := 8192 + 3*int(scale) + int(disp)
				var want [8]byte
				if order == op.ByteOrderBigEndian {
					binary.BigEndian.PutUint64(want[:], value)
				} else {
					binary.LittleEndian.PutUint64(want[:], value)
				}
				if !bytes.Equal(data[off:off+8], want[:]) {
					t.Fatalf("scale%d disp%d order%d stored%x want%x", scale, disp, order, data[off:off+8], want)
				}
			}
		}
	}
}
