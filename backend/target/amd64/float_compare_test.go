package amd64

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// floatCompareVectors were assembled by the Go toolchain (GOARCH=amd64 go
// tool asm, then go tool objdump) from the Go assembly in each comment; Go
// writes UCOMISx operands reversed, so "UCOMISS X2, X1" is ucomiss xmm1, xmm2.
// Fields: condition, Bits, A (GP), B and C (XMM numbers), code.
var floatCompareVectors = []struct {
	c       op.FloatCondition
	w       uint8
	d, a, b uint8
	code    string
}{
	{0, 32, 0, 1, 2, "31c00f2eca0f96c083d800"},              // XORL AX, AX; UCOMISS X2, X1; SETLS AX; SBBL $0, AX
	{1, 32, 0, 1, 2, "31c00f2eca0f97c083d000"},              // XORL AX, AX; UCOMISS X2, X1; SETHI AX; ADCL $0, AX
	{2, 32, 0, 1, 2, "31c00f2ed10f97c0"},                    // XORL AX, AX; UCOMISS X1, X2; SETHI AX
	{3, 32, 0, 1, 2, "31c00f2ed10f96c0"},                    // XORL AX, AX; UCOMISS X1, X2; SETLS AX
	{4, 32, 0, 1, 2, "31c00f2ed10f93c0"},                    // XORL AX, AX; UCOMISS X1, X2; SETCC AX
	{5, 32, 0, 1, 2, "31c00f2ed10f92c0"},                    // XORL AX, AX; UCOMISS X1, X2; SETCS AX
	{6, 32, 0, 1, 2, "31c00f2eca0f97c0"},                    // XORL AX, AX; UCOMISS X2, X1; SETHI AX
	{7, 32, 0, 1, 2, "31c00f2eca0f96c0"},                    // XORL AX, AX; UCOMISS X2, X1; SETLS AX
	{8, 32, 0, 1, 2, "31c00f2eca0f93c0"},                    // XORL AX, AX; UCOMISS X2, X1; SETCC AX
	{9, 32, 0, 1, 2, "31c00f2eca0f92c0"},                    // XORL AX, AX; UCOMISS X2, X1; SETCS AX
	{10, 32, 0, 1, 2, "31c00f2eca0f9bc0"},                   // XORL AX, AX; UCOMISS X2, X1; SETPC AX
	{11, 32, 0, 1, 2, "31c00f2eca0f9ac0"},                   // XORL AX, AX; UCOMISS X2, X1; SETPS AX
	{12, 32, 0, 1, 2, "31c00f2eca0f95c0"},                   // XORL AX, AX; UCOMISS X2, X1; SETNE AX
	{13, 32, 0, 1, 2, "31c00f2eca0f94c0"},                   // XORL AX, AX; UCOMISS X2, X1; SETEQ AX
	{0, 64, 0, 1, 2, "31c0660f2eca0f96c083d800"},            // XORL AX, AX; UCOMISD X2, X1; SETLS AX; SBBL $0, AX
	{1, 64, 0, 1, 2, "31c0660f2eca0f97c083d000"},            // XORL AX, AX; UCOMISD X2, X1; SETHI AX; ADCL $0, AX
	{2, 64, 0, 1, 2, "31c0660f2ed10f97c0"},                  // XORL AX, AX; UCOMISD X1, X2; SETHI AX
	{3, 64, 0, 1, 2, "31c0660f2ed10f96c0"},                  // XORL AX, AX; UCOMISD X1, X2; SETLS AX
	{4, 64, 0, 1, 2, "31c0660f2ed10f93c0"},                  // XORL AX, AX; UCOMISD X1, X2; SETCC AX
	{5, 64, 0, 1, 2, "31c0660f2ed10f92c0"},                  // XORL AX, AX; UCOMISD X1, X2; SETCS AX
	{6, 64, 0, 1, 2, "31c0660f2eca0f97c0"},                  // XORL AX, AX; UCOMISD X2, X1; SETHI AX
	{7, 64, 0, 1, 2, "31c0660f2eca0f96c0"},                  // XORL AX, AX; UCOMISD X2, X1; SETLS AX
	{8, 64, 0, 1, 2, "31c0660f2eca0f93c0"},                  // XORL AX, AX; UCOMISD X2, X1; SETCC AX
	{9, 64, 0, 1, 2, "31c0660f2eca0f92c0"},                  // XORL AX, AX; UCOMISD X2, X1; SETCS AX
	{10, 64, 0, 1, 2, "31c0660f2eca0f9bc0"},                 // XORL AX, AX; UCOMISD X2, X1; SETPC AX
	{11, 64, 0, 1, 2, "31c0660f2eca0f9ac0"},                 // XORL AX, AX; UCOMISD X2, X1; SETPS AX
	{12, 64, 0, 1, 2, "31c0660f2eca0f95c0"},                 // XORL AX, AX; UCOMISD X2, X1; SETNE AX
	{13, 64, 0, 1, 2, "31c0660f2eca0f94c0"},                 // XORL AX, AX; UCOMISD X2, X1; SETEQ AX
	{0, 32, 6, 9, 3, "31f6440f2ecb400f96c683de00"},          // XORL SI, SI; UCOMISS X3, X9; SETLS SI; SBBL $0, SI
	{1, 32, 6, 9, 3, "31f6440f2ecb400f97c683d600"},          // XORL SI, SI; UCOMISS X3, X9; SETHI SI; ADCL $0, SI
	{2, 32, 6, 9, 3, "31f6410f2ed9400f97c6"},                // XORL SI, SI; UCOMISS X9, X3; SETHI SI
	{3, 32, 6, 9, 3, "31f6410f2ed9400f96c6"},                // XORL SI, SI; UCOMISS X9, X3; SETLS SI
	{4, 32, 6, 9, 3, "31f6410f2ed9400f93c6"},                // XORL SI, SI; UCOMISS X9, X3; SETCC SI
	{5, 32, 6, 9, 3, "31f6410f2ed9400f92c6"},                // XORL SI, SI; UCOMISS X9, X3; SETCS SI
	{6, 32, 6, 9, 3, "31f6440f2ecb400f97c6"},                // XORL SI, SI; UCOMISS X3, X9; SETHI SI
	{7, 32, 6, 9, 3, "31f6440f2ecb400f96c6"},                // XORL SI, SI; UCOMISS X3, X9; SETLS SI
	{8, 32, 6, 9, 3, "31f6440f2ecb400f93c6"},                // XORL SI, SI; UCOMISS X3, X9; SETCC SI
	{9, 32, 6, 9, 3, "31f6440f2ecb400f92c6"},                // XORL SI, SI; UCOMISS X3, X9; SETCS SI
	{10, 32, 6, 9, 3, "31f6440f2ecb400f9bc6"},               // XORL SI, SI; UCOMISS X3, X9; SETPC SI
	{11, 32, 6, 9, 3, "31f6440f2ecb400f9ac6"},               // XORL SI, SI; UCOMISS X3, X9; SETPS SI
	{12, 32, 6, 9, 3, "31f6440f2ecb400f95c6"},               // XORL SI, SI; UCOMISS X3, X9; SETNE SI
	{13, 32, 6, 9, 3, "31f6440f2ecb400f94c6"},               // XORL SI, SI; UCOMISS X3, X9; SETEQ SI
	{0, 64, 6, 9, 3, "31f666440f2ecb400f96c683de00"},        // XORL SI, SI; UCOMISD X3, X9; SETLS SI; SBBL $0, SI
	{1, 64, 6, 9, 3, "31f666440f2ecb400f97c683d600"},        // XORL SI, SI; UCOMISD X3, X9; SETHI SI; ADCL $0, SI
	{2, 64, 6, 9, 3, "31f666410f2ed9400f97c6"},              // XORL SI, SI; UCOMISD X9, X3; SETHI SI
	{3, 64, 6, 9, 3, "31f666410f2ed9400f96c6"},              // XORL SI, SI; UCOMISD X9, X3; SETLS SI
	{4, 64, 6, 9, 3, "31f666410f2ed9400f93c6"},              // XORL SI, SI; UCOMISD X9, X3; SETCC SI
	{5, 64, 6, 9, 3, "31f666410f2ed9400f92c6"},              // XORL SI, SI; UCOMISD X9, X3; SETCS SI
	{6, 64, 6, 9, 3, "31f666440f2ecb400f97c6"},              // XORL SI, SI; UCOMISD X3, X9; SETHI SI
	{7, 64, 6, 9, 3, "31f666440f2ecb400f96c6"},              // XORL SI, SI; UCOMISD X3, X9; SETLS SI
	{8, 64, 6, 9, 3, "31f666440f2ecb400f93c6"},              // XORL SI, SI; UCOMISD X3, X9; SETCC SI
	{9, 64, 6, 9, 3, "31f666440f2ecb400f92c6"},              // XORL SI, SI; UCOMISD X3, X9; SETCS SI
	{10, 64, 6, 9, 3, "31f666440f2ecb400f9bc6"},             // XORL SI, SI; UCOMISD X3, X9; SETPC SI
	{11, 64, 6, 9, 3, "31f666440f2ecb400f9ac6"},             // XORL SI, SI; UCOMISD X3, X9; SETPS SI
	{12, 64, 6, 9, 3, "31f666440f2ecb400f95c6"},             // XORL SI, SI; UCOMISD X3, X9; SETNE SI
	{13, 64, 6, 9, 3, "31f666440f2ecb400f94c6"},             // XORL SI, SI; UCOMISD X3, X9; SETEQ SI
	{0, 32, 13, 12, 15, "4531ed450f2ee7410f96c54183dd00"},   // XORL R13, R13; UCOMISS X15, X12; SETLS R13; SBBL $0, R13
	{1, 32, 13, 12, 15, "4531ed450f2ee7410f97c54183d500"},   // XORL R13, R13; UCOMISS X15, X12; SETHI R13; ADCL $0, R13
	{2, 32, 13, 12, 15, "4531ed450f2efc410f97c5"},           // XORL R13, R13; UCOMISS X12, X15; SETHI R13
	{3, 32, 13, 12, 15, "4531ed450f2efc410f96c5"},           // XORL R13, R13; UCOMISS X12, X15; SETLS R13
	{4, 32, 13, 12, 15, "4531ed450f2efc410f93c5"},           // XORL R13, R13; UCOMISS X12, X15; SETCC R13
	{5, 32, 13, 12, 15, "4531ed450f2efc410f92c5"},           // XORL R13, R13; UCOMISS X12, X15; SETCS R13
	{6, 32, 13, 12, 15, "4531ed450f2ee7410f97c5"},           // XORL R13, R13; UCOMISS X15, X12; SETHI R13
	{7, 32, 13, 12, 15, "4531ed450f2ee7410f96c5"},           // XORL R13, R13; UCOMISS X15, X12; SETLS R13
	{8, 32, 13, 12, 15, "4531ed450f2ee7410f93c5"},           // XORL R13, R13; UCOMISS X15, X12; SETCC R13
	{9, 32, 13, 12, 15, "4531ed450f2ee7410f92c5"},           // XORL R13, R13; UCOMISS X15, X12; SETCS R13
	{10, 32, 13, 12, 15, "4531ed450f2ee7410f9bc5"},          // XORL R13, R13; UCOMISS X15, X12; SETPC R13
	{11, 32, 13, 12, 15, "4531ed450f2ee7410f9ac5"},          // XORL R13, R13; UCOMISS X15, X12; SETPS R13
	{12, 32, 13, 12, 15, "4531ed450f2ee7410f95c5"},          // XORL R13, R13; UCOMISS X15, X12; SETNE R13
	{13, 32, 13, 12, 15, "4531ed450f2ee7410f94c5"},          // XORL R13, R13; UCOMISS X15, X12; SETEQ R13
	{0, 64, 13, 12, 15, "4531ed66450f2ee7410f96c54183dd00"}, // XORL R13, R13; UCOMISD X15, X12; SETLS R13; SBBL $0, R13
	{1, 64, 13, 12, 15, "4531ed66450f2ee7410f97c54183d500"}, // XORL R13, R13; UCOMISD X15, X12; SETHI R13; ADCL $0, R13
	{2, 64, 13, 12, 15, "4531ed66450f2efc410f97c5"},         // XORL R13, R13; UCOMISD X12, X15; SETHI R13
	{3, 64, 13, 12, 15, "4531ed66450f2efc410f96c5"},         // XORL R13, R13; UCOMISD X12, X15; SETLS R13
	{4, 64, 13, 12, 15, "4531ed66450f2efc410f93c5"},         // XORL R13, R13; UCOMISD X12, X15; SETCC R13
	{5, 64, 13, 12, 15, "4531ed66450f2efc410f92c5"},         // XORL R13, R13; UCOMISD X12, X15; SETCS R13
	{6, 64, 13, 12, 15, "4531ed66450f2ee7410f97c5"},         // XORL R13, R13; UCOMISD X15, X12; SETHI R13
	{7, 64, 13, 12, 15, "4531ed66450f2ee7410f96c5"},         // XORL R13, R13; UCOMISD X15, X12; SETLS R13
	{8, 64, 13, 12, 15, "4531ed66450f2ee7410f93c5"},         // XORL R13, R13; UCOMISD X15, X12; SETCC R13
	{9, 64, 13, 12, 15, "4531ed66450f2ee7410f92c5"},         // XORL R13, R13; UCOMISD X15, X12; SETCS R13
	{10, 64, 13, 12, 15, "4531ed66450f2ee7410f9bc5"},        // XORL R13, R13; UCOMISD X15, X12; SETPC R13
	{11, 64, 13, 12, 15, "4531ed66450f2ee7410f9ac5"},        // XORL R13, R13; UCOMISD X15, X12; SETPS R13
	{12, 64, 13, 12, 15, "4531ed66450f2ee7410f95c5"},        // XORL R13, R13; UCOMISD X15, X12; SETNE R13
	{13, 64, 13, 12, 15, "4531ed66450f2ee7410f94c5"},        // XORL R13, R13; UCOMISD X15, X12; SETEQ R13
	{0, 32, 8, 0, 8, "4531c0410f2ec0410f96c04183d800"},      // XORL R8, R8; UCOMISS X8, X0; SETLS R8; SBBL $0, R8
	{1, 32, 8, 0, 8, "4531c0410f2ec0410f97c04183d000"},      // XORL R8, R8; UCOMISS X8, X0; SETHI R8; ADCL $0, R8
	{2, 32, 8, 0, 8, "4531c0440f2ec0410f97c0"},              // XORL R8, R8; UCOMISS X0, X8; SETHI R8
	{3, 32, 8, 0, 8, "4531c0440f2ec0410f96c0"},              // XORL R8, R8; UCOMISS X0, X8; SETLS R8
	{4, 32, 8, 0, 8, "4531c0440f2ec0410f93c0"},              // XORL R8, R8; UCOMISS X0, X8; SETCC R8
	{5, 32, 8, 0, 8, "4531c0440f2ec0410f92c0"},              // XORL R8, R8; UCOMISS X0, X8; SETCS R8
	{6, 32, 8, 0, 8, "4531c0410f2ec0410f97c0"},              // XORL R8, R8; UCOMISS X8, X0; SETHI R8
	{7, 32, 8, 0, 8, "4531c0410f2ec0410f96c0"},              // XORL R8, R8; UCOMISS X8, X0; SETLS R8
	{8, 32, 8, 0, 8, "4531c0410f2ec0410f93c0"},              // XORL R8, R8; UCOMISS X8, X0; SETCC R8
	{9, 32, 8, 0, 8, "4531c0410f2ec0410f92c0"},              // XORL R8, R8; UCOMISS X8, X0; SETCS R8
	{10, 32, 8, 0, 8, "4531c0410f2ec0410f9bc0"},             // XORL R8, R8; UCOMISS X8, X0; SETPC R8
	{11, 32, 8, 0, 8, "4531c0410f2ec0410f9ac0"},             // XORL R8, R8; UCOMISS X8, X0; SETPS R8
	{12, 32, 8, 0, 8, "4531c0410f2ec0410f95c0"},             // XORL R8, R8; UCOMISS X8, X0; SETNE R8
	{13, 32, 8, 0, 8, "4531c0410f2ec0410f94c0"},             // XORL R8, R8; UCOMISS X8, X0; SETEQ R8
	{0, 64, 8, 0, 8, "4531c066410f2ec0410f96c04183d800"},    // XORL R8, R8; UCOMISD X8, X0; SETLS R8; SBBL $0, R8
	{1, 64, 8, 0, 8, "4531c066410f2ec0410f97c04183d000"},    // XORL R8, R8; UCOMISD X8, X0; SETHI R8; ADCL $0, R8
	{2, 64, 8, 0, 8, "4531c066440f2ec0410f97c0"},            // XORL R8, R8; UCOMISD X0, X8; SETHI R8
	{3, 64, 8, 0, 8, "4531c066440f2ec0410f96c0"},            // XORL R8, R8; UCOMISD X0, X8; SETLS R8
	{4, 64, 8, 0, 8, "4531c066440f2ec0410f93c0"},            // XORL R8, R8; UCOMISD X0, X8; SETCC R8
	{5, 64, 8, 0, 8, "4531c066440f2ec0410f92c0"},            // XORL R8, R8; UCOMISD X0, X8; SETCS R8
	{6, 64, 8, 0, 8, "4531c066410f2ec0410f97c0"},            // XORL R8, R8; UCOMISD X8, X0; SETHI R8
	{7, 64, 8, 0, 8, "4531c066410f2ec0410f96c0"},            // XORL R8, R8; UCOMISD X8, X0; SETLS R8
	{8, 64, 8, 0, 8, "4531c066410f2ec0410f93c0"},            // XORL R8, R8; UCOMISD X8, X0; SETCC R8
	{9, 64, 8, 0, 8, "4531c066410f2ec0410f92c0"},            // XORL R8, R8; UCOMISD X8, X0; SETCS R8
	{10, 64, 8, 0, 8, "4531c066410f2ec0410f9bc0"},           // XORL R8, R8; UCOMISD X8, X0; SETPC R8
	{11, 64, 8, 0, 8, "4531c066410f2ec0410f9ac0"},           // XORL R8, R8; UCOMISD X8, X0; SETPS R8
	{12, 64, 8, 0, 8, "4531c066410f2ec0410f95c0"},           // XORL R8, R8; UCOMISD X8, X0; SETNE R8
	{13, 64, 8, 0, 8, "4531c066410f2ec0410f94c0"},           // XORL R8, R8; UCOMISD X8, X0; SETEQ R8
}

func TestFloatCompareEncoding(t *testing.T) {
	seen := map[[2]int]bool{}
	for _, v := range floatCompareVectors {
		want, err := hex.DecodeString(v.code)
		if err != nil {
			t.Fatal(err)
		}
		seen[[2]int{int(v.c), int(v.w)}] = true
		d, a, b := backend.Register(v.d), backend.VectorRegister(v.a), backend.VectorRegister(v.b)
		e := NewByteAssembler(abi.StackPlatform, 0)
		e.FloatCompare(v.c, v.w, d, a, b)
		if e.Err() != nil || string(e.Bytes()) != string(want) {
			t.Errorf("condition %d, %d bits, A=%d B=xmm%d C=xmm%d: % x, %v; want % x", v.c, v.w, v.d, v.a, v.b, e.Bytes(), e.Err(), want)
		}
		ins := machine.Instruction{Op: machine.OpFloatCompare, Bits: v.w, A: d, B: a, C: b, Imm: uint64(v.c)}
		if n := PhysicalSize(ins, abi.StackPlatform, 0); n != len(want) {
			t.Errorf("condition %d, %d bits: size %d, want %d", v.c, v.w, n, len(want))
		}
		if !FlagEffect(ins) || FlagFacts(ins).Modified == 0 {
			t.Errorf("condition %d: flag writes not reported", v.c)
		}
		text := NewTextAssembler(abi.StackPlatform, 0)
		if err := machine.Visit(ins, text); err != nil || text.Err() != nil || text.AssembledSize() != len(want) || !strings.Contains(text.Text(), "ucomis") {
			t.Errorf("condition %d, %d bits: listing %q (%d bytes), %v %v", v.c, v.w, text.Text(), text.AssembledSize(), err, text.Err())
		}
	}
	if len(seen) != 28 {
		t.Errorf("vectors cover %d condition and width pairs, want 28", len(seen))
	}
	for _, bad := range []machine.Instruction{
		{Op: machine.OpFloatCompare, Bits: 16, A: 1, B: backend.VectorRegister(2), C: backend.VectorRegister(3)},
		{Op: machine.OpFloatCompare, Bits: 64, A: 1, B: backend.VectorRegister(2), C: backend.VectorRegister(3), Imm: 14},
		{Op: machine.OpFloatCompare, Bits: 64, A: 1, B: backend.VectorRegister(2), C: backend.VectorRegister(3), Imm: 256},
		{Op: machine.OpFloatCompare, Bits: 64, A: backend.VectorRegister(1), B: backend.VectorRegister(2), C: backend.VectorRegister(3)},
		{Op: machine.OpFloatCompare, Bits: 64, A: 1, B: 2, C: backend.VectorRegister(3)},
		{Op: machine.OpFloatCompare, Bits: 64, A: 1, B: backend.VectorRegister(2), C: backend.VectorRegister(16)},
	} {
		if PhysicalSize(bad, abi.StackPlatform, 0) != 0 {
			t.Errorf("sized %+v", bad)
		}
		if bad.Imm > 0xff {
			continue // Visit narrows Imm; the size check above rejects it
		}
		e := NewByteAssembler(abi.StackPlatform, 0)
		if machine.Visit(bad, e) == nil && e.Err() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
