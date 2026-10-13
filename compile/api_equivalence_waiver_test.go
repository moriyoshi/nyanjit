package compile_test

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// The golden replay waives a difference only for its stated purpose: each
// controlled change of one unrelated observation must still be reported.
func TestGoldenWaiversArePinned(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.VStore(b.ParamNative(), b.ConstVector(ir.Vec(4, 32), make([]byte, 16)))
	b.Return(b.ConstBits(32, 7))
	sealed, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	vector := sealed.Clone()
	product := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 32}, {Op: ir.OpParam, Bits: 32}, {Op: ir.OpMulHigh, Bits: 32, A: 0, B: 1}}, Arity: 2, Result: 2}

	dir := t.TempDir()
	write := func(name string, value any) string {
		t.Helper()
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		z := gzip.NewWriter(f)
		if err := json.NewEncoder(z).Encode(value); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// replay records r with change applied and returns the differing count.
	replay := func(name string, p *ir.Program, r apiGolden, change func(*apiGolden)) goldenSummary {
		t.Helper()
		r.Fixture = name + ".json.gz"
		write(r.Fixture, struct{ Program *ir.Program }{p})
		out, err := replayAPIEntry(r, p, false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r.Out = out
		change(&r)
		summary, err := replayGolden(dir, write(name+"-golden.gz", r))
		if err != nil {
			t.Fatal(err)
		}
		return summary
	}
	record := func(arch, entry string) apiGolden {
		return apiGolden{Variant: "controlled", Entry: entry,
			Options: recordedOptions{Architecture: arch, ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 4096}}
	}
	type control struct {
		arch, entry, key string
		p                *ir.Program
	}
	var controls []control
	for _, arch := range []string{"amd64", "386"} {
		for _, c := range [][2]string{{"vectors", "features"}, {"scopes", "scopes"}, {"emittemplate", "relocations"}, {"vectors", "error"}, {"assembly", "listing"}, {"lower", "report"}} {
			controls = append(controls, control{arch, c[0], c[1], vector})
		}
	}
	for _, c := range [][2]string{{"vectors", "features"}, {"scopes", "scopes"}, {"emittemplate", "relocations"}, {"vectors", "error"}, {"assembly", "listing"}, {"lower", "report"}} {
		controls = append(controls, control{"ppc64le", c[0], c[1], product})
	}
	for _, c := range controls {
		name := c.arch + "-" + c.entry + "-" + c.key
		if c.entry == "emittemplate" && c.arch != runtime.GOARCH {
			continue // native templates need the host architecture
		}
		if s := replay(name+"-same", c.p, record(c.arch, c.entry), func(*apiGolden) {}); s.differing != 0 {
			t.Errorf("%s: unchanged record differs: %v", name, s.mismatches)
		}
		s := replay(name, c.p, record(c.arch, c.entry), func(r *apiGolden) {
			if c.key == "error" {
				r.Err, r.Is, r.Out = "recorded fixture expected unsupported", []string{"Unsupported"}, nil
				return
			}
			if _, ok := r.Out[c.key]; !ok {
				t.Fatalf("%s: no %s output", name, c.key)
			}
			r.Out[c.key] = "controlled-unexpected-golden-value"
		})
		if s.differing != 1 {
			t.Errorf("%s: controlled change was waived (intended %v, reviewed %v)", name, s.intended, s.reviewed)
		}
	}

	// A reviewed change applies to its exact record and hashes only.
	r := record("386", "assembly")
	name := "reviewed"
	out, err := replayAPIEntry(r, vector, false)
	if err != nil {
		t.Fatal(err)
	}
	key := reviewedOutput{name + ".json.gz", r.Variant, r.Entry, "listing"}
	reviewedChanges[key] = reviewedChange{"controlled", "before", out["listing"]}
	defer delete(reviewedChanges, key)
	if s := replay(name, vector, r, func(r *apiGolden) { r.Out["listing"] = "before" }); s.differing != 0 || s.reviewed["controlled"] != 1 {
		t.Errorf("exact reviewed change not applied: %+v", s)
	}
	if s := replay(name, vector, r, func(r *apiGolden) { r.Out["listing"] = "other" }); s.differing != 1 {
		t.Errorf("reviewed change waived another before-hash: %+v", s)
	}
	reviewedChanges[key] = reviewedChange{"controlled", "before", "unexpected-after"}
	if s := replay(name, vector, r, func(r *apiGolden) { r.Out["listing"] = "before" }); s.differing != 1 {
		t.Errorf("reviewed change waived another after-hash: %+v", s)
	}

	// An amd64 record from before dead-flag elision is accepted only when
	// lowering without elision reproduces it.
	add := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 64}, {Op: ir.OpConst, Bits: 64, Imm: 7}, {Op: ir.OpAdd, Bits: 64, A: 0, B: 1}}, Arity: 1, Result: 2}
	for _, entry := range []string{"assembly", "lower"} {
		r := record("amd64", entry)
		before, err := replayAPIEntry(r, add, true)
		if err != nil {
			t.Fatal(err)
		}
		after, err := replayAPIEntry(r, add, false)
		if err != nil {
			t.Fatal(err)
		}
		k := map[string]string{"assembly": "listing", "lower": "report"}[entry]
		if before[k] == after[k] {
			t.Fatalf("%s: elision did not change the %s", entry, k)
		}
		name := "dead-flags-" + entry
		if s := replay(name, add, r, func(r *apiGolden) { r.Out = before }); s.differing != 0 || s.reviewed[deadFlagLEA] != 1 {
			t.Errorf("%s: record without elision not accepted: %+v", entry, s)
		}
		if s := replay(name, add, r, func(r *apiGolden) { r.Out[k] = "controlled-unexpected-golden-value" }); s.differing != 1 {
			t.Errorf("%s: dead-flag rule waived another change: %+v", entry, s)
		}
	}
}
