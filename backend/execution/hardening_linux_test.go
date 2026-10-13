//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package execution

import (
	"bytes"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"testing"
)

func TestHardenMappedEntryAndRegistration(t *testing.T) {
	for _, registered := range []bool{false, true} {
		body := securityReturnBytes()
		var address uintptr
		var register FrameRegistrar
		if registered {
			register = func(mem []byte) (bool, error) {
				address = Entry(mem)
				if !bytes.Equal(mem, body) {
					t.Fatal("registrar received prefix or padding")
				}
				return true, nil
			}
		}
		c, err := NewCode(body, CodeOptions{ABI: abi.C, Harden: true, GoManaged: registered}, register)
		if err != nil {
			t.Fatal(err)
		}
		offset := int(c.entry - Entry(c.mem))
		if offset < 16 || offset > 4096 || offset%16 != 0 || !bytes.Equal(c.mem[offset:offset+len(body)], body) {
			t.Fatalf("offset%d", offset)
		}
		if registered && address != c.entry {
			t.Fatal("registration entry differs from call entry")
		}
		if _, err = c.Call(); err != nil {
			t.Fatal(err)
		}
		if err = c.Close(); err != nil {
			t.Fatal(err)
		}
		if !registered && c.mem != nil {
			t.Fatal("Close retained unmanaged mapping")
		}
		if registered { // The test registrar retains no runtime metadata.
			if err := Unmap(c.mem); err != nil {
				t.Fatal(err)
			}
			c.mem = nil
		}
	}
}
