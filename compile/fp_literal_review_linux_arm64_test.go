package compile_test

import (
	"math"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// The review's balanced integer tree keeps an early FP literal live in a
// multi-megabyte function; the final XORs make its result independently known.
func largeLiteralReviewProgram(n int) *ir.Program {
	ins := []ir.Instruction{{Op: ir.OpParam, Bits: 64}, {Op: ir.OpParam, Bits: 64}, {Op: ir.OpConst, Bits: 64, Imm: math.Float64bits(1.5)}, {Op: ir.OpFMul, Bits: 64, A: 0, B: 2}}
	level := make([]int, 0, n)
	for i := 0; i < n; i++ {
		ins = append(ins, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: uint64(i)*2654435761 + 12345})
		src := 1
		if i == 0 {
			src = 3
		}
		ins = append(ins, ir.Instruction{Op: ir.OpXor, Bits: 64, A: src, B: len(ins) - 1})
		level = append(level, len(ins)-1)
	}
	for len(level) > 1 {
		next := make([]int, 0, (len(level)+1)/2)
		for i := 0; i+1 < len(level); i += 2 {
			ins = append(ins, ir.Instruction{Op: ir.OpAdd, Bits: 64, A: level[i], B: level[i+1]})
			next = append(next, len(ins)-1)
		}
		if len(level)%2 != 0 {
			next = append(next, level[len(level)-1])
		}
		level = next
	}
	prev := level[0]
	ins = append(ins, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: math.Float64bits(2.5)})
	ins = append(ins, ir.Instruction{Op: ir.OpFMul, Bits: 64, A: 3, B: len(ins) - 1})
	ins = append(ins, ir.Instruction{Op: ir.OpXor, Bits: 64, A: prev, B: len(ins) - 1})
	ins = append(ins, ir.Instruction{Op: ir.OpXor, Bits: 64, A: prev, B: len(ins) - 1})
	return &ir.Program{Arity: 2, Result: len(ins) - 1, Instructions: ins}
}

func TestARM64LargeFunctionLiteralReach(t *testing.T) {
	p := largeLiteralReviewProgram(200000)
	options := abi.CompileOptions{}
	text, err := compile.EmitAssembly(p, options)
	if err != nil || !strings.Contains(text, "adr X16, .") {
		t.Fatalf("far literal listing: %v", err)
	}
	template, err := compile.Prepare(p, options)
	if err != nil {
		t.Fatal(err)
	}
	if template.Report().CodeBytes <= 1<<20 {
		t.Fatal("test did not exceed literal reach")
	}
	// Bind twice, keeping both mappings live: the long form must remap safely.
	first, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, code := range []*compile.Code{first, second} {
		got, err := code.Call(uintptr(math.Float64bits(3)), 0)
		if err != nil || uint64(got) != math.Float64bits(11.25) {
			t.Fatalf("large literal result: %#x,%v", got, err)
		}
	}
}
