package compile

import (
	"embed"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

//go:embed testdata/narrow-pressure/*.json
var round12RetryFixtures embed.FS

func round12RetryFixture(t *testing.T, name string) *ir.Program {
	t.Helper()
	data, err := round12RetryFixtures.ReadFile("testdata/narrow-pressure/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var p ir.Program
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	return &p
}

// Both fixtures need a larger loong64 frame for their native narrow stores
// than for the byte-store plan, and land the native plan above the
// 640-byte managed-frame limit under one allocator each. The native Store16
// pressure fixture plus 35 simultaneously live 32-bit Vars lowers at 656
// bytes with the direct walk and at 640 with Online; 386-1720000259's control
// program with 18 32-bit Vars live from its entry lowers at 656 bytes with
// Online and at 640 with the direct walk. Their byte plans fit 640.
var round12ManagedFixtures = []string{"loong64-managed-frame.json", "loong64-managed-control-frame.json"}

// An ordinary managed plan can lower successfully above the managed-frame
// limit; only callable-template construction used to reject it.
func TestRound12ManagedNarrowFrameRetry(t *testing.T) {
	// The fixtures' frames come from their Var homes.
	withoutVarPromotion(t)
	spec := TargetSpec{Architecture: "loong64"}
	retried := 0
	for _, name := range round12ManagedFixtures {
		p := round12RetryFixture(t, name)
		for _, harden := range []bool{false, true} {
			if harden && p.Control != nil {
				continue // loong64 hardens no control flow
			}
			f := Func{ABI: abi.C, Context: ContextGoManaged, AssumePointerFree: true, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, Harden: harden}
			o := mustTarget(t, spec).options(f)
			limit := newMachineTarget(o).ManagedFrameLimit()
			input, err := inputForLowering(p, true, o)
			if err != nil {
				t.Fatal(err)
			}
			native, err := lowerUnitInputOnce(input, o, emitLayoutText, boundaryManaged)
			if err != nil {
				t.Fatal(err)
			}
			unit, err := lowerUnit(p, spec, f)
			if err != nil {
				t.Fatal(err)
			}
			report := unit.Report()
			if native.report.StackBytes > limit {
				retried++
				if _, err := native.Template(); !errors.Is(err, ErrBackendLimit) {
					t.Fatalf("%s: native frame=%d Template=%v, want frame limit", name, native.report.StackBytes, err)
				}
				if report.StackBytes() > limit || report.LoweringStats().NarrowMemoryRetries != 1 {
					t.Fatalf("%s Harden=%v: retry frame=%d %+v, want the byte plan within %d", name, harden, report.StackBytes(), report.LoweringStats(), limit)
				}
			} else if report.StackBytes() != native.report.StackBytes || report.LoweringStats().NarrowMemoryRetries != 0 {
				t.Fatalf("%s Harden=%v: frame=%d %+v, want the native plan's %d", name, harden, report.StackBytes(), report.LoweringStats(), native.report.StackBytes)
			}
			listing, err := unit.Listing()
			if err != nil {
				t.Fatal(err)
			}
			compatibility, err := lowerListing(p, spec, f)
			if err != nil || !harden && compatibility != listing {
				t.Fatalf("listing mismatch: %v", err)
			}
			template, err := unit.Template()
			if runtime.GOOS == "linux" && runtime.GOARCH == "loong64" {
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(template.Report().Snapshot(), report.Snapshot()) {
					t.Fatal("Template report changed retry plan")
				}
				repeated, err := unit.Template()
				if err != nil || repeated != template {
					t.Fatalf("repeated Template=%p,%v", repeated, err)
				}
			} else if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("cross Template=%v", err)
			}
			if !reflect.DeepEqual(unit.Report().Snapshot(), report.Snapshot()) {
				t.Fatal("Template mutated Unit report")
			}
		}
	}
	if retried == 0 {
		t.Fatal("no fixture exceeds the managed-frame limit natively")
	}
}

// 386-1720001128 fits a 128-byte frame only through the byte plan under
// either allocator; each allocator also retries one managed fixture.
func TestRound12NarrowMemoryRetryStatistics(t *testing.T) {
	// The fixtures' frames come from their Var homes.
	withoutVarPromotion(t)
	managed := 0
	for _, name := range append([]string{"386-1720001128.json"}, round12ManagedFixtures...) {
		p := round12RetryFixture(t, name)
		spec := TargetSpec{Architecture: "386"}
		f := Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 128}}
		if strings.HasPrefix(name, "loong64-") {
			spec.Architecture = "loong64"
			f.Context = ContextGoManaged
			f.AssumePointerFree = true
			f.Stack.BytesAvailable = 8192
		}
		unit, err := lowerUnit(p, spec, f)
		if err != nil {
			t.Fatal(err)
		}
		stats := unit.Report().LoweringStats()
		if spec.Architecture == "loong64" && stats.NarrowMemoryRetries == 0 {
			continue // this allocator fits the native plan
		}
		if stats.NarrowMemoryRetries != 1 {
			t.Fatalf("%s: %+v", name, stats)
		}
		if spec.Architecture == "loong64" {
			managed++
		}
		source, _, err := legalizeVectorsWithMemoryFallback(p, mustTarget(t, spec).options(f), true)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := lowerUnit(source, spec, f)
		if err != nil {
			t.Fatal(err)
		}
		expected := plain.Report().LoweringStats()
		if expected.NarrowMemoryRetries != 0 || stats.Retries != expected.Retries || stats.Allocations != expected.Allocations {
			t.Fatalf("narrow retry must be separate from speculative allocation retries: actual=%+v byte-plan=%+v", stats, expected)
		}
	}
	if managed == 0 {
		t.Fatal("no managed fixture takes the narrow retry")
	}
}
