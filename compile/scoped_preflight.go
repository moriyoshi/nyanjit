package compile

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// FixedRegisterUse is a register required by an operation at an IR position.
// Value is the instruction ID supplying that operand.
type FixedRegisterUse struct {
	Position int
	Value    int
	Word     int
	Register string
}

// RegisterOpportunity is an optional placement that may enable a shorter
// target instruction. It is not a fixed register constraint.
type RegisterOpportunity struct {
	Position int
	Value    int
	Register string
	Kind     string
}

// ScopeRegisterUsage describes the register constraints visible before
// allocation. Nested operations are included in each enclosing scope.
// ClobberedRegisters are call effects, not a promise about final code.
type ScopeRegisterUsage struct {
	ID, Parent                int
	Enter, Leave              int
	FixedUses                 []FixedRegisterUse
	Opportunities             []RegisterOpportunity
	ClobberedRegisters        []string
	ClobberedFPRegisters      []string
	ClobberedSpecialRegisters []string
}

func amd64ZeroBranchValue(predicate *ir.PredicateExpr, ops []ir.Instruction) (int, bool) {
	if predicate == nil || predicate.Kind != ir.PredicateCompare || predicate.WithValue ||
		predicate.Immediate != 0 || predicate.Value < 0 || predicate.Value >= len(ops) ||
		(predicate.Condition != ir.BranchEqual && predicate.Condition != ir.BranchNotEqual) ||
		instructionBits(ops[predicate.Value]) != 64 {
		return 0, false
	}
	return predicate.Value, true
}

// registerScopes looks ahead through a finished Program's value
// scopes. The root is scope zero and covers the entire Program. It reports
// fixed call operands and call clobbers without emitting or allocating code.
// It describes source value IDs and native instruction constraints, before
// legalization and allocation. A later narrow-memory pressure retry may expand
// operations into byte accesses; this preflight does not predict that plan.
func registerScopes(input ir.Input, options compileOptions) ([]ScopeRegisterUsage, error) {
	program, _, sourceErr := validatedSource(input)
	if sourceErr != nil {
		return nil, sourceErr
	}
	if err := validateEmissionTarget(options, emitText); err != nil {
		return nil, err
	}
	architecture := targetArchitecture(options)
	if program.Arity > abi.MaxArgumentsFor(architecture) {
		return nil, entryArityError(program.Arity, architecture)
	}
	target := newMachineTarget(options)
	if target == nil {
		return nil, ErrUnsupported
	}
	scopes := make([]ScopeRegisterUsage, 0, len(program.ScopeExits)+1)
	scopes = append(scopes, ScopeRegisterUsage{ID: 0, Parent: -1, Leave: len(program.Instructions)})
	for _, exit := range program.ScopeExits {
		scopes = append(scopes, ScopeRegisterUsage{ID: exit.ID, Parent: exit.Parent, Enter: exit.Enter, Leave: exit.Position})
	}
	seen := make([]map[string]bool, len(scopes))
	seenFP := make([]map[string]bool, len(scopes))
	seenSpecial := make([]map[string]bool, len(scopes))
	for i := range seen {
		seen[i] = make(map[string]bool)
		seenFP[i] = make(map[string]bool)
		seenSpecial[i] = make(map[string]bool)
	}
	for position, ins := range program.Instructions {
		if ins.Op != ir.OpCallC && ins.Op != ir.OpCallGo && ins.Op != ir.OpSyscall {
			continue
		}
		kind, flavor := abi.FunctionCall, abi.C
		if ins.Op == ir.OpSyscall {
			kind = abi.SystemCall
		} else if ins.Op == ir.OpCallGo {
			flavor = abi.Go
		}
		bridgedC := options.ManagedCalls && ins.Op == ir.OpCallC
		if bridgedC {
			flavor = abi.Go
		}
		count := len(ins.Args) - 1
		if bridgedC {
			count++ // The C target is the helper's first Go argument.
		}
		inputs := ins.Args[1:]
		if bridgedC {
			inputs = ins.Args
		}
		words := make([]int, len(inputs))
		for i, id := range inputs {
			words[i] = (int(instructionBits(program.Instructions[id])) + int(target.RegisterBits()) - 1) / int(target.RegisterBits())
		}
		if bridgedC && target.Architecture() == "386" {
			// The compiler gateway uses uint64 Go slots, with native C widths
			// encoded in the target slot's high word.
			for i := range words {
				words[i] = 2
			}
		}
		if target.Architecture() == "386" && ins.Op == ir.OpCallGo {
			bits := make([]uint8, len(inputs))
			for i, id := range inputs {
				bits[i] = instructionBits(program.Instructions[id])
			}
			if err := validate386GoSubwords(bits); err != nil {
				return nil, err
			}
		}
		effect, err := cachedCallEffect(abi.CallSpec{Architecture: target.Architecture(), Kind: kind, ABI: flavor, ArgumentCount: count, ArgumentWords: words})
		if err != nil {
			return nil, err
		}
		if ins.Effect != nil && !bridgedC {
			effect = effect.Clone()
			effect.ClobberedRegisters = append(effect.ClobberedRegisters, ins.Effect.ClobberedRegisters...)
			effect.ClobberedFPRegisters = append(effect.ClobberedFPRegisters, ins.Effect.ClobberedFPRegisters...)
			effect.ClobberedSpecialRegisters = append(effect.ClobberedSpecialRegisters, ins.Effect.ClobberedSpecialRegisters...)
		}
		fixed := make([]FixedRegisterUse, 0, len(ins.Args))
		if kind == abi.SystemCall {
			fixed = append(fixed, FixedRegisterUse{Position: position, Value: ins.Args[0], Register: effect.NumberRegister})
		} else if !bridgedC {
			fixed = append(fixed, FixedRegisterUse{Position: position, Value: ins.Args[0], Register: target.RegisterName(target.CallTargetRegister())})
		}
		for arg, value := range inputs {
			if arg < len(effect.ArgumentLocations) {
				for word, location := range effect.ArgumentLocations[arg].Words {
					if location.Storage == abi.Register {
						fixed = append(fixed, FixedRegisterUse{Position: position, Value: value, Word: word, Register: location.Register})
					}
				}
			} else if arg < len(effect.ArgumentRegisters) && effect.ArgumentRegisters[arg] != "" {
				fixed = append(fixed, FixedRegisterUse{Position: position, Value: value, Register: effect.ArgumentRegisters[arg]})
			}
		}
		for i := range scopes {
			if position < scopes[i].Enter || position >= scopes[i].Leave {
				continue
			}
			scopes[i].FixedUses = append(scopes[i].FixedUses, fixed...)
			for _, name := range effect.ClobberedRegisters {
				if !seen[i][name] {
					seen[i][name] = true
					scopes[i].ClobberedRegisters = append(scopes[i].ClobberedRegisters, name)
				}
			}
			for _, name := range effect.ClobberedFPRegisters {
				if !seenFP[i][name] {
					seenFP[i][name] = true
					scopes[i].ClobberedFPRegisters = append(scopes[i].ClobberedFPRegisters, name)
				}
			}
			for _, name := range effect.ClobberedSpecialRegisters {
				if !seenSpecial[i][name] {
					seenSpecial[i][name] = true
					scopes[i].ClobberedSpecialRegisters = append(scopes[i].ClobberedSpecialRegisters, name)
				}
			}
		}
	}
	if target.Architecture() == "amd64" && program.Control != nil {
		labelOrder := make(map[int]int)
		for order, step := range program.Control.Steps {
			if step.IsLabel {
				labelOrder[step.Label] = order
			}
		}
		for order, step := range program.Control.Steps {
			if step.IsLabel || step.Term != ir.TermBranch {
				continue
			}
			value, ok := amd64ZeroBranchValue(step.Predicate, program.Instructions)
			if !ok {
				continue
			}
			candidate := RegisterOpportunity{Position: step.End, Value: value, Register: "RCX", Kind: "JRCXZ"}
			if step.Predicate.Condition == ir.BranchNotEqual && labelOrder[step.To] < order {
				decrement := program.Instructions[value]
				if decrement.Op == ir.OpSub && decrement.B >= 0 && decrement.B < len(program.Instructions) &&
					program.Instructions[decrement.B].Op == ir.OpConst && program.Instructions[decrement.B].Imm == 1 {
					for _, arg := range step.Args {
						if arg == value {
							candidate.Kind = "LOOP"
							break
						}
					}
				}
			}
			for i := range scopes {
				if step.End > scopes[i].Enter && step.End < scopes[i].Leave || i == 0 {
					scopes[i].Opportunities = append(scopes[i].Opportunities, candidate)
				}
			}
		}
	}
	if target.Architecture() == "ppc64le" && program.Control != nil {
		labelOrder := make(map[int]int)
		for order, step := range program.Control.Steps {
			if step.IsLabel {
				labelOrder[step.Label] = order
			}
		}
		for order, step := range program.Control.Steps {
			if step.IsLabel || step.Term != ir.TermBranch {
				continue
			}
			value, nonzero, ok := ppcCountBranchValue(step.Predicate, program.Instructions)
			if !ok {
				continue
			}
			backedge := step.To
			args := step.Args
			if !nonzero {
				backedge, args = step.Other, step.OtherArgs
			}
			header, exists := labelOrder[backedge]
			if !exists || header >= order {
				continue
			}
			decrement := program.Instructions[value]
			if decrement.Op != ir.OpSub || decrement.B < 0 ||
				decrement.B >= len(program.Instructions) || program.Instructions[decrement.B].Op != ir.OpConst ||
				program.Instructions[decrement.B].Imm != 1 {
				continue
			}
			carried := false
			for _, arg := range args {
				carried = carried || arg == value
			}
			if !carried {
				continue
			}
			candidate := RegisterOpportunity{Position: step.End, Value: value, Register: "CTR", Kind: "BDNZ"}
			for i := range scopes {
				if step.End > scopes[i].Enter && step.End < scopes[i].Leave || i == 0 {
					scopes[i].Opportunities = append(scopes[i].Opportunities, candidate)
				}
			}
		}
	}
	return scopes, nil
}

func ppcCountBranchValue(node *ir.PredicateExpr, ops []ir.Instruction) (value int, nonzero, ok bool) {
	for node != nil && node.Kind == ir.PredicateNot {
		nonzero = !nonzero
		node = node.Left
	}
	if node == nil || node.Kind != ir.PredicateCompare || node.WithValue || node.Immediate != 0 ||
		node.Value < 0 || node.Value >= len(ops) || instructionBits(ops[node.Value]) != 64 ||
		(node.Condition != ir.BranchEqual && node.Condition != ir.BranchNotEqual) {
		return
	}
	return node.Value, (node.Condition == ir.BranchNotEqual) != nonzero, true
}
