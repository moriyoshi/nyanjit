package target_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

// Size functions measure forms rather than validate them, but these rejected
// forms once measured as accepted ones and must stay at zero bytes.
func TestSizeOfRejectedForms(t *testing.T) {
	cases := map[string][]string{
		"amd64":   {"OpBinaryImmediate:A=0 B=0 Arith=OpAdd Imm=1", "OpBinaryImmediate:A=0 B=0 Bits=64 Arith=OpOr Imm=0x80000000", "OpMulWide:A=1"},
		"arm64":   {"OpMulWide:A=1"},
		"riscv64": {"OpMulWide:A=5"},
		"loong64": {"OpMulWide:A=4"},
		"ppc64":   {"OpLiteralData:Data=000000"},
	}
	byName := make(map[string]machine.Opcode, len(opcodeNames))
	for i, name := range opcodeNames {
		byName[name] = machine.Opcode(i)
	}
	for _, target := range formTargets(t) {
		for _, form := range cases[target.dir] {
			name, example, _ := strings.Cut(form, ":")
			ins, err := parseForm(byName[name], example)
			if err != nil {
				t.Fatalf("%s %s: %v", target.dir, form, err)
			}
			if _, err := target.assemble(ins); err == nil {
				t.Errorf("%s %s: assembled", target.dir, form)
			}
			if size := target.size(ins); size != 0 {
				t.Errorf("%s %s: size function reports %d bytes", target.dir, form, size)
			}
		}
	}
}
