package compile_test

import (
	"bytes"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

func scratchPrograms() []*ir.SealedProgram {
	return []*ir.SealedProgram{workload.Tiny(0), workload.Small(0), workload.Chain(53), workload.Scopes(17), workload.Loops(19), workload.FP(13)}
}
func TestScratchConcurrentCompiles(t *testing.T) {
	if runtime.GOOS != "linux" || !compile.HostBackend() {
		t.Skip("no native backend")
	}
	programs := scratchPrograms()
	type output struct {
		listing string
		report  abi.EmissionReport
		code    []byte
	}
	expected := make([]output, len(programs))
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
	for i, p := range programs {
		u, err := lowerUnit(p, compile.TargetSpec{}, f)
		if err != nil {
			t.Fatal(err)
		}
		listing, err := u.Listing()
		if err != nil {
			t.Fatal(err)
		}
		e, err := compile.EncodedBytes(u)
		if err != nil {
			t.Fatal(err)
		}
		expected[i] = output{listing, u.Report().Snapshot(), bytes.Clone(e)}
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 12; n++ {
				i := (worker + n) % len(programs)
				p := programs[i]
				want := expected[i]
				u, err := lowerUnit(p, compile.TargetSpec{}, f)
				if err != nil {
					t.Error(err)
					return
				}
				listing, err := u.Listing()
				if err != nil || listing != want.listing || !reflect.DeepEqual(u.Report().Snapshot(), want.report) {
					t.Errorf("concurrent Lower/Listing/Report %d: %v", i, err)
					return
				}
				e, err := compile.EncodedBytes(u)
				if err != nil || !bytes.Equal(e, want.code) {
					t.Errorf("concurrent encoding %d: %v", i, err)
					return
				}
				template, err := u.Template()
				if err != nil {
					t.Error(err)
					return
				}
				prepared, err := lowerTemplate(p, compile.TargetSpec{}, f)
				if err != nil || !reflect.DeepEqual(template.Report().Snapshot(), prepared.Report().Snapshot()) {
					t.Errorf("concurrent Prepare %d: %v", i, err)
					return
				}
				for _, v := range []*execution.Template{template, prepared} {
					code, err := v.Bind(nil)
					if err != nil {
						t.Error(err)
						return
					}
					if err = code.Close(); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
func TestScratchReuseAndGCLeaveLazyOutputsOwned(t *testing.T) {
	if runtime.GOOS != "linux" || !compile.HostBackend() {
		t.Skip("no native backend")
	}
	programs := scratchPrograms()
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
	for _, a := range programs {
		before, err := lowerUnit(a, compile.TargetSpec{}, f)
		if err != nil {
			t.Fatal(err)
		}
		listing, err := before.Listing()
		if err != nil {
			t.Fatal(err)
		}
		report := before.Report()
		encoded, err := compile.EncodedBytes(before)
		if err != nil {
			t.Fatal(err)
		}
		saved := bytes.Clone(encoded)
		template, err := before.Template()
		if err != nil {
			t.Fatal(err)
		}
		templateReport := template.Report()
		relocations := template.Relocations()
		lazy, err := lowerUnit(a, compile.TargetSpec{}, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range programs {
			if _, err := lowerUnit(b, compile.TargetSpec{}, f); err != nil {
				t.Fatal(err)
			}
		}
		runtime.GC()
		again, err := lowerUnit(a, compile.TargetSpec{}, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range []*compile.Unit{before, lazy, again} {
			got, err := u.Listing()
			if err != nil || got != listing || !reflect.DeepEqual(u.Report().Snapshot(), report.Snapshot()) {
				t.Fatalf("Unit changed after scratch reuse: %v", err)
			}
			e, err := compile.EncodedBytes(u)
			if err != nil || !bytes.Equal(e, saved) {
				t.Fatalf("lazy bytes changed: %v", err)
			}
			gotTemplate, err := u.Template()
			if err != nil || !reflect.DeepEqual(gotTemplate.Report().Snapshot(), templateReport.Snapshot()) || !reflect.DeepEqual(gotTemplate.Relocations(), relocations) {
				t.Fatalf("template changed after scratch reuse: %v", err)
			}
			code, err := gotTemplate.Bind(nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := code.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
