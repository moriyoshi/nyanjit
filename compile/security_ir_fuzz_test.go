package compile_test

import (
	"embed"
	"encoding/json"
	"errors"
	"math/rand"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

var securityInteresting = []int{-1, 0, 1, 2, 3, 1 << 20}

func securityPickIndex(r *rand.Rand, n int) int {
	switch r.Intn(4) {
	case 0:
		return securityInteresting[r.Intn(len(securityInteresting))]
	case 1:
		return n - 1 + r.Intn(3)
	default:
		if n <= 0 {
			return 0
		}
		return r.Intn(n)
	}
}

var securityBitsChoices = []uint8{0, 1, 7, 8, 16, 32, 64, 65, 128, 255}
var securityImmChoices = []uint64{0, 1, 2, 7, 8, 15, 16, 31, 32, 63, 64, 65, 127, 128, 1 << 32, ^uint64(0), 1 << 63}

func securityRandType(r *rand.Rand) ir.Type {
	switch r.Intn(5) {
	case 0:
		return ir.Type{}
	case 1:
		return ir.Scalar(securityBitsChoices[r.Intn(len(securityBitsChoices))])
	case 2:
		return ir.Vec([]uint16{1, 2, 4, 8, 16, 3, 0, 65535}[r.Intn(8)], securityBitsChoices[r.Intn(len(securityBitsChoices))])
	case 3:
		return ir.Mask([]uint16{1, 2, 4, 8, 16, 3, 64, 65}[r.Intn(8)])
	default:
		return ir.Type{Kind: abi.ValueKind(r.Intn(5)), Lanes: uint16(r.Intn(70)), LaneBits: securityBitsChoices[r.Intn(len(securityBitsChoices))]}
	}
}

func securityMutateInstr(r *rand.Rand, p *ir.Program) {
	n := len(p.Instructions)
	if n == 0 {
		return
	}
	i := &p.Instructions[r.Intn(n)]
	switch r.Intn(14) {
	case 0:
		i.Op = ir.Opcode(r.Intn(80))
	case 1:
		i.Bits = securityBitsChoices[r.Intn(len(securityBitsChoices))]
	case 2:
		i.A = securityPickIndex(r, n)
	case 3:
		i.B = securityPickIndex(r, n)
	case 4:
		i.C = securityPickIndex(r, n)
	case 5:
		i.Imm = securityImmChoices[r.Intn(len(securityImmChoices))]
	case 6:
		i.Type = securityRandType(r)
	case 7:
		if len(i.Args) > 0 && r.Intn(2) == 0 {
			i.Args[r.Intn(len(i.Args))] = securityPickIndex(r, n)
		} else if r.Intn(2) == 0 {
			i.Args = append(i.Args, securityPickIndex(r, n))
		} else if len(i.Args) > 0 {
			i.Args = i.Args[:len(i.Args)-1]
		}
	case 8:
		i.Variable = securityPickIndex(r, len(p.Variables))
	case 9:
		i.ByteOrder = ir.ByteOrder(r.Intn(4))
	case 10:
		i.Aligned = !i.Aligned
	case 11:
		i.Contract = !i.Contract
	case 12:
		i.Data = make([]byte, []int{0, 1, 8, 16, 32, 64}[r.Intn(6)])
	case 13:
		// copy another instruction's shape
		*i = p.Instructions[r.Intn(n)].Clone()
	}
}

func securityMutateVar(r *rand.Rand, p *ir.Program) {
	if len(p.Variables) == 0 {
		return
	}
	v := &p.Variables[r.Intn(len(p.Variables))]
	n := len(p.Instructions)
	switch r.Intn(7) {
	case 0:
		v.Type = securityRandType(r)
	case 1:
		v.Scope = securityPickIndex(r, len(p.ScopeExits)+1)
	case 2:
		v.Region = securityPickIndex(r, len(p.ScopeExits)+1)
	case 3:
		v.Declaration = securityPickIndex(r, n)
	case 4:
		if len(v.Initial) > 0 {
			v.Initial[r.Intn(len(v.Initial))] = securityPickIndex(r, n)
		}
	case 5:
		v.Immutable = !v.Immutable
	case 6:
		v.Initial = append(v.Initial, securityPickIndex(r, n))
	}
}

func securityMutateScope(r *rand.Rand, p *ir.Program) {
	if len(p.ScopeExits) == 0 {
		return
	}
	s := &p.ScopeExits[r.Intn(len(p.ScopeExits))]
	n := len(p.Instructions)
	switch r.Intn(6) {
	case 0:
		s.ID = securityPickIndex(r, len(p.ScopeExits)+1)
	case 1:
		s.Parent = securityPickIndex(r, len(p.ScopeExits)+1)
	case 2:
		s.Enter = securityPickIndex(r, n)
	case 3:
		s.Position = securityPickIndex(r, n)
	case 4:
		s.Hidden = append(s.Hidden, securityPickIndex(r, n))
	case 5:
		s.Exports = append(s.Exports, securityPickIndex(r, n))
	}
}

func securityMutatePred(r *rand.Rand, e *ir.PredicateExpr, n int) {
	for e != nil && r.Intn(3) != 0 {
		if e.Left != nil && r.Intn(2) == 0 {
			e = e.Left
		} else if e.Right != nil {
			e = e.Right
		} else {
			break
		}
	}
	if e == nil {
		return
	}
	switch r.Intn(8) {
	case 0:
		e.Kind = ir.PredicateKind(r.Intn(5))
	case 1:
		e.Condition = ir.BranchCondition(r.Intn(12))
	case 2:
		e.Value = securityPickIndex(r, n)
	case 3:
		e.Other = securityPickIndex(r, n)
		e.WithValue = r.Intn(2) == 0
	case 4:
		e.Anchor = securityPickIndex(r, n)
		e.HasAnchor = r.Intn(2) == 0
	case 5:
		e.Immediate = int64(securityImmChoices[r.Intn(len(securityImmChoices))])
	case 6:
		e.Left = nil
	case 7:
		e.Right = &ir.PredicateExpr{Kind: ir.PredicateCompare, Value: securityPickIndex(r, n)}
	}
}

func securityMutateControl(r *rand.Rand, p *ir.Program) {
	c := p.Control
	if c == nil {
		return
	}
	n := len(p.Instructions)
	nl := len(c.Labels)
	s := &c.Steps[r.Intn(len(c.Steps))]
	switch r.Intn(18) {
	case 0:
		s.Label = securityPickIndex(r, nl)
	case 1:
		s.To = securityPickIndex(r, nl)
	case 2:
		s.Other = securityPickIndex(r, nl)
	case 3:
		s.Term = ir.ControlTerm(r.Intn(4))
	case 4:
		s.Start = securityPickIndex(r, n)
	case 5:
		s.End = securityPickIndex(r, n)
	case 6:
		s.Result = securityPickIndex(r, n)
	case 7:
		s.Arity = securityPickIndex(r, 3)
	case 8:
		s.Values = append(s.Values, securityPickIndex(r, n))
	case 9:
		if len(s.Args) > 0 {
			s.Args[r.Intn(len(s.Args))] = securityPickIndex(r, n)
		} else {
			s.Args = append(s.Args, securityPickIndex(r, n))
		}
	case 10:
		if len(s.OtherArgs) > 0 {
			s.OtherArgs[r.Intn(len(s.OtherArgs))] = securityPickIndex(r, n)
		} else {
			s.OtherArgs = append(s.OtherArgs, securityPickIndex(r, n))
		}
	case 11:
		securityMutatePred(r, s.Predicate, n)
	case 12:
		if nl > 0 {
			c.Params[r.Intn(nl)] = securityPickIndex(r, 3)
		}
	case 13:
		if nl > 0 {
			l := r.Intn(nl)
			c.ParamBits[l] = append(c.ParamBits[l], securityBitsChoices[r.Intn(len(securityBitsChoices))])
		}
	case 14:
		if nl > 0 {
			c.LabelScopes[r.Intn(nl)] = securityPickIndex(r, len(p.ScopeExits)+1)
		}
	case 15:
		if r.Intn(2) == 0 {
			c.ParamTypes = nil
		} else {
			c.ParamTypes = make([][]ir.Type, nl)
			for l := range c.ParamTypes {
				for k := 0; k < min(max(c.Params[l], 0), 16); k++ {
					c.ParamTypes[l] = append(c.ParamTypes[l], ir.Scalar(64))
				}
			}
		}
	case 16:
		s.LeaveStart = securityPickIndex(r, len(p.ScopeExits))
	case 17:
		s.LeaveEnd = securityPickIndex(r, len(p.ScopeExits))
	}
}

func securityMutate(r *rand.Rand, p *ir.Program) {
	switch k := r.Intn(20); {
	case k < 10:
		securityMutateInstr(r, p)
	case k < 12:
		securityMutateVar(r, p)
	case k < 14:
		securityMutateScope(r, p)
	case k < 18:
		securityMutateControl(r, p)
	case k == 18:
		switch r.Intn(4) {
		case 0:
			p.Result = securityPickIndex(r, len(p.Instructions))
		case 1:
			p.Arity = securityPickIndex(r, 3)
		case 2:
			p.ResultParts = []int{p.Result, securityPickIndex(r, len(p.Instructions))}
		case 3:
			p.ResultParts = nil
		}
	default:
		// structural: duplicate or remove the last instruction
		if r.Intn(2) == 0 && len(p.Instructions) > 1 {
			p.Instructions = p.Instructions[:len(p.Instructions)-1]
		} else {
			p.Instructions = append(p.Instructions, p.Instructions[r.Intn(len(p.Instructions))].Clone())
		}
	}
}

//go:embed testdata/security-scopeleave-args-*.json
var securityScopeArgsCases embed.FS

func securityIRSeeds() []*ir.Program {
	seeds := []*ir.Program{workload.Tiny(0).Clone(), workload.Scopes(4).Clone(), workload.Loops(8).Clone(), workload.Vectors(4).Clone(), workload.DeepScopes(4).Clone()}
	a := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpConst}}, Variables: []ir.Variable{{Type: ir.Scalar(64), Region: 1, Declaration: 100, Initial: []int{50}, Immutable: true}}, ScopeExits: []ir.ScopeExit{{ID: 1, Enter: 100, Position: 200}}, Result: 0}
	b := seeds[1].Clone()
	for i := range b.Instructions {
		if b.Instructions[i].Op == ir.OpScopeLeave {
			b.Instructions[i].Args = []int{1000}
			break
		}
	}
	c := workload.NestedLoops(3).Clone()
	for i := range c.Control.Steps {
		s := &c.Control.Steps[i]
		if !s.IsLabel && s.Term == ir.TermJump {
			s.OtherArgs = []int{s.Start}
			break
		}
	}
	d := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpLabelParam, EntryReg: 4}}, Result: 0}
	e := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 8, Imm: 0xffffffffffffff01}}, Result: 0}
	f := seeds[4].Clone()
	for i := range f.Instructions {
		if f.Instructions[i].Op == ir.OpScopeEnter {
			f.Instructions[i].Type = ir.Mask(4)
			break
		}
	}
	// A vector ResultParts operand passed Validate and panicked in Lower
	// (security audit 2026-10-25, F18).
	g := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpParam}, {Op: ir.OpVSplat, Type: ir.Vec(2, 64), Bits: 64, A: 0}}, Arity: 1, Result: 0, ResultParts: []int{0, 1}}
	seeds = append(seeds, a, b, c, d, e, f, g)
	for _, name := range []string{"testdata/security-scopeleave-args-1.json", "testdata/security-scopeleave-args-2.json"} {
		raw, err := securityScopeArgsCases.ReadFile(name)
		if err != nil {
			panic(err)
		}
		var p ir.Program
		if err := json.Unmarshal(raw, &p); err != nil {
			panic(err)
		}
		seeds = append(seeds, &p)
	}
	return seeds
}

// FuzzSecurityRawIR ports the audit's instruction/Var/scope/control mutators.
// It never executes generated bytes and bounds mutation sizes and counts.
func FuzzSecurityRawIR(f *testing.F) {
	seeds := securityIRSeeds()
	for i := range seeds {
		f.Add(uint8(i), uint64(0), uint8(0))
	}
	f.Fuzz(func(t *testing.T, shape uint8, seed uint64, mutations uint8) {
		index := int(shape) % len(seeds)
		p := seeds[index].Clone()
		r := rand.New(rand.NewSource(int64(seed)))
		for i := 0; i < int(mutations)%5; i++ {
			securityMutate(r, p)
		}
		valid := p.Validate() == nil
		// The second ScopeLeave audit case passes an entry parameter, whose
		// block dominates the leave; it is valid since cross-block values
		// follow dominance, and must compile.
		if index >= 5 && index != len(seeds)-1 && mutations%5 == 0 && valid {
			t.Fatal("audit seed accepted by Validate")
		}
		_, err := ir.Seal(p)
		if (err == nil) != valid {
			t.Fatalf("Validate/Seal disagree: %v", err)
		}
		for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
			spec := compile.TargetSpec{Architecture: arch}
			o := compile.Func{ABI: abi.C, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 20}}
			_, err := lowerListing(p, spec, o)
			invalid := errors.Is(err, ir.ErrInvalidProgram) || errors.Is(err, compile.ErrInvalidFunction)
			if valid && invalid {
				t.Fatalf("%s accepted then invalid: %v", arch, err)
			}
			if !valid && !invalid && !errors.Is(err, ir.ErrPredicateLimit) && !errors.Is(err, compile.ErrBackendLimit) {
				t.Fatalf("%s lost validation error: %v", arch, err)
			}
			u, e := lowerUnit(p, spec, o)
			if e == nil {
				_, e = u.Listing()
			}
			if valid && (errors.Is(e, ir.ErrInvalidProgram) || errors.Is(e, compile.ErrInvalidFunction)) {
				t.Fatalf("%s Lower invalid: %v", arch, e)
			}
		}
		o := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 20}}
		external := o
		external.Context = compile.ContextExternal
		for _, call := range []func() error{
			func() error { _, e := lowerReport(p, compile.TargetSpec{}, external); return e },
			func() error { _, e := lowerTemplate(p, compile.TargetSpec{}, o); return e },
			func() error { _, e := lowerTemplate(p, compile.TargetSpec{}, external); return e },
		} {
			err := call()
			if valid && (errors.Is(err, ir.ErrInvalidProgram) || errors.Is(err, compile.ErrInvalidFunction)) {
				t.Fatalf("native entry accepted then invalid: %v", err)
			}
		}
	})
}
