//go:build linux && (386 || amd64 || arm64 || ppc64le || riscv64 || loong64)

package abicheck

import (
	"encoding/binary"
	"runtime"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
)

// clobberCode overwrites one C-preserved general register and, where the C ABI
// preserves any, one floating-point register, then returns. The amd64 code
// also sets the Go ABI's fixed-zero X15.
var clobberCode = map[string]struct {
	code     []byte
	words    []uint32
	c, goABI []string
}{
	// XORQ BX, BX; PCMPEQL X15, X15; RET
	"amd64": {code: []byte{0x48, 0x31, 0xdb, 0x66, 0x45, 0x0f, 0x76, 0xff, 0xc3}, c: []string{"RBX"}, goABI: []string{"XMM15"}},
	// XORL BX, BX; RET
	"386": {code: []byte{0x31, 0xdb, 0xc3}, c: []string{"EBX"}},
	// MOVD ZR, R19; FMOVD ZR, F8; RET
	"arm64": {words: []uint32{0xaa1f03f3, 0x9e6703e8, 0xd65f03c0}, c: []string{"X19", "V8"}},
	// MOVD $0, R14; FSUB F14, F14, F14; RET
	"ppc64le": {words: []uint32{0x39c00000, 0xfdce7028, 0x4e800020}, c: []string{"R14", "F14"}},
	// MOV X0, X18; MOVD $(0.0), F8; RET
	"riscv64": {words: []uint32{0x00000913, 0xf2000453, 0x00008067}, c: []string{"X18", "F8"}},
	// MOVV R0, R23; MOVV R0, F24; RET
	"loong64": {words: []uint32{0x00150017, 0x0114a818, 0x4c000020}, c: []string{"R23", "F24"}},
}

func TestCallCheckedDetectsClobbers(t *testing.T) {
	clobber := clobberCode[runtime.GOARCH]
	code := clobber.code
	for _, w := range clobber.words {
		code = binary.LittleEndian.AppendUint32(code, w)
	}
	mapped, err := execution.NewCode(code, execution.CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer mapped.Close()
	entry, err := mapped.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	for flavor, want := range map[abi.Flavor][]string{abi.C: clobber.c, abi.Go: clobber.goABI} {
		seed := seeds()
		regs := new(registerFile)
		*regs = seed
		callChecked(entry, new(execution.ArgumentWords), uintptr(flavor), 0, regs)
		if got := clobbered(flavor, &seed, regs); !slices.Equal(got, want) {
			t.Errorf("ABI %d: got %v, want %v", flavor, got, want)
		}
	}
}

func TestRegisterFileLayout(t *testing.T) {
	slots, recorded := 0, false
	for _, reg := range registers {
		if recorded && !reg.recorded {
			t.Fatalf("seeded %s follows a recorded register", reg.name)
		}
		recorded = reg.recorded
		slots++
		if reg.recorded {
			slots++
		}
	}
	if slots != registerSlots {
		t.Fatalf("registers occupy %d slots, registerFile has %d", slots, registerSlots)
	}
}
