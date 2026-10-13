package ir_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestSealOwnsSourceAndEditableClones(t *testing.T) {
	raw := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 32, Imm: 7}}, Result: 0}
	sealed, err := ir.Seal(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw.Instructions[0].Imm = 99
	clone := sealed.Clone()
	if sealed.Len() != 1 || sealed.Arity() != 0 || clone.Instructions[0].Imm != 7 {
		t.Fatal("sealed input aliases its source")
	}
	clone.Instructions[0].Imm = 23
	resealed, err := ir.Seal(clone)
	if err != nil || resealed.Clone().Instructions[0].Imm != 23 || sealed.Clone().Instructions[0].Imm != 7 {
		t.Fatalf("clone/reseal ownership: %v", err)
	}
	clone.Result = 99
	if _, err := ir.Seal(clone); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("invalid raw edit accepted: %v", err)
	}
}

func TestSealOwnsVectorPayload(t *testing.T) {
	data := make([]byte, 16)
	data[0] = 42
	raw := &ir.Program{Result: 1, Instructions: []ir.Instruction{
		{Op: ir.OpVConst, Type: ir.Vec(4, 32), Bits: 32, Data: data},
		{Op: ir.OpVExtract, Type: ir.Scalar(32), Bits: 32, A: 0},
	}}
	sealed, err := ir.Seal(raw)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 99
	copy := sealed.Clone()
	if copy.Instructions[0].Data[0] != 42 {
		t.Fatal("payload aliases caller bytes")
	}
	copy.Instructions[0].Data[0] = 7
	if sealed.Clone().Instructions[0].Data[0] != 42 {
		t.Fatal("editable clone changed sealed payload")
	}
}

func TestEmptySealedInput(t *testing.T) {
	var nilInput *ir.SealedProgram
	for _, p := range []*ir.SealedProgram{nilInput, {}} {
		if p.Clone() != nil || p.Len() != 0 || p.Arity() != 0 {
			t.Fatal("empty snapshot exposes data")
		}
	}
	if _, err := ir.Seal(nil); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatal(err)
	}
}

func TestSealedProgramPublicIdentity(t *testing.T) {
	typ := reflect.TypeOf(ir.SealedProgram{})
	if typ.Name() != "SealedProgram" || typ.PkgPath() != "github.com/moriyoshi/nyanjit/ir" {
		t.Fatalf("public identity: %v", typ)
	}
	pointer := reflect.PointerTo(typ)
	methods := []string{"Clone", "Len", "Arity", "Result", "Instructions", "Variables", "ScopeExits", "Control", "ResultParts", "ParameterTypes", "Analysis"}
	if pointer.NumMethod() != len(methods) {
		t.Fatalf("public methods: %d", pointer.NumMethod())
	}
	for _, name := range methods {
		if _, ok := pointer.MethodByName(name); !ok {
			t.Fatalf("missing method %s", name)
		}
	}
}
