package compile

import (
	"embed"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
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

// This is the native Store16 pressure fixture plus 35 simultaneously live
// 32-bit Vars. The ordinary managed plan lowers successfully at 656 bytes;
// only callable-template construction used to reject its 640-byte limit.
func TestRound12ManagedNarrowFrameRetry(t *testing.T) {
	// The fixture's frame comes from its Var homes.
	withoutVarPromotion(t)
	p := round12RetryFixture(t, "loong64-managed-frame.json")
	for _, harden := range []bool{false, true} {
		spec := TargetSpec{Architecture: "loong64"}
		f := Func{ABI: abi.C, Context: ContextGoManaged, AssumePointerFree: true, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, Harden: harden}
		o := mustTarget(t, spec).options(f)
		input, err := inputForLowering(p, true, o)
		if err != nil {
			t.Fatal(err)
		}
		native, err := lowerUnitInputOnce(input, o, emitLayoutText, boundaryManaged)
		if err != nil {
			t.Fatal(err)
		}
		if native.report.StackBytes != 656 {
			t.Fatalf("native frame=%d want656", native.report.StackBytes)
		}
		if _, err := native.Template(); !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("native Template=%v, want frame limit", err)
		}
		unit, err := lowerUnit(p, spec, f)
		if err != nil {
			t.Fatal(err)
		}
		report := unit.Report()
		if report.StackBytes() != 640 {
			t.Fatalf("Harden=%v retry frame=%d want640", harden, report.StackBytes())
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

func TestRound12NarrowMemoryRetryStatistics(t *testing.T) {
	// The fixtures' frames come from their Var homes.
	withoutVarPromotion(t)
	for _, name := range []string{"386-1720000736.json", "loong64-managed-frame.json"} {
		p := round12RetryFixture(t, name)
		spec := TargetSpec{Architecture: "386"}
		f := Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 128}}
		if name == "loong64-managed-frame.json" {
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
		if stats.NarrowMemoryRetries != 1 {
			t.Fatalf("%s: %+v", name, stats)
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
}
