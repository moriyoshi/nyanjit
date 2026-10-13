package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// loweringInput is the validated, detached input to the machine
// backend. Compilation never reconstructs a mutable Builder.
type loweringInput struct {
	code             []instruction
	leaves           []scopeEvent
	control          *ir.Control
	arity            int
	result           int
	resultParts      []int
	vectorOperations []abi.VectorOperation
	// paramBits holds each entry parameter's scalar width; zero marks a
	// vector parameter.
	paramBits []uint8
	source    *ir.Program
	// fallbackSource is the validated graph before SIMD/memory legalization.
	// The lowering entry point clears it before returning a Unit.
	fallbackSource *ir.Program
	// origin is the validated source graph, before cross-block threading and
	// legalization. A Unit keeps it to lower again for a symbolic listing and
	// to report register scopes in source value IDs.
	origin *ir.Program
	// view holds the borrowed sealed storage that origin points to.
	view ir.Program
	// promoted records that Var promotion rewrote the source; a Unit lowers
	// origin the same way again for its symbolic listing.
	promoted bool
	homes    *machine.HomePlan
	facts    *programFacts
	scratch  *compileScratch
}

// inputFromProgram plans Var homes for program as written, without Var
// promotion, for callers that inspect the home plan.
func inputFromProgram(program ir.Input, options ...compileOptions) (*loweringInput, error) {
	return makeLoweringInput(program, false, false, options...)
}

// inputForLowering prepares program for Lower. With promote, promotable Vars
// become SSA values (see promoteVariables).
func inputForLowering(program ir.Input, promote bool, options ...compileOptions) (*loweringInput, error) {
	return makeLoweringInput(program, true, promote, options...)
}

func makeLoweringInput(program ir.Input, deferHomes, promote bool, options ...compileOptions) (*loweringInput, error) {
	input := new(loweringInput)
	facts, err := validatedSourceInto(program, &input.view)
	if err != nil {
		return nil, err
	}
	snapshot := &input.view
	selectedOptions := compileOptions{}
	if len(options) != 0 {
		selectedOptions = options[0]
	}
	paramBits := facts.EntryParamBits()
	snapshot, facts, original, vectorOperations, promoted, err := preparedSource(snapshot, facts, promote, selectedOptions)
	if err != nil {
		return nil, err
	}
	var homes *machine.HomePlan
	if !deferHomes {
		homes = planVariableHomesWithFacts(snapshot, newMachineTarget(selectedOptions), facts, selectedOptions.Harden, selectedOptions.Stack)
	}
	*input = loweringInput{
		view:   input.view,
		source: snapshot, fallbackSource: original, origin: &input.view, homes: homes, facts: facts,
		code: snapshot.Instructions, leaves: snapshot.ScopeExits, vectorOperations: vectorOperations,
		arity: snapshot.Arity, result: snapshot.Result, resultParts: snapshot.ResultParts,
		paramBits: paramBits, promoted: promoted,
	}
	if snapshot.Control != nil {
		input.control = snapshot.Control
	}
	return input, nil
}

// preparedSource promotes Vars when promote is set, threads cross-block
// values and legalizes vectors. original is the program before
// legalization. A promoted program that threading or legalization rejects,
// for example one that carries more values across blocks than the budget
// allows, is prepared again without promotion: promotion changes code
// quality, never what lowering accepts.
func preparedSource(source *ir.Program, sourceFacts *programFacts, promote bool, options compileOptions) (snapshot *ir.Program, facts *programFacts, original *ir.Program, operations []abi.VectorOperation, promoted bool, err error) {
	snapshot, facts = source, sourceFacts
	if promote && len(snapshot.Variables) != 0 {
		next := promoteVariables(snapshot, facts, promotionPolicyFor(options))
		switch {
		case next == snapshot:
		case !promotionStrict && hasCrossBlockUses(next):
			// Threading reads only the program's structure, and sealing its
			// result validates the promoted program too.
			snapshot, facts, promoted = next, nil, true
		default:
			view, nextFacts, sealErr := promotedSource(next)
			if sealErr == nil {
				snapshot, facts, promoted = view, nextFacts, true
			} else if promotionStrict {
				return nil, nil, nil, nil, false, sealErr
			}
		}
	}
	if hasCrossBlockUses(snapshot) {
		if snapshot, facts, err = threadedSource(snapshot); err != nil {
			if promoted && promotionRetry(err) {
				return preparedSource(source, sourceFacts, false, options)
			}
			return nil, nil, nil, nil, false, err
		}
	}
	original = snapshot
	snapshot, operations, err = legalizeVectors(snapshot, options)
	if err != nil {
		if promoted && promotionRetry(err) {
			return preparedSource(source, sourceFacts, false, options)
		}
		return nil, nil, nil, nil, false, err
	}
	if snapshot != original {
		facts = newProgramFacts(snapshot)
	}
	return snapshot, facts, original, operations, promoted, nil
}

// Only the internal opaque snapshot can bypass validation. Raw adapters always
// seal a detached copy, so public field edits cannot leave a stale trusted bit.
func validatedSource(program ir.Input) (*ir.Program, *programFacts, error) {
	view := new(ir.Program)
	facts, err := validatedSourceInto(program, view)
	if err != nil {
		return nil, nil, err
	}
	return view, facts, nil
}

// validatedSourceInto seals program if needed and fills view with its
// borrowed storage, which the compiler only reads.
func validatedSourceInto(program ir.Input, view *ir.Program) (*programFacts, error) {
	if program == nil {
		return nil, ErrInvalidFunction
	}
	sealed, ok := program.(*ir.SealedProgram)
	if !ok {
		var err error
		sealed, err = ir.SealOwned(program.Clone())
		if err != nil {
			return nil, invalidSource(err)
		}
	}
	facts := sealed.Analysis()
	if facts == nil {
		return nil, ErrInvalidFunction
	}
	sealedView(sealed, view)
	return facts, nil
}

// threadedSource returns program with its cross-block uses threaded through
// label parameters (see threadCrossBlockValues), sealed, and its analysis.
func threadedSource(program *ir.Program) (*ir.Program, *programFacts, error) {
	threaded, err := threadCrossBlockValues(program)
	if err != nil {
		return nil, nil, err
	}
	sealed, err := ir.SealOwned(threaded)
	if err != nil {
		return nil, nil, fmt.Errorf("threading cross-block values: %w: %w", ErrInvalidFunction, err)
	}
	view := new(ir.Program)
	sealedView(sealed, view)
	return view, sealed.Analysis(), nil
}

// promotedSource seals a program from promoteVariables. A program that
// fails validation is an internal error; the caller then lowers the
// unpromoted source.
func promotedSource(program *ir.Program) (*ir.Program, *programFacts, error) {
	sealed, err := ir.SealOwned(program)
	if err != nil {
		return nil, nil, fmt.Errorf("promoting Vars: %w: %w", ErrInvalidFunction, err)
	}
	view := new(ir.Program)
	sealedView(sealed, view)
	return view, sealed.Analysis(), nil
}

func sealedView(sealed *ir.SealedProgram, view *ir.Program) {
	*view = ir.Program{
		ParameterTypes: sealed.ParameterTypes(), Variables: sealed.Variables(), Instructions: sealed.Instructions(),
		ScopeExits: sealed.ScopeExits(), Control: sealed.Control(), Arity: sealed.Arity(), Result: sealed.Result(),
		ResultParts: sealed.ResultParts(),
	}
}
