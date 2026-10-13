package compile

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestBlindSelectedPreservesBorrowedInput(t *testing.T) {
	const payload uint64 = 0x3141592653589793
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "loong64", "ppc64le"} {
		target := newMachineTarget(mustTarget(t, TargetSpec{Architecture: arch}).options(Func{}))
		words := uint8(64 / target.RegisterBits())
		p := machineProgram{Values: []machine.SelectedValue{{Bits: 64, Words: words, Constant: true, Imm: payload, Param: -1, EntrySlot: -1, EntryReg: -1}}, Instructions: []machineInstruction{{Op: mConst, Dst: 0, Imm: payload}}, Result: 0, Scopes: []machine.SelectedScope{{Start: 0, End: 1}}}
		got, err := blindSelected(p, target, bytes.NewReader(bytes.Repeat([]byte{0xa5}, 64)))
		if err != nil {
			t.Fatal(err)
		}
		if got.Values[0].Constant || got.Values[0].Imm != 0 || got.Instructions[len(got.Instructions)-1].Arithmetic != ir.OpXor {
			t.Fatalf("%s missing runtime decode: %+v", arch, got)
		}
		for _, i := range got.Instructions {
			if i.Op == mConst && i.Imm == payload {
				t.Fatal("original payload reappeared")
			}
		}
		if !p.Values[0].Constant || p.Values[0].Imm != payload || len(p.Instructions) != 1 || p.Scopes[0].End != 1 {
			t.Fatal("borrowed input was mutated")
		}
		if got.Scopes[0].End != len(got.Instructions) {
			t.Fatal("scope end not rebased")
		}
		if _, err := blindSelected(p, target, bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
			t.Fatalf("random failure lost: %v", err)
		}
	}
}

func TestBlindVectorPoolWords(t *testing.T) {
	data := []byte{0x13, 0x37, 0x42, 0x24, 0x13, 0x37, 0x42, 0x24, 0x13, 0x37, 0x42, 0x24, 0x13, 0x37, 0x42, 0x24}
	p := machineProgram{Values: []machine.SelectedValue{{Class: backend.Vector, Words: 1}}, Instructions: []machineInstruction{{Op: mVector, Arithmetic: ir.OpVConst, Type: ir.Vec(4, 32), Bits: 32, Dst: 0, Data: data}}, Result: 0}
	got, err := blindSelected(p, newMachineTarget(mustTarget(t, TargetSpec{Architecture: "arm64"}).options(Func{})), bytes.NewReader(bytes.Repeat([]byte{0xa5}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Instructions) != 3 || got.Instructions[2].Arithmetic != ir.OpXor {
		t.Fatalf("missing vector decode: %+v", got.Instructions)
	}
	for _, i := range got.Instructions {
		if bytes.Equal(i.Data, data) {
			t.Fatal("unencoded vector payload retained")
		}
	}
	for n := range data {
		if got.Instructions[0].Data[n]^got.Instructions[1].Data[n] != data[n] {
			t.Fatal("vector bit pattern changed")
		}
	}
}
