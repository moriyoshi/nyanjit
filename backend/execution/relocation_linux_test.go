//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le)

package execution

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"os"
	"strconv"
	"strings"
	"testing"
)

func mappingPermissions(t *testing.T, address uintptr) string {
	t.Helper()
	data, e := os.ReadFile("/proc/self/maps")
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		lo, hi, ok := strings.Cut(f[0], "-")
		if !ok {
			continue
		}
		a, e1 := strconv.ParseUint(lo, 16, 64)
		b, e2 := strconv.ParseUint(hi, 16, 64)
		if e1 == nil && e2 == nil && uint64(address) >= a && uint64(address) < b {
			return f[1]
		}
	}
	t.Fatalf("mapping %#x absent", address)
	return ""
}
func TestRelocationRunsBeforeExecutableSeal(t *testing.T) {
	seen := false
	mem, e := mapCode([]byte{1, 2, 3, 4}, func(m []byte) error {
		seen = true
		if p := mappingPermissions(t, Entry(m)); !strings.HasPrefix(p, "rw-") {
			return fmt.Errorf("relocation mapping permissions %s", p)
		}
		m[0] = 9
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	defer Unmap(mem)
	if !seen || mem[0] != 9 {
		t.Fatal("hook failed")
	}
	if p := mappingPermissions(t, Entry(mem)); !strings.HasPrefix(p, "r-x") {
		t.Fatalf("sealed permissions %s", p)
	}
	marker := errors.New("patch failure")
	m, e := mapCode([]byte{1}, func([]byte) error { return marker })
	if m != nil || !errors.Is(e, marker) {
		t.Fatalf("failed relocation returned %v %v", m, e)
	}
}
func TestMapWithRelocations(t *testing.T) {
	// No execution: an AMD64 immediate is an architecture-independent patch
	// fixture for this mapping test on every supported Linux architecture.
	code := []byte{0x48, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0}
	rs := []machine.Relocation{{Symbol: "value", Encoding: machine.RelocAMD64Immediate}}
	mem, e := MapWithRelocations(code, rs, map[string]uint64{"value": 0x123456789abcdef0})
	if e != nil {
		t.Fatal(e)
	}
	defer Unmap(mem)
	if binary.LittleEndian.Uint64(mem[2:]) != 0x123456789abcdef0 || code[2] != 0 {
		t.Fatal("mapping was not linked independently")
	}
	m, e := MapWithRelocations(code, rs, nil)
	if m != nil || !errors.Is(e, machine.ErrUnboundSymbol) {
		t.Fatalf("unbound: %v %v", m, e)
	}
}
