package compile

import (
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestEntryWordsFromFactsMatchesInstructionScan(t *testing.T) {
	for _, widths := range [][]uint8{nil, {0}, {8, 16, 32, 64}} {
		p := &ir.Program{Arity: len(widths)}
		for _, bits := range widths {
			p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpParam, Bits: bits})
		}
		// Non-parameter instructions do not contribute to the signature.
		p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpConst, Bits: 64})
		facts := newProgramFacts(p)
		for _, registerBits := range []uint8{32, 64} {
			want := entryWordsFromIRInto(p.Instructions, registerBits, []int{99})
			for _, context := range []*programFacts{nil, facts} {
				got := entryWordsWithFacts(p.Instructions, registerBits, []int{99}, context)
				if !slices.Equal(got, want) {
					t.Fatalf("widths %v, register bits %d: got %v, want %v", widths, registerBits, got, want)
				}
			}
		}
	}
	// Zero in cached metadata marks a vector, so use the instruction scan.
	ops := []instruction{{Op: opParam, Bits: 32}}
	facts := newProgramFacts(&ir.Program{Arity: 1, Instructions: []ir.Instruction{{Op: ir.OpParam, Type: ir.Vec(4, 32)}}})
	if bits := facts.EntryParamBits(); !slices.Equal(bits, []uint8{0}) {
		t.Fatalf("vector parameter metadata = %v", bits)
	}
	if got := entryWordsWithFacts(ops, 32, nil, facts); !slices.Equal(got, []int{1}) {
		t.Fatalf("vector metadata fallback: %v", got)
	}
}
