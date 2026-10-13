package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

// laneMoveProgram loads x and y of type t, applies one shuffle (indices
// non-nil) or extend to result type r, and stores the result.
func laneMoveProgram(t, r ir.Type, op ir.Opcode, indices []byte, imm uint64) *ir.Program {
	b := 3
	if op != ir.OpVShuffle {
		b = 0
	}
	return &ir.Program{Arity: 2, Result: 6, Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 64},
		{Op: ir.OpParam, Bits: 64},
		{Op: ir.OpVLoad, Type: t, Bits: t.LaneBits, A: 0},
		{Op: ir.OpVLoad, Type: t, Bits: t.LaneBits, A: 1},
		{Op: op, Type: r, Bits: r.LaneBits, A: 2, B: b, Data: indices, Imm: imm},
		{Op: ir.OpVStore, Type: r, Bits: r.LaneBits, A: 0, B: 4},
		{Op: ir.OpConst, Bits: 64},
	}}
}

func laneIndices(n int, f func(i int) int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(f(i))
	}
	return out
}

// TestLaneMoveLowering checks how shuffles and extends are classified on
// each target: SSE2 lacks general byte shuffles, NEON and LSX shuffle any
// 128-bit chunk, a wider shuffle splits when each output chunk draws on at
// most two input chunks, and an extend is native from a 128-bit input.
func TestLaneMoveLowering(t *testing.T) {
	reverse := func(n int) []byte { return laneIndices(n, func(i int) int { return n - 1 - i }) }
	interleave := func(n int) []byte { return laneIndices(n, func(i int) int { return i/2 + i%2*n }) }
	// Lanes 0..15 from x's high chunk reversed, 16..31 from y's low chunk.
	crossChunks := laneIndices(32, func(i int) int {
		if i < 16 {
			return 31 - i
		}
		return 32 + (i*7)%16
	})
	// Output chunk 0 draws on three input chunks.
	threeChunks := laneIndices(32, func(i int) int { return i * 5 % 64 })
	x86 := map[string]bool{"amd64": true, "386": true}
	for _, arch := range []string{"amd64", "386", "arm64", "loong64", "riscv64", "ppc64le"} {
		spec := TargetSpec{Architecture: arch}
		if arch == "loong64" {
			spec.Vectors = VectorsRequire("lsx")
		}
		native := arch != "riscv64" && arch != "ppc64le"
		pick := func(yes, no abi.VectorLowering, ok bool) abi.VectorLowering {
			if ok && native {
				return yes
			}
			return no
		}
		for _, test := range []struct {
			name    string
			t, r    ir.Type
			op      ir.Opcode
			indices []byte
			imm     uint64
			want    abi.VectorLowering
		}{
			{"reverse-64", ir.Vec(2, 64), ir.Vec(2, 64), ir.OpVShuffle, []byte{3, 0}, 0, pick(abi.Native, abi.Scalarized, true)},
			{"random-32", ir.Vec(4, 32), ir.Vec(4, 32), ir.OpVShuffle, []byte{6, 1, 7, 0}, 0, pick(abi.Native, abi.Scalarized, true)},
			{"reverse-8", ir.Vec(16, 8), ir.Vec(16, 8), ir.OpVShuffle, reverse(16), 0, pick(abi.Native, abi.Scalarized, !x86[arch])},
			{"interleave-8", ir.Vec(16, 8), ir.Vec(16, 8), ir.OpVShuffle, interleave(16), 0, pick(abi.Native, abi.Scalarized, true)},
			{"broadcast-8", ir.Vec(16, 8), ir.Vec(16, 8), ir.OpVShuffle, laneIndices(16, func(int) int { return 11 }), 0, pick(abi.Native, abi.Scalarized, true)},
			{"reverse-16", ir.Vec(8, 16), ir.Vec(8, 16), ir.OpVShuffle, reverse(8), 0, pick(abi.Native, abi.Scalarized, true)},
			{"cross-chunks", ir.Vec(32, 8), ir.Vec(32, 8), ir.OpVShuffle, crossChunks, 0, pick(abi.Split, abi.Scalarized, !x86[arch])},
			{"three-chunks", ir.Vec(32, 8), ir.Vec(32, 8), ir.OpVShuffle, threeChunks, 0, abi.Scalarized},
			{"chunk-identity", ir.Vec(8, 32), ir.Vec(8, 32), ir.OpVShuffle, []byte{12, 13, 14, 15, 0, 1, 2, 3}, 0, pick(abi.Split, abi.Scalarized, true)},
			{"widen-128", ir.Vec(8, 8), ir.Vec(8, 16), ir.OpVExtendUnsigned, nil, 0, abi.Scalarized},
			{"widen-256", ir.Vec(16, 8), ir.Vec(16, 16), ir.OpVExtendUnsigned, nil, 0, pick(abi.Split, abi.Scalarized, true)},
			{"widen-512", ir.Vec(8, 32), ir.Vec(8, 64), ir.OpVExtendSigned, nil, 0, pick(abi.Split, abi.Scalarized, true)},
			{"widen-half", ir.Vec(8, 16), ir.Vec(4, 32), ir.OpVExtendSigned, nil, 1, pick(abi.Native, abi.Scalarized, true)},
		} {
			p := laneMoveProgram(test.t, test.r, test.op, test.indices, test.imm)
			if err := p.Validate(); err != nil {
				t.Fatalf("%s/%s: %v", arch, test.name, err)
			}
			for _, vectors := range []VectorPolicy{spec.Vectors, VectorsOff()} {
				s := spec
				s.Vectors = vectors
				target := mustTarget(t, s)
				input, err := inputFromProgram(p, target.options(Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}))
				if err != nil {
					t.Fatalf("%s/%s: %v", arch, test.name, err)
				}
				want := test.want
				if vectors == VectorsOff() {
					want = abi.Scalarized
				}
				found := false
				for _, operation := range input.vectorOperations {
					if ir.Opcode(operation.Operation) == test.op {
						found = true
						if operation.Lowering != want || operation.Type != test.r {
							t.Errorf("%s/%s vectors=%v: report %v %+v, want %v", arch, test.name, vectors, operation.Lowering, operation.Type, want)
						}
					}
				}
				if !found {
					t.Errorf("%s/%s: no report", arch, test.name)
				}
				// Every target compiles every form, natively or not.
				if _, err := Lower(target, p, Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}); err != nil {
					t.Errorf("%s/%s vectors=%v: Lower: %v", arch, test.name, vectors, err)
				}
			}
		}
		// The shape-only query holds for every index set.
		info := mustTarget(t, spec).Vectors()
		for _, query := range []struct {
			op   ir.Opcode
			t    ir.Type
			want abi.VectorLowering
		}{
			{ir.OpVShuffle, ir.Vec(4, 32), pick(abi.Expanded, abi.Scalarized, true)},
			{ir.OpVShuffle, ir.Vec(16, 8), pick(abi.Expanded, abi.Scalarized, !x86[arch])},
			{ir.OpVShuffle, ir.Vec(8, 32), abi.Scalarized},
			{ir.OpVExtendUnsigned, ir.Vec(8, 16), abi.Scalarized},
			{ir.OpVExtendSigned, ir.Vec(16, 16), pick(abi.Split, abi.Scalarized, true)},
		} {
			if got := info.Lowering(query.op, query.t); got != query.want {
				t.Errorf("%s: Lowering(%v, %+v)=%v, want %v", arch, query.op, query.t, got, query.want)
			}
		}
	}
}

// TestLaneMovesWithVarsScalarize keeps a shuffle's input in a Var, so the
// optimized tier scalarizes the program.
func TestLaneMovesWithVarsScalarize(t *testing.T) {
	typ := ir.Vec(4, 32)
	b := builder.NewBuilder()
	a := must(b.ParamNative())
	v := must(b.Var(must(b.VLoad(typ, a))))
	x := must(b.Get(v))
	mustDo(b.VStore(a, must(b.ShuffleLanes(x, x, []uint16{1, 2, 3, 4}))))
	mustDo(b.Return(must(b.ConstBits(64, 0))))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	target := mustTarget(t, TargetSpec{Architecture: "arm64"})
	input, err := inputFromProgram(p, target.options(Func{}))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operation := range input.vectorOperations {
		if ir.Opcode(operation.Operation) == ir.OpVShuffle {
			found = true
			if operation.Lowering != abi.Scalarized {
				t.Fatalf("shuffle in a scoped program lowered %v", operation.Lowering)
			}
		}
	}
	if !found {
		t.Fatal("no shuffle report")
	}
}
