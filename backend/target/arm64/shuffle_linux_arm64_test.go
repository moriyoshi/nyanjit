//go:build linux && arm64

package arm64_test

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
)

// runVector loads V(b) from buf[0:16] and V(c) from buf[16:32] (only V(b)
// when they are one register), runs ins and stores V(a) to buf[32:48].
func runVector(t *testing.T, ins machine.Instruction, buf *[48]byte) {
	t.Helper()
	a, b, c := uint32(backend.VectorNumber(ins.A)), uint32(backend.VectorNumber(ins.B)), uint32(backend.VectorNumber(ins.C))
	words := []uint32{0x3dc00000 | b}
	if ins.Arithmetic == op.OpVShuffle && c != b {
		words = append(words, 0x3dc00400|c)
	}
	code := binary.LittleEndian.AppendUint32(nil, words[0])
	for _, w := range words[1:] {
		code = binary.LittleEndian.AppendUint32(code, w)
	}
	asm := arm64.NewByteAssembler(abi.StackNoUse, 0)
	if err := asm.Vector(ins); err != nil {
		t.Fatalf("%+v: %v", ins, err)
	}
	code = append(code, asm.Bytes()...)
	code = binary.LittleEndian.AppendUint32(code, 0x3d800800|a) // str qA, [x0, #32]
	code = binary.LittleEndian.AppendUint32(code, 0xd65f03c0)   // ret
	program, err := execution.NewCode(code, execution.CodeOptions{ABI: abi.C, Arity: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer program.Close()
	if _, err := program.Call(uintptr(unsafe.Pointer(buf))); err != nil {
		t.Fatal(err)
	}
}

// TestNEONShuffleExecution runs shuffles and extends on the host against
// their lane semantics, for every width and register alias arrangement.
func TestNEONShuffleExecution(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 9))
	// A, B, C register numbers: distinct, A=B, A=C, B=C and A=B=C.
	aliases := [][3]uint8{{3, 4, 5}, {4, 4, 5}, {5, 4, 5}, {3, 4, 4}, {4, 4, 4}}
	for _, bits := range []uint8{8, 16, 32, 64} {
		n := 128 / int(bits)
		width := int(bits / 8)
		sets := [][]int{
			lanes(n, func(i int) int { return i }),
			lanes(n, func(i int) int { return n + i }),
			lanes(n, func(i int) int { return n - 1 - i }),
			lanes(n, func(i int) int { return i/2 + i%2*n }),
			lanes(n, func(i int) int { return n/2 + i/2 + i%2*n }),
			lanes(n, func(i int) int { return 2 * i }),
			lanes(n, func(i int) int { return i | 1 + i%2*n }),
			lanes(n, func(int) int { return n - 1 }),
			lanes(n, func(int) int { return n + 1 }),
		}
		for k := 1; k < 2*n; k++ {
			sets = append(sets, lanes(n, func(i int) int { return (i + k) % (2 * n) }), lanes(n, func(i int) int { return (i + k) % n }))
		}
		for range 40 {
			sets = append(sets, lanes(n, func(int) int { return r.IntN(2 * n) }), lanes(n, func(int) int { return r.IntN(n) }), lanes(n, func(int) int { return n + r.IntN(n) }))
		}
		for _, indices := range sets {
			for _, regs := range aliases {
				var buf [48]byte
				for i := range buf[:32] {
					buf[i] = byte(r.Uint32())
				}
				input := buf
				if regs[1] == regs[2] {
					copy(input[16:32], input[:16])
				}
				runVector(t, shuffleForm(bits, regs[0], regs[1], regs[2], indices), &buf)
				for i, x := range indices {
					want := input[x*width : (x+1)*width]
					if got := buf[32+i*width : 32+(i+1)*width]; string(got) != string(want) {
						t.Fatalf("bits %d indices %v regs %v: lane %d = %x, want %x", bits, indices, regs, i, got, want)
					}
				}
			}
		}
	}
	v := backend.VectorRegister
	for _, bits := range []uint8{16, 32, 64} {
		for _, arithmetic := range []op.Opcode{op.OpVExtendUnsigned, op.OpVExtendSigned} {
			for half := range uint64(2) {
				for _, a := range []uint8{3, 4} {
					var buf [48]byte
					for i := range buf[:16] {
						buf[i] = byte(r.Uint32())
					}
					input := buf
					runVector(t, machine.Instruction{Op: machine.OpVector, Arithmetic: arithmetic, Bits: bits, A: v(a), B: v(4), Imm: half}, &buf)
					narrow := int(bits / 16)
					for i := range 128 / int(bits) {
						var x uint64
						for j := narrow - 1; j >= 0; j-- {
							x = x<<8 | uint64(input[int(half)*8+i*narrow+j])
						}
						if arithmetic == op.OpVExtendSigned && x>>(8*narrow-1) != 0 {
							x |= ^uint64(0) << (8 * narrow)
						}
						var got uint64
						for j := 2*narrow - 1; j >= 0; j-- {
							got = got<<8 | uint64(buf[32+i*2*narrow+j])
						}
						if want := x & (^uint64(0) >> (64 - bits)); got != want {
							t.Fatalf("%v bits %d half %d A=V%d: lane %d = %#x, want %#x", arithmetic, bits, half, a, i, got, want)
						}
					}
				}
			}
		}
	}
}
