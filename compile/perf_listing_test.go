package compile_test

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestPerformanceListingCorpus replays frozen programs/options captured before a
// compiler change. Keeping inputs fixed also keeps embedded function addresses
// fixed across test binaries. See scripts/perf-listings.py for capture.
func TestPerformanceListingCorpus(t *testing.T) {
	directory := os.Getenv("NYANJIT_LISTING_CORPUS")
	if directory == "" {
		t.Skip("set NYANJIT_LISTING_CORPUS to replay a captured corpus")
	}
	files, err := filepath.Glob(filepath.Join(directory, "*.json.gz"))
	if err != nil || len(files) == 0 {
		t.Fatalf("listing corpus: %d files, %v", len(files), err)
	}
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			t.Fatal(err)
		}
		var fixture struct {
			Program *ir.Program
			Options listingFixtureOptions
			Listing string
			SHA256  string
		}
		err = json.NewDecoder(gz).Decode(&fixture)
		gz.Close()
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		spec, fn := fixture.Options.split()
		listing, err := lowerListing(fixture.Program, spec, fn)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		digest := sha256.Sum256([]byte(listing))
		if hex.EncodeToString(digest[:]) != fixture.SHA256 {
			before := name + ".before.s"
			after := name + ".after.s"
			if err := os.WriteFile(before, []byte(fixture.Listing), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(after, []byte(listing), 0600); err != nil {
				t.Fatal(err)
			}
			t.Errorf("listing changed: diff -u %s %s", before, after)
		}
	}
	t.Logf("replayed %d listing fixtures", len(files))
}

// listingFixtureOptions decodes the flat options object stored by
// scripts/perf-listings.py.
type listingFixtureOptions struct {
	Architecture        string
	CPU                 abi.CPUProfile
	AMD64FMA            bool
	Features            []string
	ABI                 abi.Flavor
	PPC64CPU            uint8
	ManagedCalls        bool
	Stack               abi.StackPractice
	StackPointer        abi.StackPointerRole
	StackBytesAvailable int
	AssumePointerFree   bool
	PreferCompactLoops  bool
	Harden              bool
}

// split returns the target and function settings the fixture was captured
// with. Listings did no callable checks, so non-managed fixtures are external.
func (o listingFixtureOptions) split() (compile.TargetSpec, compile.Func) {
	spec := compile.TargetSpec{Architecture: o.Architecture, CPU: o.CPU}
	if o.AMD64FMA {
		spec.CPU.Extensions = append(slices.Clone(spec.CPU.Extensions), "fma")
	}
	if o.PPC64CPU != 0 && spec.CPU.Level == "" {
		spec.CPU.Level = fmt.Sprintf("power%d", o.PPC64CPU)
	}
	switch {
	case o.Features == nil:
	case len(o.Features) == 0:
		spec.Vectors = compile.VectorsOff()
	default:
		spec.Vectors = compile.VectorsRequire(o.Features...)
	}
	f := compile.Func{
		ABI:                o.ABI,
		Context:            compile.ContextExternal,
		Stack:              compile.StackPolicy{Practice: o.Stack, Pointer: o.StackPointer, BytesAvailable: o.StackBytesAvailable},
		AssumePointerFree:  o.AssumePointerFree,
		PreferCompactLoops: o.PreferCompactLoops,
		Harden:             o.Harden,
	}
	if o.ManagedCalls {
		f.Context = compile.ContextGoManaged
		// The captured listings predate Lower's pointer-free check, and the
		// assertion only gates frame registration; it does not change code.
		f.AssumePointerFree = true
	}
	return spec, f
}
