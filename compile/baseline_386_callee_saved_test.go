package compile_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// A 386 C function without a frame keeps the cdecl callee-saved EBX, ESI and
// EDI: it saves the ones it writes in the workspace and restores them before
// returning.
func TestBaseline386StackNoUsePreservesCalleeSaved(t *testing.T) {
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "386", Tier: compile.TierBaseline})
	if err != nil {
		t.Fatal(err)
	}
	b := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})
	p := []nyanjit.Value{b.ParamBits(32), b.ParamBits(32), b.ParamBits(32)}
	var vs []nyanjit.Value
	for i := 0; i < 24; i++ {
		vs = append(vs, b.Add(p[i%3], b.ConstBits(32, uint64(i*7+1))))
	}
	acc := vs[0]
	for _, v := range vs[1:] {
		acc = b.Mul(acc, v)
	}
	for _, v := range vs {
		acc = b.Xor(acc, v)
	}
	b.Return(acc)
	u, err := b.Lower()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range u.Report().ClobberedRegisters() {
		if r == "EBX" || r == "ESI" || r == "EDI" {
			t.Errorf("report clobbers callee-saved %s", r)
		}
	}
	listing, err := u.Listing()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(listing), "\n")
	for _, r := range []string{"EBX", "ESI", "EDI"} {
		if !strings.Contains(listing, r) {
			continue
		}
		first := -1
		for i, line := range lines {
			if strings.Contains(line, r) {
				first = i
				break
			}
		}
		if !strings.HasPrefix(lines[first], "mov dword ptr [EBP + ") || !strings.HasSuffix(lines[first], ", "+r) {
			t.Errorf("%s is first used before it is saved: %s\n%s", r, lines[first], listing)
		}
		restore := fmt.Sprintf("mov %s, dword ptr [EBP + %s", r, strings.TrimSuffix(strings.TrimPrefix(lines[first], "mov dword ptr [EBP + "), "], "+r))
		found := false
		for _, line := range lines[len(lines)-5:] {
			found = found || strings.HasPrefix(line, restore)
		}
		if !found {
			t.Errorf("%s is not restored before ret:\n%s", r, listing)
		}
	}
}
