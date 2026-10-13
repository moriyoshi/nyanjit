package execution

import (
	"bytes"
	"encoding/binary"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"testing"
)

func Test386TemplateBindsOwnLiteralAddresses(t *testing.T) {
	code := make([]byte, 48)
	copy(code, []byte{0xf2, 0x0f, 0x10, 0x1d, 0, 0, 0, 0})
	copy(code[32:], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	rs := []machine.Relocation{{Encoding: machine.Reloc386Literal, TargetOffset: 32}}
	resolved := false
	tmpl := NewTemplateWithResolver(code, abi.NewReport(abi.EmissionReport{Architecture: "386"}), rs, CodeOptions{ABI: abi.C}, goregister.Frame{}, func(symbols []machine.Relocation, bindings map[string]uint64) (map[string]uint64, error) {
		resolved = true
		if len(symbols) != 0 {
			t.Errorf("symbol resolver received local-address records: %+v", symbols)
		}
		return bindings, nil
	})
	// Both mappings remain live, so copying a patched first address into the
	// second mapping cannot accidentally work through address reuse.
	a, e := tmpl.Bind(nil)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := tmpl.Bind(nil)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if !resolved {
		t.Fatal("missing symbol resolver")
	}
	if a.entry == b.entry {
		t.Fatal("distinct live bindings share a mapping")
	}
	for _, c := range []*Code{a, b} {
		if got := binary.LittleEndian.Uint32(c.mem[4:]); got != uint32(c.entry+32) {
			t.Fatalf("literal address %#x, expected %#x", got, c.entry+32)
		}
	}
	if !bytes.Equal(code, tmpl.code) || binary.LittleEndian.Uint32(tmpl.code[4:]) != 0 {
		t.Fatal("Bind patched the reusable template")
	}
	clone := tmpl.Relocations()
	clone[0].TargetOffset = 40
	if tmpl.Relocations()[0].TargetOffset != 32 {
		t.Fatal("relocation accessor aliases template")
	}
}

func Test386LiteralPatchedBeforeFrameRegistration(t *testing.T) {
	code := make([]byte, 48)
	copy(code, []byte{0xf2, 0x0f, 0x10, 0x1d, 0, 0, 0, 0})
	rs := []machine.Relocation{{Encoding: machine.Reloc386Literal, TargetOffset: 32}}
	seen := false
	var mapping []byte
	c, e := newCode(code, CodeOptions{ABI: abi.Go, GoManaged: true}, func(mem []byte) (bool, error) {
		seen = true
		mapping = mem[:len(mem)+1]
		if binary.LittleEndian.Uint32(mem[4:]) != uint32(Entry(mem)+32) {
			t.Error("runtime registration saw unpatched literal")
		}
		return true, nil
	}, rs, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !seen {
		t.Fatal("missing registrar")
	}
	c.Close()
	// This test registrar retains no runtime metadata, so release its mapping.
	if e := Unmap(mapping); e != nil {
		t.Fatal(e)
	}
}
