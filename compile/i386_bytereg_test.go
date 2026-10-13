package compile_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// i386ByteChain is x ^ 0x5a, << 1, + 3 and >> 2 at 8 bits, zero-extended.
func i386ByteChain(b *n.Builder, x n.Value) n.Value {
	v := b.Xor(x, b.ConstBits(8, 0x5a))
	v = b.ShiftLeft(v, 1)
	v = b.Add(v, b.ConstBits(8, 3))
	v = b.ShiftRight(v, 2)
	return b.CastUnsigned(v, 32)
}

func i386ByteChainEval(x uint32) uint32 {
	v := uint8(x) ^ 0x5a
	v <<= 1
	v += 3
	v >>= 2
	return uint32(v)
}

// i386ByteKernels compute i386ByteChain on their last parameter x with 32-bit
// values live. In "freed", x ^ 0x5a is computed while p, q, r and s and
// their products' sum hold registers, the constant 0x5a among them, which
// then dies and leaves a byte register for the rest of the chain. In
// "live", four values computed first and used after the chain hold every
// byte register throughout. Neither has more than six parameters, which
// amd64 hosts allow.
var i386ByteKernels = []struct {
	name  string
	arity int
	build func(*n.Builder)
	eval  func(in []uint32) uint32
}{
	{"freed", 5, func(b *n.Builder) {
		p, q, r, s := b.ParamBits(32), b.ParamBits(32), b.ParamBits(32), b.ParamBits(32)
		x := b.ParamBits(8)
		u := b.Add(b.Mul(p, q), b.Mul(r, s))
		v := i386ByteChain(b, x)
		b.Return(b.Add(b.Add(u, v), b.Add(p, b.Add(q, b.Add(r, s)))))
	}, func(in []uint32) uint32 {
		p, q, r, s := in[0], in[1], in[2], in[3]
		return p*q + r*s + i386ByteChainEval(in[4]) + p + q + r + s
	}},
	{"live", 6, func(b *n.Builder) {
		p, q, r, s, t := b.ParamBits(32), b.ParamBits(32), b.ParamBits(32), b.ParamBits(32), b.ParamBits(32)
		x := b.ParamBits(8)
		a, c := b.Mul(p, q), b.Mul(r, s)
		d, e := b.Mul(a, t), b.Xor(a, c)
		b.Return(b.Add(a, b.Add(c, b.Add(d, b.Add(e, i386ByteChain(b, x))))))
	}, func(in []uint32) uint32 {
		a, c := in[0]*in[1], in[2]*in[3]
		return a + c + a*in[4] + (a ^ c) + i386ByteChainEval(in[5])
	}},
}

// i386SpillPattern matches a store to the platform frame after the
// prologue: a spill.
var i386SpillPattern = regexp.MustCompile(`\nmov dword ptr \[ESP \+ \d+\], E\w+\n`)

// TestI386BytePreference: on the optimized tier the 8-bit chain of "freed"
// moves to the byte register the constant leaves, so its masks are MOVZX;
// in "live" every byte register holds a live value, and the chain stays in
// ESI or EDI with the AND masks, spilling nothing to make room. Both compute
// Go's result on 386.
func TestI386BytePreference(t *testing.T) {
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}}
	inByte := regexp.MustCompile(`\nshl E[ABCD]X, 1\nmovzx E[ABCD]X, [ABCD]L\n(.*\n)*add E[ABCD]X, E\w+\nmovzx E[ABCD]X, [ABCD]L\n(.*\n)*shr E[ABCD]X, 2\nmovzx E[ABCD]X, [ABCD]L\n`)
	inOther := regexp.MustCompile(`\nshl E(SI|DI), 1\nand E(SI|DI), 0xff\n(.*\n)*add E(SI|DI), E\w+\nand E(SI|DI), 0xff\n(.*\n)*shr E(SI|DI), 2\nand E(SI|DI), 0xff\n`)
	// Online prefers EAX only for a tie after a crowded start.
	online := os.Getenv("NYANJIT_INTERNAL_ONLINE_ALLOCATION") == "1"
	for _, k := range i386ByteKernels {
		u := lower386(t, compile.TierOptimized, k.build, f)
		listing, err := u.Listing()
		if err != nil {
			t.Fatal(err)
		}
		live := k.name == "live"
		if !live && !online && !inByte.MatchString(listing) {
			t.Errorf("%s: chain not in a byte register:\n%s", k.name, listing)
		}
		if live && (!inOther.MatchString(listing) || i386SpillPattern.MatchString(listing)) {
			t.Errorf("%s: chain not in ESI or EDI without a spill:\n%s", k.name, listing)
		}
		if runtime.GOARCH != "386" {
			continue
		}
		template, err := u.Template()
		if err != nil {
			t.Fatal(err)
		}
		code, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range []uint32{0, 1, 0x5a, 0x7f, 0x80, 0xa5, 0xff} {
			in := []uint32{0x01020304, 0x10203040, 0xfffffff0, x * 0x01010101, 0x9e3779b9}[:k.arity-1]
			in = append(in, x)
			args := make([]uintptr, len(in))
			for i, v := range in {
				args[i] = uintptr(v)
			}
			lo, _, err := code.CallPair(args...)
			if err != nil {
				t.Fatal(err)
			}
			if want := k.eval(in); uint32(lo) != want {
				t.Fatalf("%s: f(%#x) = %#x, want %#x\n%s", k.name, in, uint32(lo), want, listing)
			}
		}
		code.Close()
	}
}

// i386BigEndianStore16 stores a 16-bit value big-endian at a 64-bit
// address plus one, two byte stores of which the second's value lives on in
// a register without a byte form, and loads the 64 bits at the address.
func i386BigEndianStore16() (*ir.SealedProgram, error) {
	return ir.Seal(&ir.Program{Arity: 2, Result: 5, Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 64},
		{Op: ir.OpParam, Bits: 16},
		{Op: ir.OpConst, Bits: 64, Imm: 1},
		{Op: ir.OpAdd, Bits: 64, A: 0, B: 2},
		{Op: ir.OpStore16, Bits: 16, A: 3, B: 1, ByteOrder: ir.ByteOrderBigEndian},
		{Op: ir.OpLoad64, Bits: 64, A: 0},
	}})
}

// TestI386ByteStore: on the direct walk, an 8-bit store of a value outside
// the byte registers copies it into a free one instead of borrowing EAX or
// ECX around a spill. On 386 the stored bytes are big-endian.
func TestI386ByteStore(t *testing.T) {
	p, err := i386BigEndianStore16()
	if err != nil {
		t.Fatal(err)
	}
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "386"})
	if err != nil {
		t.Fatal(err)
	}
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}}
	u, err := compile.Lower(target, p, f)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := u.Listing()
	if err != nil {
		t.Fatal(err)
	}
	stores := regexp.MustCompile(`\nmov byte ptr \[E\w+\], [ABCD]L\n`).FindAllString(listing, -1)
	if len(stores) != 2 {
		t.Errorf("%d byte stores, want 2:\n%s", len(stores), listing)
	}
	// The online allocator keeps the borrowed register.
	borrow := regexp.MustCompile(`\nmov dword ptr \[ESP \+ \d+\], (E[ABCD]X)\nmov (E[ABCD]X), E(SI|DI|BP)\nmov byte ptr `)
	if os.Getenv("NYANJIT_INTERNAL_ONLINE_ALLOCATION") != "1" && borrow.MatchString(listing) {
		t.Errorf("spill around a byte store:\n%s", listing)
	}
	if runtime.GOARCH != "386" {
		return
	}
	template, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	code, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, v := range []uint16{0, 0x1234, 0xff00, 0x00ff, 0xa55a} {
		buf := []byte{1, 2, 3, 4, 5, 6, 7, 8}
		lo, hi, err := code.CallPair(uintptr(unsafe.Pointer(&buf[0])), 0, uintptr(v))
		if err != nil {
			t.Fatal(err)
		}
		want := []byte{1, byte(v >> 8), byte(v), 4, 5, 6, 7, 8}
		got := uint64(uint32(lo)) | uint64(uint32(hi))<<32
		if string(buf) != string(want) || got != binary.LittleEndian.Uint64(want) {
			t.Fatalf("store %#x: buffer % x, load %#x\n%s", v, buf, got, listing)
		}
		runtime.KeepAlive(buf)
	}
}

// TestI386BaselineByteTemp: the baseline allocates no byte register but
// EBX, so an 8-bit result bound for ESI, EDI or EBP is computed in EAX and
// moved there by MOVZX: no 6-byte AND masks an 8-bit result outside EAX.
func TestI386BaselineByteTemp(t *testing.T) {
	and := regexp.MustCompile(`\nand E(BX|CX|DX|SI|DI|BP), 0xff\n`)
	temp := regexp.MustCompile(`\nmovzx E(SI|DI|BP), AL\n`)
	values := []uint64{0, 1, 0x5a, 0x7f, 0x80, 0xff}
	for _, o := range i386MaskOps {
		for _, flags := range []bool{false, true} {
			name := fmt.Sprintf("%s/read=%v", o.name, flags)
			// Two live parameters push the result out of EBX.
			u := lower386(t, compile.TierBaseline, i386MaskKernel(8, o.build, flags), compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}})
			listing, err := u.Listing()
			if err != nil {
				t.Fatal(err)
			}
			if and.MatchString(listing) {
				t.Errorf("%s: AND mask:\n%s", name, listing)
			}
			if !temp.MatchString(listing) && !strings.Contains(listing, "movzx EBX, BL") {
				t.Errorf("%s: no MOVZX into the result:\n%s", name, listing)
			}
			if runtime.GOARCH != "386" {
				continue
			}
			template, err := u.Template()
			if err != nil {
				t.Fatal(err)
			}
			code, err := template.Bind(nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range values {
				for _, y := range values {
					lo, _, err := code.CallPair(uintptr(x), uintptr(y))
					if err != nil {
						t.Fatal(err)
					}
					r := o.eval(x, y) & 0xff
					want := r ^ x
					if flags && r == 0 {
						want = 0x99
					} else if flags {
						want = r ^ 0x5a
					}
					if uint64(lo) != want {
						t.Fatalf("%s: f(%#x, %#x) = %#x, want %#x\n%s", name, x, y, lo, want, listing)
					}
				}
			}
			code.Close()
		}
	}
}
