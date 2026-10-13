//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386 || loong64)

package irconform

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// errorClass names the sentinel an error from compile.Lower wraps.
func errorClass(err error) string {
	for _, c := range []struct {
		err  error
		name string
	}{
		{compile.ErrInvalidFunction, "invalid"},
		{compile.ErrCPUUnsupported, "cpu"},
		{compile.ErrUnsupported, "unsupported"},
		{compile.ErrBackendLimit, "limit"},
	} {
		if errors.Is(err, c.err) {
			return c.name
		}
	}
	if err != nil {
		return err.Error()
	}
	return "ok"
}

// TestConformanceHardened runs the corpus with compile.Func.Harden at both
// tiers. The tiers accept and reject every case alike (backend limits may
// differ), and the cases they accept match the interpreter. riscv64 and
// loong64 have no speculation barrier, so both tiers reject the cases with
// control flow there.
func TestConformanceHardened(t *testing.T) {
	cases := Cases()
	tiers := []compile.Tier{compile.TierOptimized, compile.TierBaseline}
	var targets []compile.Target
	for _, tier := range tiers {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Skip(err)
		}
		targets = append(targets, target)
	}
	f := compile.Func{ABI: abi.C, Harden: true, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: stack}}
	rejected := 0
	for _, c := range cases {
		program, err := c.Build()
		if err != nil {
			continue
		}
		var classes [2]string
		for k, target := range targets {
			_, err := compile.Lower(target, program, f)
			classes[k] = errorClass(err)
		}
		if classes[0] != classes[1] && classes[0] != "limit" && classes[1] != "limit" {
			t.Errorf("%s: optimized %s, baseline %s", c.Name, classes[0], classes[1])
		}
		if classes[0] == "unsupported" {
			rejected++
		}
	}
	t.Logf("%d cases rejected as unsupported by both tiers", rejected)
	for k, target := range targets {
		name := [...]string{"tier-optimized", "tier-baseline"}[k]
		t.Run(name, func(t *testing.T) {
			var pass, skip, fail int
			for _, c := range cases {
				var reason string
				ok := t.Run(c.Name, func(t *testing.T) {
					defer func() {
						if t.Skipped() {
							reason = "skip"
						}
					}()
					check(t, target, c, true)
				})
				switch {
				case !ok:
					fail++
				case reason != "":
					skip++
				default:
					pass++
				}
			}
			t.Logf("%s hardened: %d pass, %d skip, %d fail", name, pass, skip, fail)
		})
	}
}
