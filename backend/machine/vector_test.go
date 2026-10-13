package machine

import (
	"github.com/moriyoshi/nyanjit/backend"
	"testing"
)

func TestVectorRegistersReserveGoAndExpansionRegisters(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "loong64"} {
		seen := map[backend.Register]bool{}
		want := 12
		if arch == "386" {
			want = 5
		}
		if arch == "arm64" {
			want = 19
		}
		if arch == "loong64" {
			want = 16
		}
		if got := VectorRegisters(arch).Len(); got != want {
			t.Fatalf("%s pool has %d registers, want %d", arch, got, want)
		}
		for r := range VectorRegisters(arch).All() {
			n := backend.VectorNumber(r)
			if !backend.IsVectorRegister(r) || seen[r] {
				t.Fatalf("%s invalid pool register %d", arch, r)
			}
			seen[r] = true
			if arch == "loong64" && (n < 8 || n >= 24) {
				t.Fatalf("allocated partial-preserved or expansion register f%d", n)
			}
			if arch == "386" && (n < 3 || n >= 8) || arch == "amd64" && (n < 3 || n >= 15) || arch == "arm64" && n >= 8 && n < 19 || arch == "arm64" && n >= 30 {
				t.Fatalf("%s allocated reserved vector register %d", arch, n)
			}
		}
	}
}
