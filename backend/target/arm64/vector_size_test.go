package arm64

import (
	"math/rand"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestVectorSizeMatchesEncodingPlan(t *testing.T) {
	check := func(i machine.Instruction, stack abi.StackPractice, homes int) {
		t.Helper()
		p, err := vectorPlan(i, stack, homes, false)
		want := p.n * 4
		if err != nil {
			want = 0
		}
		if got := VectorSize(i, stack, homes); got != want {
			t.Fatalf("%+v stack=%v homes=%d: size=%d encoding=%d err=%v", i, stack, homes, got, want, err)
		}
	}
	// Enumerate all arithmetic opcodes, lane widths and special immediate
	// boundaries, including currently unsupported plans and aliased operands.
	for operation := 0; operation < 256; operation++ {
		for _, bits := range []uint8{0, 8, 16, 32, 64, 128, 255} {
			for _, imm := range []uint64{0, 1, 3, 15, 31, 32, 33, 64, ^uint64(0)} {
				for alias := 0; alias < 2; alias++ {
					i := machine.Instruction{Op: machine.OpVector, Arithmetic: op.Opcode(operation), Bits: bits, Imm: imm, A: backend.VectorRegister(0), B: backend.VectorRegister(1), C: backend.VectorRegister(2)}
					if alias == 1 {
						i.B = i.A
					}
					switch i.Arithmetic {
					case op.OpVLoad, op.OpVSplat:
						i.B = 1
					case op.OpVStore, op.OpVExtract, op.OpVMaskBits:
						i.A = 0
					case op.OpVInsert, op.OpVInsertZero:
						i.C = 2
					}
					check(i, abi.StackNoUse, 0)
				}
			}
		}
	}
	// Vary registers (including invalid banks), slots, homes, address fields,
	// byte order and logical VectorType independently of the valid examples.
	r := rand.New(rand.NewSource(120064))
	widths := []uint8{0, 8, 16, 32, 64, 128, 255}
	immediates := []uint64{0, 1, 15, 31, 32, 33, 64, ^uint64(0)}
	slots := []int{-1, 0, 1, 2, 8190, 8191, 8192, int(^uint(0) >> 1)}
	for n := 0; n < 400000; n++ {
		i := machine.Instruction{Op: []machine.Opcode{machine.OpVector, machine.OpVectorMove, machine.OpVectorSpill, machine.OpVectorReload, 255}[r.Intn(5)], Arithmetic: op.Opcode(r.Intn(256)), Bits: widths[r.Intn(len(widths))], A: backend.Register(r.Intn(256)), B: backend.Register(r.Intn(256)), C: backend.Register(r.Intn(256)), Slot: slots[r.Intn(len(slots))], Imm: immediates[r.Intn(len(immediates))], ByteOrder: op.ByteOrder(r.Intn(5)), VectorType: abi.ValueType{Kind: abi.ValueKind(r.Intn(5)), Lanes: uint16(r.Intn(64)), LaneBits: widths[r.Intn(len(widths))]}}
		if n%2 == 0 {
			i.A = backend.VectorRegister(uint8(r.Intn(32)))
			i.B = backend.VectorRegister(uint8(r.Intn(32)))
			i.C = backend.VectorRegister(uint8(r.Intn(32)))
		}
		if n%3 == 0 {
			i.B = i.A
		}
		if n%7 == 0 {
			i.Disp = 1
		}
		if n%11 == 0 {
			i.Indexed = true
		}
		check(i, abi.StackPractice(r.Intn(2)), []int{-1, 0, 9, int(^uint(0) >> 1)}[r.Intn(4)])
	}
}
