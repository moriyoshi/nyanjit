package machine_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func Test386LiteralRelocation(t *testing.T) {
	for _, width := range []int{4, 8, 16} {
		prefix, opcode := byte(0xf3), byte(0x10)
		if width == 8 {
			prefix = 0xf2
		}
		if width == 16 {
			opcode = 0x6f
		}
		code := make([]byte, 48)
		copy(code, []byte{0xb8, 0, 0, 0, 0, prefix, 0x0f, opcode, 0x3d, 0, 0, 0, 0})
		copy(code[32:], []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
		original := bytes.Clone(code)
		rs := []machine.Relocation{{Symbol: "external", Encoding: machine.Reloc386Immediate, Offset: 0}, {Encoding: machine.Reloc386Literal, Offset: 5, TargetOffset: 32}}
		if _, e := machine.LinkRelocations(code, rs, map[string]uint64{"external": 17}); !errors.Is(e, machine.ErrCodeBaseRequired) {
			t.Fatalf("missing code base: %v", e)
		}
		for _, base := range []uint64{0, 0x12340000, 0xffffffff - 47} {
			out, e := machine.LinkRelocationsAt(code, rs, map[string]uint64{"external": 17}, base)
			if e != nil {
				t.Fatal(e)
			}
			if binary.LittleEndian.Uint32(out[9:]) != uint32(base+32) || binary.LittleEndian.Uint32(out[1:]) != 17 || !bytes.Equal(out[32:], code[32:]) {
				t.Fatalf("bad linked code: %x", out)
			}
		}
		if !bytes.Equal(code, original) {
			t.Fatal("link mutated template")
		}
		for _, base := range []uint64{1 << 32, 0xffffffff - 46, ^uint64(0)} {
			if _, e := machine.LinkRelocationsAt(code, rs, map[string]uint64{"external": 17}, base); !errors.Is(e, backend.ErrInvalidFunction) {
				t.Fatalf("overflow base %#x: %v", base, e)
			}
		}
		for _, off := range []int{-1, 6, 41, 100} {
			bad := append([]machine.Relocation(nil), rs...)
			bad[1].Offset = off
			if _, e := machine.LinkRelocationsAt(code, bad, map[string]uint64{"external": 17}, 0x10000); !errors.Is(e, backend.ErrInvalidFunction) {
				t.Fatalf("bad offset%d: %v", off, e)
			}
		}
		bad := append([]machine.Relocation(nil), rs...)
		bad[1].TargetOffset = uint64(len(code) - width + 1)
		if _, e := machine.LinkRelocationsAt(code, bad, map[string]uint64{"external": 17}, 0); !errors.Is(e, backend.ErrInvalidFunction) {
			t.Fatalf("truncated pool: %v", e)
		}
		bad[1].TargetOffset = 32
		bad[1].Symbol = "external"
		if _, e := machine.LinkRelocationsAt(code, bad, map[string]uint64{"external": 17}, 0); !errors.Is(e, backend.ErrInvalidFunction) {
			t.Fatalf("local symbol: %v", e)
		}
		if _, e := machine.LinkRelocationsAt(code, rs, nil, 0); !errors.Is(e, machine.ErrUnboundSymbol) {
			t.Fatalf("unbound external: %v", e)
		}
	}
}
