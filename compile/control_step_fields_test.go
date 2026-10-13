package compile_test

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Each edge carries one parameter, so both valid and malformed argument lists
// exercise lowering rather than only empty, non-parameterized blocks.
func controlFieldProgram() *ir.Program {
	return &ir.Program{
		Result: -1,
		Instructions: []ir.Instruction{
			{Op: ir.OpConst, Bits: 64, Imm: 7},
			{Op: ir.OpLabelParam, Bits: 64},
			{Op: ir.OpLabelParam, Bits: 64},
			{Op: ir.OpLabelParam, Bits: 64},
		},
		Control: &ir.Control{
			Labels: []bool{true, true, true, true}, LabelScopes: make([]int, 4),
			Params: []int{0, 1, 1, 1}, ParamBits: [][]uint8{nil, {64}, {64}, {64}},
			Steps: []ir.ControlStep{
				{IsLabel: true, Label: 0},
				{Term: ir.TermJump, Start: 0, End: 1, To: 1, Other: -1, Result: -1, Args: []int{0}},
				{IsLabel: true, Label: 1},
				{Term: ir.TermBranch, Start: 1, End: 2, To: 2, Other: 3, Result: 1, Values: []int{1}, Args: []int{1}, OtherArgs: []int{1}, Predicate: &ir.PredicateExpr{Kind: ir.PredicateCompare, Value: 1}},
				{IsLabel: true, Label: 2},
				{Term: ir.TermReturn, Start: 2, End: 3, To: -1, Other: -1, Result: 2},
				{IsLabel: true, Label: 3},
				{Term: ir.TermReturn, Start: 3, End: 4, Result: 3},
			},
		},
	}
}

func TestControlStepUnusedFieldsRejected(t *testing.T) {
	base := controlFieldProgram()
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{0, 1, 5} {
		for _, field := range []string{"To", "Other", "Args", "OtherArgs", "Predicate", "Values", "ResultParts"} {
			// The jump's first edge is used and tested in the mutation matrix.
			if index == 1 && (field == "To" || field == "Args") {
				continue
			}
			t.Run(fmt.Sprintf("step%d/%s", index, field), func(t *testing.T) {
				p := base.Clone()
				s := &p.Control.Steps[index]
				switch field {
				case "To":
					s.To = 2
				case "Other":
					s.Other = 2
				case "Args":
					s.Args = []int{s.Start}
				case "OtherArgs":
					s.OtherArgs = []int{s.Start}
				case "Predicate":
					s.Predicate = &ir.PredicateExpr{Kind: ir.PredicateCompare, Value: s.Start}
				case "Values":
					s.Values = []int{s.Start}
				case "ResultParts":
					if index == 5 {
						return // Return uses ResultParts; separate validation covers it.
					}
					s.ResultParts = []int{s.Start}
				}
				if err := p.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
					t.Fatalf("unused %s accepted: %v", field, err)
				}
				controlFieldEntryPoints(t, p, false)
			})
		}
	}
}

func TestControlLabelSegmentMetadataRejected(t *testing.T) {
	for field := 0; field < 7; field++ {
		p := controlFieldProgram()
		s := &p.Control.Steps[0]
		switch field {
		case 0:
			s.Term = ir.TermReturn
		case 1:
			s.Start = 1
		case 2:
			s.End = 1
		case 3:
			s.LeaveStart = 1
		case 4:
			s.LeaveEnd = 1
		case 5:
			s.Arity = 1
		case 6:
			s.Term = ir.ControlTerm(255)
		}
		if err := p.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
			t.Fatalf("label segment field %d accepted: %v", field, err)
		}
		controlFieldEntryPoints(t, p, false)
	}
}

func TestControlStepUnusedPredicateRejectedBeforeWork(t *testing.T) {
	for _, index := range []int{0, 1, 5} {
		p := controlFieldProgram()
		predicate := &ir.PredicateExpr{Kind: ir.PredicateCompare}
		for i := 0; i < 20; i++ {
			predicate = &ir.PredicateExpr{Kind: ir.PredicateAnd, Left: predicate, Right: predicate}
		}
		p.Control.Steps[index].Predicate = predicate
		if err := p.Validate(); !errors.Is(err, ir.ErrInvalidProgram) || errors.Is(err, ir.ErrPredicateLimit) {
			t.Fatalf("step %d: unused predicate traversed before field rejection: %v", index, err)
		}
	}
}

// Cover every edge field on labels and all terminator kinds with valid,
// missing, negative, out-of-range, wrong-count, cyclic, and malformed operands.
func TestControlStepEdgeMutationMatrix(t *testing.T) {
	indices := []int{0, 1, 3, 5}
	cases, valid := 0, 0
	for _, index := range indices {
		for field := 0; field < 7; field++ {
			for variant := 0; variant < 7; variant++ {
				p := controlFieldProgram()
				s := &p.Control.Steps[index]
				mutateControlEdge(s, field, variant)
				err := p.Validate()
				if err == nil {
					valid++
				} else if !errors.Is(err, ir.ErrInvalidProgram) {
					t.Fatalf("step=%d field=%d variant=%d: validation %v", index, field, variant, err)
				}
				t.Run(fmt.Sprintf("step%d/field%d/variant%d", index, field, variant), func(t *testing.T) {
					controlFieldEntryPoints(t, p, err == nil)
				})
				cases++
			}
		}
	}
	// Combinations catch ordering bugs hidden by one-field-at-a-time mutation.
	r := rand.New(rand.NewSource(110011))
	for i := 0; i < 512; i++ {
		p := controlFieldProgram()
		for j := 0; j < 1+r.Intn(4); j++ {
			s := &p.Control.Steps[indices[r.Intn(len(indices))]]
			mutateControlEdge(s, r.Intn(7), r.Intn(7))
		}
		err := p.Validate()
		if err == nil {
			valid++
		} else if !errors.Is(err, ir.ErrInvalidProgram) {
			t.Fatalf("combination %d: validation %v", i, err)
		}
		controlFieldEntryPoints(t, p, err == nil)
		cases++
	}
	t.Logf("%d mutations: %d valid, %d rejected; all entry points agree", cases, valid, cases-valid)
}

func mutateControlEdge(s *ir.ControlStep, field, variant int) {
	destination := []int{-2, -1, 0, 1, 2, 4, int(^uint(0) >> 1)}[variant]
	arguments := [][]int{nil, {}, {s.Start}, {-1}, {s.End}, {s.Start, s.Start}, {int(^uint(0) >> 1)}}[variant]
	switch field {
	case 0:
		s.To = destination
	case 1:
		s.Other = destination
	case 2:
		s.Args = arguments
	case 3:
		s.OtherArgs = arguments
	case 4:
		s.Values = arguments
	case 5:
		s.ResultParts = arguments
	case 6:
		n := &ir.PredicateExpr{Kind: ir.PredicateCompare, Value: s.Start}
		switch variant {
		case 0:
			n = nil
		case 2:
			n.Value = -1
		case 3:
			n.Value = s.End
		case 4:
			n.Kind, n.Left = ir.PredicateNot, n
		case 5:
			n.Left = &ir.PredicateExpr{Kind: ir.PredicateCompare}
		case 6:
			n.Kind = 255
		}
		s.Predicate = n
	}
}

func controlFieldEntryPoints(t *testing.T, p *ir.Program, valid bool, harden ...bool) {
	t.Helper()
	_, err := ir.Seal(p)
	if (err == nil) != valid {
		t.Fatalf("Seal = %v, valid=%v", err, valid)
	}
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		spec := compile.TargetSpec{Architecture: arch}
		fn := compile.Func{AssumePointerFree: true, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 16}}
		if len(harden) != 0 {
			fn.Harden = harden[0]
		}
		in := func(c compile.Context) compile.Func {
			f := fn
			f.Context = c
			return f
		}
		template := func(c compile.Context) func() error {
			return func() error { _, err := lowerTemplate(p, spec, in(c)); return err }
		}
		entries := []struct {
			name string
			call func() error
		}{
			{"Listing", func() error { _, err := lowerListing(p, spec, fn); return err }},
			{"ListingSymbolic", func() error { _, err := lowerListing(p, spec, in(compile.ContextGoManaged)); return err }},
			{"Report", func() error { _, err := lowerReport(p, spec, fn); return err }},
			{"RegisterScopes", func() error {
				u, err := lowerUnit(p, spec, fn)
				if err == nil {
					_, err = u.RegisterScopes()
				}
				return err
			}},
			{"Template", template(compile.ContextNative)},
			{"TemplateGoManaged", template(compile.ContextGoManaged)},
			{"TemplateForeign", template(compile.ContextForeign)},
			{"TemplateExternal", template(compile.ContextExternal)},
		}
		if !valid {
			compileIn := func(c compile.Context) func() error {
				return func() error {
					code, err := nyanjit.Compile(p, spec, in(c))
					if code != nil {
						_ = code.Close()
					}
					return err
				}
			}
			entries = append(entries, []struct {
				name string
				call func() error
			}{
				{"Bytes", func() error {
					t, err := lowerTemplate(p, spec, in(compile.ContextExternal))
					if err == nil {
						_, err = t.Bytes(nil)
					}
					return err
				}},
				{"Compile", compileIn(compile.ContextNative)},
				{"CompileGoManaged", compileIn(compile.ContextGoManaged)},
				{"CompileForeign", compileIn(compile.ContextForeign)},
			}...)
		}
		for _, entry := range entries {
			err := entry.call()
			nativeOnly := entry.name != "Listing" && entry.name != "ListingSymbolic" && entry.name != "Report" && entry.name != "RegisterScopes"
			if valid && err != nil && !(nativeOnly && arch != runtime.GOARCH && errors.Is(err, compile.ErrUnsupported)) {
				t.Fatalf("%s/%s: accepted then invalid: %v", arch, entry.name, err)
			}
			if !valid && !errors.Is(err, compile.ErrInvalidFunction) && !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatalf("%s/%s: malformed accepted or wrong error: %v", arch, entry.name, err)
			}
		}
	}
}

func TestControlStepIgnoredDefaultsPreserveListings(t *testing.T) {
	base := controlFieldProgram()
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		spec := compile.TargetSpec{Architecture: arch}
		fn := compile.Func{AssumePointerFree: true, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 16}}
		want, err := lowerListing(base, spec, fn)
		if err != nil {
			t.Fatal(err)
		}
		p := base.Clone()
		for i := range p.Control.Steps {
			s := &p.Control.Steps[i]
			if s.IsLabel || s.Term == ir.TermReturn {
				s.To, s.Other = 0, 0
			}
			if s.IsLabel || s.Term == ir.TermJump {
				s.Result = int(^uint(0) >> 1)
			}
			if !s.IsLabel {
				s.Label = int(^uint(0) >> 1)
			}
		}
		got, err := lowerListing(p, spec, fn)
		if err != nil || got != want {
			t.Fatalf("%s: ignored default/result fields alter listing: %v", arch, err)
		}
	}
}
