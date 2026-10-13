package i386

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// floatCompareVectors were assembled by the Go toolchain (GOARCH=386 go tool
// asm, then go tool objdump) from the Go assembly in each comment; Go writes
// UCOMISx operands reversed, so "UCOMISS X2, X1" is ucomiss xmm1, xmm2, and
// "+1" is a label after the INC. Go's 386 assembler routes NEGL of
// ESP..EDI through XCHG with EBX, so those NEG bytes come from GOARCH=amd64,
// whose 32-bit form without REX is the same encoding.
// Fields: condition, Bits, A (GP), B and C (XMM numbers), code.
var floatCompareVectors = []struct {
	c       op.FloatCondition
	w       uint8
	d, a, b uint8
	code    string
}{
	{0, 32, 0, 1, 2, "31c00f2eca0f96c083d800"},   // XORL AX, AX; UCOMISS X2, X1; SETLS AX; SBBL $0, AX
	{1, 32, 0, 1, 2, "31c00f2eca0f97c083d000"},   // XORL AX, AX; UCOMISS X2, X1; SETHI AX; ADCL $0, AX
	{2, 32, 0, 1, 2, "31c00f2ed10f97c0"},         // XORL AX, AX; UCOMISS X1, X2; SETHI AX
	{3, 32, 0, 1, 2, "31c00f2ed10f96c0"},         // XORL AX, AX; UCOMISS X1, X2; SETLS AX
	{4, 32, 0, 1, 2, "31c00f2ed10f93c0"},         // XORL AX, AX; UCOMISS X1, X2; SETCC AX
	{5, 32, 0, 1, 2, "31c00f2ed10f92c0"},         // XORL AX, AX; UCOMISS X1, X2; SETCS AX
	{6, 32, 0, 1, 2, "31c00f2eca0f97c0"},         // XORL AX, AX; UCOMISS X2, X1; SETHI AX
	{7, 32, 0, 1, 2, "31c00f2eca0f96c0"},         // XORL AX, AX; UCOMISS X2, X1; SETLS AX
	{8, 32, 0, 1, 2, "31c00f2eca0f93c0"},         // XORL AX, AX; UCOMISS X2, X1; SETCC AX
	{9, 32, 0, 1, 2, "31c00f2eca0f92c0"},         // XORL AX, AX; UCOMISS X2, X1; SETCS AX
	{10, 32, 0, 1, 2, "31c00f2eca0f9bc0"},        // XORL AX, AX; UCOMISS X2, X1; SETPC AX
	{11, 32, 0, 1, 2, "31c00f2eca0f9ac0"},        // XORL AX, AX; UCOMISS X2, X1; SETPS AX
	{12, 32, 0, 1, 2, "31c00f2eca0f95c0"},        // XORL AX, AX; UCOMISS X2, X1; SETNE AX
	{13, 32, 0, 1, 2, "31c00f2eca0f94c0"},        // XORL AX, AX; UCOMISS X2, X1; SETEQ AX
	{0, 64, 0, 1, 2, "31c0660f2eca0f96c083d800"}, // XORL AX, AX; UCOMISD X2, X1; SETLS AX; SBBL $0, AX
	{1, 64, 0, 1, 2, "31c0660f2eca0f97c083d000"}, // XORL AX, AX; UCOMISD X2, X1; SETHI AX; ADCL $0, AX
	{2, 64, 0, 1, 2, "31c0660f2ed10f97c0"},       // XORL AX, AX; UCOMISD X1, X2; SETHI AX
	{3, 64, 0, 1, 2, "31c0660f2ed10f96c0"},       // XORL AX, AX; UCOMISD X1, X2; SETLS AX
	{4, 64, 0, 1, 2, "31c0660f2ed10f93c0"},       // XORL AX, AX; UCOMISD X1, X2; SETCC AX
	{5, 64, 0, 1, 2, "31c0660f2ed10f92c0"},       // XORL AX, AX; UCOMISD X1, X2; SETCS AX
	{6, 64, 0, 1, 2, "31c0660f2eca0f97c0"},       // XORL AX, AX; UCOMISD X2, X1; SETHI AX
	{7, 64, 0, 1, 2, "31c0660f2eca0f96c0"},       // XORL AX, AX; UCOMISD X2, X1; SETLS AX
	{8, 64, 0, 1, 2, "31c0660f2eca0f93c0"},       // XORL AX, AX; UCOMISD X2, X1; SETCC AX
	{9, 64, 0, 1, 2, "31c0660f2eca0f92c0"},       // XORL AX, AX; UCOMISD X2, X1; SETCS AX
	{10, 64, 0, 1, 2, "31c0660f2eca0f9bc0"},      // XORL AX, AX; UCOMISD X2, X1; SETPC AX
	{11, 64, 0, 1, 2, "31c0660f2eca0f9ac0"},      // XORL AX, AX; UCOMISD X2, X1; SETPS AX
	{12, 64, 0, 1, 2, "31c0660f2eca0f95c0"},      // XORL AX, AX; UCOMISD X2, X1; SETNE AX
	{13, 64, 0, 1, 2, "31c0660f2eca0f94c0"},      // XORL AX, AX; UCOMISD X2, X1; SETEQ AX
	{0, 32, 3, 5, 7, "31db0f2eef0f96c383db00"},   // XORL BX, BX; UCOMISS X7, X5; SETLS BX; SBBL $0, BX
	{1, 32, 3, 5, 7, "31db0f2eef0f97c383d300"},   // XORL BX, BX; UCOMISS X7, X5; SETHI BX; ADCL $0, BX
	{2, 32, 3, 5, 7, "31db0f2efd0f97c3"},         // XORL BX, BX; UCOMISS X5, X7; SETHI BX
	{3, 32, 3, 5, 7, "31db0f2efd0f96c3"},         // XORL BX, BX; UCOMISS X5, X7; SETLS BX
	{4, 32, 3, 5, 7, "31db0f2efd0f93c3"},         // XORL BX, BX; UCOMISS X5, X7; SETCC BX
	{5, 32, 3, 5, 7, "31db0f2efd0f92c3"},         // XORL BX, BX; UCOMISS X5, X7; SETCS BX
	{6, 32, 3, 5, 7, "31db0f2eef0f97c3"},         // XORL BX, BX; UCOMISS X7, X5; SETHI BX
	{7, 32, 3, 5, 7, "31db0f2eef0f96c3"},         // XORL BX, BX; UCOMISS X7, X5; SETLS BX
	{8, 32, 3, 5, 7, "31db0f2eef0f93c3"},         // XORL BX, BX; UCOMISS X7, X5; SETCC BX
	{9, 32, 3, 5, 7, "31db0f2eef0f92c3"},         // XORL BX, BX; UCOMISS X7, X5; SETCS BX
	{10, 32, 3, 5, 7, "31db0f2eef0f9bc3"},        // XORL BX, BX; UCOMISS X7, X5; SETPC BX
	{11, 32, 3, 5, 7, "31db0f2eef0f9ac3"},        // XORL BX, BX; UCOMISS X7, X5; SETPS BX
	{12, 32, 3, 5, 7, "31db0f2eef0f95c3"},        // XORL BX, BX; UCOMISS X7, X5; SETNE BX
	{13, 32, 3, 5, 7, "31db0f2eef0f94c3"},        // XORL BX, BX; UCOMISS X7, X5; SETEQ BX
	{0, 64, 3, 5, 7, "31db660f2eef0f96c383db00"}, // XORL BX, BX; UCOMISD X7, X5; SETLS BX; SBBL $0, BX
	{1, 64, 3, 5, 7, "31db660f2eef0f97c383d300"}, // XORL BX, BX; UCOMISD X7, X5; SETHI BX; ADCL $0, BX
	{2, 64, 3, 5, 7, "31db660f2efd0f97c3"},       // XORL BX, BX; UCOMISD X5, X7; SETHI BX
	{3, 64, 3, 5, 7, "31db660f2efd0f96c3"},       // XORL BX, BX; UCOMISD X5, X7; SETLS BX
	{4, 64, 3, 5, 7, "31db660f2efd0f93c3"},       // XORL BX, BX; UCOMISD X5, X7; SETCC BX
	{5, 64, 3, 5, 7, "31db660f2efd0f92c3"},       // XORL BX, BX; UCOMISD X5, X7; SETCS BX
	{6, 64, 3, 5, 7, "31db660f2eef0f97c3"},       // XORL BX, BX; UCOMISD X7, X5; SETHI BX
	{7, 64, 3, 5, 7, "31db660f2eef0f96c3"},       // XORL BX, BX; UCOMISD X7, X5; SETLS BX
	{8, 64, 3, 5, 7, "31db660f2eef0f93c3"},       // XORL BX, BX; UCOMISD X7, X5; SETCC BX
	{9, 64, 3, 5, 7, "31db660f2eef0f92c3"},       // XORL BX, BX; UCOMISD X7, X5; SETCS BX
	{10, 64, 3, 5, 7, "31db660f2eef0f9bc3"},      // XORL BX, BX; UCOMISD X7, X5; SETPC BX
	{11, 64, 3, 5, 7, "31db660f2eef0f9ac3"},      // XORL BX, BX; UCOMISD X7, X5; SETPS BX
	{12, 64, 3, 5, 7, "31db660f2eef0f95c3"},      // XORL BX, BX; UCOMISD X7, X5; SETNE BX
	{13, 64, 3, 5, 7, "31db660f2eef0f94c3"},      // XORL BX, BX; UCOMISD X7, X5; SETEQ BX
	{0, 32, 6, 0, 3, "31f60f2ec377014683de00"},   // XORL SI, SI; UCOMISS X3, X0; JHI +1; INCL SI; SBBL $0, SI
	{1, 32, 6, 0, 3, "31f60f2ec376014683d600"},   // XORL SI, SI; UCOMISS X3, X0; JLS +1; INCL SI; ADCL $0, SI
	{2, 32, 6, 0, 3, "31f60f2ed8760146"},         // XORL SI, SI; UCOMISS X0, X3; JLS +1; INCL SI
	{3, 32, 6, 0, 3, "31f60f2ed8770146"},         // XORL SI, SI; UCOMISS X0, X3; JHI +1; INCL SI
	{4, 32, 6, 0, 3, "0f2ed819f646"},             // UCOMISS X0, X3; SBBL SI, SI; INCL SI
	{5, 32, 6, 0, 3, "0f2ed819f6f7de"},           // UCOMISS X0, X3; SBBL SI, SI; NEGL SI
	{6, 32, 6, 0, 3, "31f60f2ec3760146"},         // XORL SI, SI; UCOMISS X3, X0; JLS +1; INCL SI
	{7, 32, 6, 0, 3, "31f60f2ec3770146"},         // XORL SI, SI; UCOMISS X3, X0; JHI +1; INCL SI
	{8, 32, 6, 0, 3, "0f2ec319f646"},             // UCOMISS X3, X0; SBBL SI, SI; INCL SI
	{9, 32, 6, 0, 3, "0f2ec319f6f7de"},           // UCOMISS X3, X0; SBBL SI, SI; NEGL SI
	{10, 32, 6, 0, 3, "31f60f2ec37a0146"},        // XORL SI, SI; UCOMISS X3, X0; JPS +1; INCL SI
	{11, 32, 6, 0, 3, "31f60f2ec37b0146"},        // XORL SI, SI; UCOMISS X3, X0; JPC +1; INCL SI
	{12, 32, 6, 0, 3, "31f60f2ec3740146"},        // XORL SI, SI; UCOMISS X3, X0; JEQ +1; INCL SI
	{13, 32, 6, 0, 3, "31f60f2ec3750146"},        // XORL SI, SI; UCOMISS X3, X0; JNE +1; INCL SI
	{0, 64, 6, 0, 3, "31f6660f2ec377014683de00"}, // XORL SI, SI; UCOMISD X3, X0; JHI +1; INCL SI; SBBL $0, SI
	{1, 64, 6, 0, 3, "31f6660f2ec376014683d600"}, // XORL SI, SI; UCOMISD X3, X0; JLS +1; INCL SI; ADCL $0, SI
	{2, 64, 6, 0, 3, "31f6660f2ed8760146"},       // XORL SI, SI; UCOMISD X0, X3; JLS +1; INCL SI
	{3, 64, 6, 0, 3, "31f6660f2ed8770146"},       // XORL SI, SI; UCOMISD X0, X3; JHI +1; INCL SI
	{4, 64, 6, 0, 3, "660f2ed819f646"},           // UCOMISD X0, X3; SBBL SI, SI; INCL SI
	{5, 64, 6, 0, 3, "660f2ed819f6f7de"},         // UCOMISD X0, X3; SBBL SI, SI; NEGL SI
	{6, 64, 6, 0, 3, "31f6660f2ec3760146"},       // XORL SI, SI; UCOMISD X3, X0; JLS +1; INCL SI
	{7, 64, 6, 0, 3, "31f6660f2ec3770146"},       // XORL SI, SI; UCOMISD X3, X0; JHI +1; INCL SI
	{8, 64, 6, 0, 3, "660f2ec319f646"},           // UCOMISD X3, X0; SBBL SI, SI; INCL SI
	{9, 64, 6, 0, 3, "660f2ec319f6f7de"},         // UCOMISD X3, X0; SBBL SI, SI; NEGL SI
	{10, 64, 6, 0, 3, "31f6660f2ec37a0146"},      // XORL SI, SI; UCOMISD X3, X0; JPS +1; INCL SI
	{11, 64, 6, 0, 3, "31f6660f2ec37b0146"},      // XORL SI, SI; UCOMISD X3, X0; JPC +1; INCL SI
	{12, 64, 6, 0, 3, "31f6660f2ec3740146"},      // XORL SI, SI; UCOMISD X3, X0; JEQ +1; INCL SI
	{13, 64, 6, 0, 3, "31f6660f2ec3750146"},      // XORL SI, SI; UCOMISD X3, X0; JNE +1; INCL SI
	{0, 32, 5, 6, 4, "31ed0f2ef477014583dd00"},   // XORL BP, BP; UCOMISS X4, X6; JHI +1; INCL BP; SBBL $0, BP
	{1, 32, 5, 6, 4, "31ed0f2ef476014583d500"},   // XORL BP, BP; UCOMISS X4, X6; JLS +1; INCL BP; ADCL $0, BP
	{2, 32, 5, 6, 4, "31ed0f2ee6760145"},         // XORL BP, BP; UCOMISS X6, X4; JLS +1; INCL BP
	{3, 32, 5, 6, 4, "31ed0f2ee6770145"},         // XORL BP, BP; UCOMISS X6, X4; JHI +1; INCL BP
	{4, 32, 5, 6, 4, "0f2ee619ed45"},             // UCOMISS X6, X4; SBBL BP, BP; INCL BP
	{5, 32, 5, 6, 4, "0f2ee619edf7dd"},           // UCOMISS X6, X4; SBBL BP, BP; NEGL BP
	{6, 32, 5, 6, 4, "31ed0f2ef4760145"},         // XORL BP, BP; UCOMISS X4, X6; JLS +1; INCL BP
	{7, 32, 5, 6, 4, "31ed0f2ef4770145"},         // XORL BP, BP; UCOMISS X4, X6; JHI +1; INCL BP
	{8, 32, 5, 6, 4, "0f2ef419ed45"},             // UCOMISS X4, X6; SBBL BP, BP; INCL BP
	{9, 32, 5, 6, 4, "0f2ef419edf7dd"},           // UCOMISS X4, X6; SBBL BP, BP; NEGL BP
	{10, 32, 5, 6, 4, "31ed0f2ef47a0145"},        // XORL BP, BP; UCOMISS X4, X6; JPS +1; INCL BP
	{11, 32, 5, 6, 4, "31ed0f2ef47b0145"},        // XORL BP, BP; UCOMISS X4, X6; JPC +1; INCL BP
	{12, 32, 5, 6, 4, "31ed0f2ef4740145"},        // XORL BP, BP; UCOMISS X4, X6; JEQ +1; INCL BP
	{13, 32, 5, 6, 4, "31ed0f2ef4750145"},        // XORL BP, BP; UCOMISS X4, X6; JNE +1; INCL BP
	{0, 64, 5, 6, 4, "31ed660f2ef477014583dd00"}, // XORL BP, BP; UCOMISD X4, X6; JHI +1; INCL BP; SBBL $0, BP
	{1, 64, 5, 6, 4, "31ed660f2ef476014583d500"}, // XORL BP, BP; UCOMISD X4, X6; JLS +1; INCL BP; ADCL $0, BP
	{2, 64, 5, 6, 4, "31ed660f2ee6760145"},       // XORL BP, BP; UCOMISD X6, X4; JLS +1; INCL BP
	{3, 64, 5, 6, 4, "31ed660f2ee6770145"},       // XORL BP, BP; UCOMISD X6, X4; JHI +1; INCL BP
	{4, 64, 5, 6, 4, "660f2ee619ed45"},           // UCOMISD X6, X4; SBBL BP, BP; INCL BP
	{5, 64, 5, 6, 4, "660f2ee619edf7dd"},         // UCOMISD X6, X4; SBBL BP, BP; NEGL BP
	{6, 64, 5, 6, 4, "31ed660f2ef4760145"},       // XORL BP, BP; UCOMISD X4, X6; JLS +1; INCL BP
	{7, 64, 5, 6, 4, "31ed660f2ef4770145"},       // XORL BP, BP; UCOMISD X4, X6; JHI +1; INCL BP
	{8, 64, 5, 6, 4, "660f2ef419ed45"},           // UCOMISD X4, X6; SBBL BP, BP; INCL BP
	{9, 64, 5, 6, 4, "660f2ef419edf7dd"},         // UCOMISD X4, X6; SBBL BP, BP; NEGL BP
	{10, 64, 5, 6, 4, "31ed660f2ef47a0145"},      // XORL BP, BP; UCOMISD X4, X6; JPS +1; INCL BP
	{11, 64, 5, 6, 4, "31ed660f2ef47b0145"},      // XORL BP, BP; UCOMISD X4, X6; JPC +1; INCL BP
	{12, 64, 5, 6, 4, "31ed660f2ef4740145"},      // XORL BP, BP; UCOMISD X4, X6; JEQ +1; INCL BP
	{13, 64, 5, 6, 4, "31ed660f2ef4750145"},      // XORL BP, BP; UCOMISD X4, X6; JNE +1; INCL BP
	{0, 32, 7, 2, 2, "31ff0f2ed277014783df00"},   // XORL DI, DI; UCOMISS X2, X2; JHI +1; INCL DI; SBBL $0, DI
	{1, 32, 7, 2, 2, "31ff0f2ed276014783d700"},   // XORL DI, DI; UCOMISS X2, X2; JLS +1; INCL DI; ADCL $0, DI
	{2, 32, 7, 2, 2, "31ff0f2ed2760147"},         // XORL DI, DI; UCOMISS X2, X2; JLS +1; INCL DI
	{3, 32, 7, 2, 2, "31ff0f2ed2770147"},         // XORL DI, DI; UCOMISS X2, X2; JHI +1; INCL DI
	{4, 32, 7, 2, 2, "0f2ed219ff47"},             // UCOMISS X2, X2; SBBL DI, DI; INCL DI
	{5, 32, 7, 2, 2, "0f2ed219fff7df"},           // UCOMISS X2, X2; SBBL DI, DI; NEGL DI
	{6, 32, 7, 2, 2, "31ff0f2ed2760147"},         // XORL DI, DI; UCOMISS X2, X2; JLS +1; INCL DI
	{7, 32, 7, 2, 2, "31ff0f2ed2770147"},         // XORL DI, DI; UCOMISS X2, X2; JHI +1; INCL DI
	{8, 32, 7, 2, 2, "0f2ed219ff47"},             // UCOMISS X2, X2; SBBL DI, DI; INCL DI
	{9, 32, 7, 2, 2, "0f2ed219fff7df"},           // UCOMISS X2, X2; SBBL DI, DI; NEGL DI
	{10, 32, 7, 2, 2, "31ff0f2ed27a0147"},        // XORL DI, DI; UCOMISS X2, X2; JPS +1; INCL DI
	{11, 32, 7, 2, 2, "31ff0f2ed27b0147"},        // XORL DI, DI; UCOMISS X2, X2; JPC +1; INCL DI
	{12, 32, 7, 2, 2, "31ff0f2ed2740147"},        // XORL DI, DI; UCOMISS X2, X2; JEQ +1; INCL DI
	{13, 32, 7, 2, 2, "31ff0f2ed2750147"},        // XORL DI, DI; UCOMISS X2, X2; JNE +1; INCL DI
	{0, 64, 7, 2, 2, "31ff660f2ed277014783df00"}, // XORL DI, DI; UCOMISD X2, X2; JHI +1; INCL DI; SBBL $0, DI
	{1, 64, 7, 2, 2, "31ff660f2ed276014783d700"}, // XORL DI, DI; UCOMISD X2, X2; JLS +1; INCL DI; ADCL $0, DI
	{2, 64, 7, 2, 2, "31ff660f2ed2760147"},       // XORL DI, DI; UCOMISD X2, X2; JLS +1; INCL DI
	{3, 64, 7, 2, 2, "31ff660f2ed2770147"},       // XORL DI, DI; UCOMISD X2, X2; JHI +1; INCL DI
	{4, 64, 7, 2, 2, "660f2ed219ff47"},           // UCOMISD X2, X2; SBBL DI, DI; INCL DI
	{5, 64, 7, 2, 2, "660f2ed219fff7df"},         // UCOMISD X2, X2; SBBL DI, DI; NEGL DI
	{6, 64, 7, 2, 2, "31ff660f2ed2760147"},       // XORL DI, DI; UCOMISD X2, X2; JLS +1; INCL DI
	{7, 64, 7, 2, 2, "31ff660f2ed2770147"},       // XORL DI, DI; UCOMISD X2, X2; JHI +1; INCL DI
	{8, 64, 7, 2, 2, "660f2ed219ff47"},           // UCOMISD X2, X2; SBBL DI, DI; INCL DI
	{9, 64, 7, 2, 2, "660f2ed219fff7df"},         // UCOMISD X2, X2; SBBL DI, DI; NEGL DI
	{10, 64, 7, 2, 2, "31ff660f2ed27a0147"},      // XORL DI, DI; UCOMISD X2, X2; JPS +1; INCL DI
	{11, 64, 7, 2, 2, "31ff660f2ed27b0147"},      // XORL DI, DI; UCOMISD X2, X2; JPC +1; INCL DI
	{12, 64, 7, 2, 2, "31ff660f2ed2740147"},      // XORL DI, DI; UCOMISD X2, X2; JEQ +1; INCL DI
	{13, 64, 7, 2, 2, "31ff660f2ed2750147"},      // XORL DI, DI; UCOMISD X2, X2; JNE +1; INCL DI
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
		e := NewByteAssembler(abi.StackPlatform)
		e.FloatCompare(v.c, v.w, d, a, b)
		if e.Err() != nil || string(e.Bytes()) != string(want) {
			t.Errorf("condition %d, %d bits, A=%d B=xmm%d C=xmm%d: % x, %v; want % x", v.c, v.w, v.d, v.a, v.b, e.Bytes(), e.Err(), want)
		}
		ins := machine.Instruction{Op: machine.OpFloatCompare, Bits: v.w, A: d, B: a, C: b, Imm: uint64(v.c)}
		if n := PhysicalSize(ins, abi.StackPlatform, 0); n != len(want) {
			t.Errorf("condition %d, %d bits: size %d, want %d", v.c, v.w, n, len(want))
		}
		if !FlagEffect(ins) {
			t.Errorf("condition %d: flag writes not reported", v.c)
		}
		text := NewTextAssembler(abi.StackPlatform)
		if err := machine.Visit(ins, text); err != nil || text.Err() != nil || string(text.Bytes()) != string(want) || !strings.Contains(text.Text(), "ucomis") {
			t.Errorf("condition %d, %d bits: listing %q, % x, %v %v", v.c, v.w, text.Text(), text.Bytes(), err, text.Err())
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
		{Op: machine.OpFloatCompare, Bits: 64, A: 1, B: backend.VectorRegister(2), C: backend.VectorRegister(8)},
		{Op: machine.OpFloatCompare, Bits: 64, A: 8, B: backend.VectorRegister(2), C: backend.VectorRegister(3)},
	} {
		if PhysicalSize(bad, abi.StackPlatform, 0) != 0 {
			t.Errorf("sized %+v", bad)
		}
		if bad.Imm > 0xff {
			continue // Visit narrows Imm; the size check above rejects it
		}
		e := NewByteAssembler(abi.StackPlatform)
		if machine.Visit(bad, e) == nil && e.Err() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
