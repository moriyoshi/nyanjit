package compile_test

import (
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// managedTier is the tier the Go-managed tests lower at. The
// TestBaselineManaged tests run them again at TierBaseline.
var managedTier = compile.TierOptimized

func managedSpec() compile.TargetSpec { return compile.TargetSpec{Tier: managedTier} }

// atBaseline runs tests with managedSpec at TierBaseline.
func atBaseline(t *testing.T, tests map[string]func(*testing.T)) {
	t.Helper()
	managedTier = compile.TierBaseline
	defer func() { managedTier = compile.TierOptimized }()
	names := make([]string, 0, len(tests))
	for name := range tests {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		t.Run(name, tests[name])
	}
}

// managedCalls is a Go-managed function with a value live across a Go call
// and a C call.
func managedCalls(b *nyanjit.Builder) {
	x := b.Param()
	live := b.Xor(x, b.Const(7))
	r := b.CallGo(b.ConstNative(0x1000), x)
	r = b.CallC(b.ConstNative(0x2000), r, live)
	b.Return(b.Add(r, live))
}

// managedPreserved are the registers a C entry preserves that Go code may
// change, by architecture: those a Go-managed C entry with calls saves.
var managedPreserved = map[string][]string{
	"amd64":   {"RBX", "RBP", "R12", "R13", "R14", "R15"},
	"arm64":   {"X19", "X20", "X21", "X22", "X23", "X24", "X25", "X26", "X27", "X28", "X29", "V8", "V9", "V10", "V11", "V12", "V13", "V14", "V15"},
	"386":     {"EBX", "ESI", "EDI", "EBP"},
	"riscv64": {"X8", "X9", "X18", "X19", "X20", "X21", "X22", "X23", "X24", "X25", "X26", "X27", "F8", "F9", "F18", "F19", "F20", "F21", "F22", "F23", "F24", "F25", "F26", "F27"},
	"ppc64le": {"R14", "R15", "R16", "R17", "R18", "R19", "R20", "R21", "R22", "R23", "R24", "R25", "R26", "R27", "R28", "R29", "R30", "R31", "F14", "F31", "V20", "V31", "CR2", "CR3", "CR4"},
	"loong64": {"R22", "R23", "R24", "R25", "R26", "R27", "R28", "R29", "R30", "R31", "F24", "F25", "F26", "F27", "F28", "F29", "F30", "F31"},
}

// TestBaselineManagedEveryTarget lowers Go-managed functions at
// TierBaseline for every architecture: the report says the unit is
// Go-managed, a C entry's report keeps the C-preserved registers, the
// listing names the C call gateway, and only the host's units have a
// Template.
func TestBaselineManagedEveryTarget(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"} {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			f := compile.Func{ABI: flavor, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}, AssumePointerFree: true}
			var reports [2]abi.Report
			for k, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
				target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier})
				if err != nil {
					t.Fatal(err)
				}
				b := nyanjit.NewBuilderFor(target, f)
				managedCalls(b)
				u, err := b.Lower()
				if arch == "386" && tier == compile.TierOptimized && errors.Is(err, compile.ErrBackendLimit) {
					// The optimized tier's 386 Go calls do not take this
					// program's 64-bit words.
					reports[k] = nil
					continue
				}
				if err != nil {
					t.Fatalf("%s/%v tier %d: %v", arch, flavor, tier, err)
				}
				reports[k] = u.Report()
				if tier == compile.TierOptimized {
					continue
				}
				listing, err := u.Listing()
				if err != nil {
					t.Fatalf("%s/%v: listing: %v", arch, flavor, err)
				}
				_, terr := u.Template()
				// The loong64 listing does not name symbols.
				if arch != "loong64" && !strings.Contains(listing, "allBridge2") {
					t.Fatalf("%s/%v: no C call gateway in\n%s", arch, flavor, listing)
				}
				if arch != runtime.GOARCH {
					if !errors.Is(terr, compile.ErrUnsupported) {
						t.Fatalf("%s/%v: cross-target Template: %v", arch, flavor, terr)
					}
				}
				if arch == "ppc64le" && flavor == abi.C && (!strings.Contains(listing, "mfcr") || !strings.Contains(listing, "stvx v31")) {
					t.Fatalf("ppc64le C entry does not save CR and V20-V31:\n%s", listing)
				}
			}
			for k, r := range reports {
				if r == nil {
					continue
				}
				if !r.GoManaged() || r.ABI() != flavor || !r.HasFunctionCalls() || r.StackBytes() == 0 {
					t.Fatalf("%s/%v tier %d report: %+v", arch, flavor, k, r.Snapshot())
				}
				if flavor != abi.C {
					continue
				}
				clobbered := slices.Concat(r.ClobberedRegisters(), r.ClobberedFPRegisters(), r.ClobberedSpecialRegisters())
				for _, name := range managedPreserved[arch] {
					if slices.Contains(clobbered, name) {
						t.Fatalf("%s tier %d: C-preserved %s reported clobbered: %v", arch, k, name, clobbered)
					}
				}
			}
		}
	}
}

// TestBaselineManagedAcceptance checks that both tiers reject the same
// Go-managed functions the same way.
func TestBaselineManagedAcceptance(t *testing.T) {
	platform := compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}
	managed := compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: platform, AssumePointerFree: true}
	cases := []struct {
		name  string
		f     compile.Func
		build func(*nyanjit.Builder)
		lower error
		tmpl  error
	}{
		{"pointer maps", compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: platform}, managedCalls, compile.ErrPointerMapsRequired, nil},
		{"no stack", compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, AssumePointerFree: true}, managedCalls, compile.ErrStackRequired, nil},
		{"small stack", compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 16}, AssumePointerFree: true}, managedCalls, compile.ErrStackRequired, nil},
		{"syscall", managed, func(b *nyanjit.Builder) { b.Return(b.Syscall(b.Const(39))) }, compile.ErrExternalCallOnly, nil},
		{"dynamic Go target", managed, func(b *nyanjit.Builder) {
			x := b.ParamNative()
			b.Return(b.CallGo(b.Add(x, b.ConstNative(16)), x))
		}, nil, compile.ErrInvalidFunction},
	}
	for _, c := range cases {
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
			if err != nil {
				t.Fatal(err)
			}
			b := nyanjit.NewBuilderFor(target, c.f)
			c.build(b)
			u, err := b.Lower()
			if c.lower != nil || err != nil {
				if !errors.Is(err, c.lower) || c.lower == nil {
					t.Fatalf("%s tier %d: Lower = %v, want %v", c.name, tier, err, c.lower)
				}
				continue
			}
			if _, err := u.Template(); !errors.Is(err, c.tmpl) {
				t.Fatalf("%s tier %d: Template = %v, want %v", c.name, tier, err, c.tmpl)
			}
		}
	}
	// Frame limits can differ between the tiers; a baseline frame above
	// the managed-frame limit gives a LimitError at Template.
	target, err := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
	if err != nil {
		t.Fatal(err)
	}
	b := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 16}, AssumePointerFree: true})
	x := b.Param()
	var live []nyanjit.Value
	for k := range 120 {
		live = append(live, b.Xor(x, b.Const(uint64(k+3))))
	}
	sum := b.CallGo(b.ConstNative(0x1000), x)
	for _, v := range live {
		sum = b.Add(sum, v)
	}
	b.Return(sum)
	u, err := b.Lower()
	if err != nil {
		t.Fatal(err)
	}
	var limit *compile.LimitError
	if _, err := u.Template(); !errors.As(err, &limit) || limit.Kind != compile.LimitFrameBytes || limit.Needed != u.Report().StackBytes() {
		t.Fatalf("%d-byte frame: Template = %v", u.Report().StackBytes(), err)
	}
}
