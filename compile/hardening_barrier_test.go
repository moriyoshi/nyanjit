package compile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

// Each target with a speculation barrier starts every hardened label with it
// and clamps the checked index; targets without one reject the control graph.
func TestHardenControlBarrierPerTarget(t *testing.T) {
	if builder.NativeBits != 64 {
		t.Skip("cross-target fixture uses 64-bit builder addresses")
	}
	b := nyanjit.NewBuilder()
	base, index, count := b.ParamNative(), b.ParamNative(), b.ParamNative()
	bad := b.NewLabel()
	b.Return(b.LoadBits(b.IndexedAddress(base, index, count, 1, bad), 8))
	b.Bind(bad)
	b.Return(b.ConstBits(8, 0))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	f := compile.Func{Context: compile.ContextExternal, Harden: true, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}
	for _, c := range []struct{ arch, barrier, clamp string }{
		{"amd64", "lfence", ".byte 48 0f 43 c1"}, // CMOVAE RAX, RCX
		{"arm64", "dsb sy", "csel"},
		{"ppc64le", "ori r31, r31, 0", "isel r3, r3, r4, 0"},
		{"riscv64", "", ""},
		{"loong64", "", ""},
	} {
		listing, err := lowerListing(p, compile.TargetSpec{Architecture: c.arch}, f)
		if c.barrier == "" {
			if !errors.Is(err, compile.ErrUnsupported) {
				t.Errorf("%s: hardened control flow accepted without a speculation barrier: %v", c.arch, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.arch, err)
		}
		if n := strings.Count(listing, c.barrier); n < 2 || !strings.Contains(listing, c.clamp) {
			t.Errorf("%s: %d barriers, clamp %v:\n%s", c.arch, n, strings.Contains(listing, c.clamp), listing)
		}
	}
}
