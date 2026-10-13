package execution

import (
	"encoding/binary"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"testing"
)

func TestHarden386LocalRelocationUsesBodyBase(t *testing.T) {
	body := make([]byte, 48)
	copy(body, []byte{0xf2, 0x0f, 0x10, 0x1d, 0, 0, 0, 0})
	relocations := []machine.Relocation{{Encoding: machine.Reloc386Literal, TargetOffset: 32}}
	template := NewTemplate(body, nil, relocations, CodeOptions{ABI: abi.C, Harden: true}, goregister.Frame{})
	for k := 0; k < 3; k++ {
		c, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		offset := int(c.entry - Entry(c.mem))
		if offset == 0 {
			t.Fatal("entry not randomized")
		}
		if got := binary.LittleEndian.Uint32(c.mem[offset+4:]); got != uint32(c.entry+32) {
			t.Fatalf("literal %x entry%x", got, c.entry)
		}
		c.Close()
	}
}
