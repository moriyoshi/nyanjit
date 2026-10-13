package machine

import "testing"

func TestVectorRegistersReserveGoAndExpansionRegisters(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		seen := map[Register]bool{}
		want := 12
		if arch == "arm64" {
			want = 19
		}
		if got := len(VectorRegisters(arch)); got != want {
			t.Fatalf("%s pool has %d registers, want %d", arch, got, want)
		}
		for _, r := range VectorRegisters(arch) {
			n := VectorNumber(r)
			if !IsVectorRegister(r) || seen[r] {
				t.Fatalf("%s invalid pool register %d", arch, r)
			}
			seen[r] = true
			if arch == "amd64" && (n < 3 || n >= 15) || arch == "arm64" && n >= 8 && n < 19 || arch == "arm64" && n >= 30 {
				t.Fatalf("%s allocated reserved vector register %d", arch, n)
			}
		}
	}
}
