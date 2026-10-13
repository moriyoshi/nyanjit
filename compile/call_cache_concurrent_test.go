package compile

import (
	"slices"
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func reviewEffectProgram(t testing.TB, extra string) *ir.SealedProgram {
	base, err := abi.PreflightCall(abi.CallSpec{Architecture: "arm64", Kind: abi.FunctionCall, ABI: abi.C, ArgumentCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	custom := base.Clone()
	custom.ClobberedRegisters = []string{"X0", extra}
	b := builder.NewBuilder()
	x := must(b.Param())
	r := must(b.CallCWithEffect(must(b.ConstNative(0x1234)), custom, x))
	mustDo(b.Return(r))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// RegisterScopes appends a custom effect onto the cached CallEffect
// slices. The arm64 cache entry has spare capacity, so concurrent callers write
// into the same shared backing array.
func TestReviewPreflightScopesAppendIntoCachedEffect(t *testing.T) {
	spec := TargetSpec{Architecture: "arm64"}
	f := Func{ABI: abi.C, Context: ContextForeign, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}
	effect, err := cachedCallEffect(abi.CallSpec{Architecture: "arm64", Kind: abi.FunctionCall, ABI: abi.C, ArgumentCount: 1, ArgumentWords: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cached arm64 C clobbers len=%d cap=%d; fp len=%d cap=%d", len(effect.ClobberedRegisters), cap(effect.ClobberedRegisters), len(effect.ClobberedFPRegisters), cap(effect.ClobberedFPRegisters))
	pa, pb := reviewEffectProgram(t, "X19"), reviewEffectProgram(t, "X20")
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := 0
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, mine, other := pa, "X19", "X20"
			if g%2 == 1 {
				p, mine, other = pb, "X20", "X19"
			}
			u, err := lowerUnit(p, spec, f)
			if err != nil {
				t.Error(err)
				return
			}
			for i := 0; i < 3000; i++ {
				s, err := u.RegisterScopes()
				if err != nil {
					t.Error(err)
					return
				}
				if !slices.Contains(s[0].ClobberedRegisters, mine) || slices.Contains(s[0].ClobberedRegisters, other) {
					mu.Lock()
					bad++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if bad != 0 {
		t.Fatalf("%d RegisterScopes results contained another goroutine's custom clobbers", bad)
	}
}
