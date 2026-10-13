//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package execution_test

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/execution"
	"os"
	"testing"
	"unsafe"
)

func TestManagedMappingLeavesSpaceBeyondText(t *testing.T) {
	page := os.Getpagesize()
	var ranges [][2]uintptr
	for i := 0; i < 32; i++ {
		code, err := execution.NewCode(make([]byte, page), execution.CodeOptions{ABI: abi.Go, GoManaged: true}, func(text []byte) (bool, error) {
			if len(text) != page || cap(text) <= len(text) {
				t.Errorf("registered length/cap=%d/%d", len(text), cap(text))
			}
			start := uintptr(unsafe.Pointer(&text[0]))
			end := start + uintptr(len(text))
			for _, r := range ranges {
				if start <= r[1] && r[0] <= end {
					t.Errorf("touching managed mappings: %#x..%#x and %#x..%#x", start, end, r[0], r[1])
				}
			}
			ranges = append(ranges, [2]uintptr{start, end})
			return true, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
