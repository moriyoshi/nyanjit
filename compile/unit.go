package compile

import (
	"fmt"
	"slices"
	"sync"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

// Unit owns one selected, allocated, and laid-out function. Outputs are lazy
// and safe to request concurrently. A Unit never owns executable memory.
type Unit struct {
	view     abi.Report
	options  compileOptions
	boundary callableBoundary
	// One of the two is set: baseline holds a TierBaseline unit's code,
	// optimizedUnit a TierOptimized unit's lowering.
	baseline *baselineUnit
	*optimizedUnit
}

// optimizedUnit is the part of a Unit that only TierOptimized produces.
type optimizedUnit struct {
	hostGateways                            bool // managed C calls lowered without this host's gateways
	kind                                    emissionKind
	physical                                physicalProgram
	report                                  abi.EmissionReport // view shares its storage
	input                                   *loweringInput
	listingOnce, encodingOnce, templateOnce sync.Once
	listing                                 string
	listingErr                              error
	emission                                emissionTemplate
	encodingErr                             error
	executable                              *execution.Template
	templateErr                             error
	symbolicOnce                            sync.Once
	literalsOnce                            sync.Once
	symbolic                                string
	symbolicErr                             error
}

type callableBoundary uint8

const (
	boundaryLeaf callableBoundary = iota
	boundaryForeign
	boundaryManaged
	boundaryExternal
)

// Lower selects, allocates, and lays out a program once for t, without
// encoding bytes or registering runtime metadata. Report, Listing and
// Template are lazy views of that one result.
//
// For the in-process contexts (ContextNative, ContextForeign and
// ContextGoManaged), Lower checks f against the context first: it returns
// ErrExternalCallOnly for calls or system calls the context cannot make,
// ErrUnsupportedABI for an ABI the context does not accept, ErrStackRequired
// when the context needs abi.StackPlatform, and ErrPointerMapsRequired when a
// Go-managed function does not assume pointer-free values. Constants that
// leave a symbol unbound return ErrUnboundSymbol; an invalid program, Target,
// Context or constant returns ErrInvalidFunction, which also matches the
// *ir.ValidationError that locates a validation failure.
//
// Programs that do not fit the target return ErrBackendLimit. When the limit
// is a number, the error is a *LimitError: LimitArguments for too many entry
// parameters or for too many values carried across blocks (see docs/ir.md),
// LimitSpillWords for spill storage, LimitPredicateNodes for an
// oversized predicate in a raw program, and LimitBranchRange for an arm64
// branch or literal load beyond its reach.
//
// Units for a target this host cannot run (another architecture, or a CPU
// profile the host lacks) give exact reports and listings; their Template
// reports why the code cannot run here. Go-managed outbound C calls use
// gateways from the target host's runtime, so for those units only
// ListingSymbolic can render them.
func Lower(t Target, program ir.Input, f Func) (*Unit, error) {
	if t == nil || t.resolved() == nil {
		return nil, ErrInvalidFunction
	}
	if f.Context > ContextExternal {
		return nil, fmt.Errorf("unknown call context %d: %w: %w", f.Context, abi.ErrInvalidSpec, ErrInvalidFunction)
	}
	if program == nil {
		return nil, ErrInvalidFunction
	}
	if t.resolved().spec.Tier == TierBaseline {
		return lowerBaseline(t, program, f)
	}
	if len(f.Constants) != 0 {
		bound, err := bindSymbols(program, f.Constants)
		if err != nil {
			return nil, err
		}
		program = bound
	}
	options := t.resolved().options(f)
	kind := emitLayoutText
	if t.resolved().native {
		kind = emitLayoutNative
	}
	input, err := inputForLowering(program, true, options)
	if err != nil {
		return nil, err
	}
	boundary := f.Context.boundary()
	if boundary != boundaryExternal {
		if err := validateCallableInput(input, options, boundary); err != nil {
			return nil, err
		}
	}
	u, err := lowerUnitInput(input, options, kind, boundary)
	if err != nil && input.promoted && promotionRetry(err) {
		// Promotion changes code quality, never what Lower accepts: a
		// promoted program that exceeds a limit, for example the frame of
		// a deep loop nest that carries every Var through every header,
		// lowers with Var homes instead.
		if input, err = inputForLowering(program, false, options); err != nil {
			return nil, err
		}
		u, err = lowerUnitInput(input, options, kind, boundary)
	}
	if err != nil {
		return nil, err
	}
	if kind == emitLayoutText && options.ManagedCalls {
		for _, ins := range input.code {
			u.hostGateways = u.hostGateways || ins.Op == opCallC
		}
	}
	return u, nil
}

func lowerUnitInputOnce(input *loweringInput, options compileOptions, kind emissionKind, boundary callableBoundary) (*Unit, error) {
	if input == nil {
		return nil, ErrInvalidFunction
	}
	_, err := emissionCPU(options, kind)
	if err != nil {
		return nil, err
	}
	if input.arity > abi.MaxArgumentsFor(targetArchitecture(options)) {
		return nil, entryArityError(input.arity, targetArchitecture(options))
	}
	scratch := getCompileScratch(options)
	input.scratch = scratch
	defer func() { input.scratch = nil; input.homes = nil; putCompileScratch(scratch) }()
	if input.homes == nil {
		input.homes = planVariableHomesInto(input.source, newMachineTarget(options), input.facts, &scratch.homes, options.Harden, options.Stack)
	}
	options.Features = slices.Clone(options.Features)
	var physical *physicalProgram
	var report abi.EmissionReport
	if input.control != nil {
		artifact, r, err := lowerControlProgram(input, options, kind)
		if err != nil {
			return nil, err
		}
		physical, report = artifact.physical, r
	} else {
		result, err := lowerFunctionMode(input.code, input.leaves, input.arity, input.result, input.resultParts, options, kind, input)
		if err != nil {
			return nil, err
		}
		physical, report = result.physical, result.report
	}
	if physical == nil {
		return nil, ErrInvalidFunction
	}
	options.CPU = report.CPU
	// Legalization creates this list exclusively for this lowering. Keep it
	// read-only with the Unit; public reports and templates copy it.
	report.VectorOperations = input.vectorOperations
	return &Unit{options: options, boundary: boundary, optimizedUnit: &optimizedUnit{kind: kind, physical: *physical, report: report, input: input}}, nil
}

// Report returns the unit's resource and effect report without encoding.
// The view is shared with the unit's template and never changes.
func (u *Unit) Report() abi.Report {
	if u == nil || u.view == nil {
		return abi.NewReport(abi.EmissionReport{})
	}
	return u.view
}

// Listing renders this unit's exact physical layout. It returns
// ErrUnsupported for a cross-target unit whose Go-managed C calls need the
// target host's gateways. When Template is available,
// both outputs use the same PCs and expansions, including concrete runtime
// gateways. Lower rejects cross-target programs whose gateways need the host.
// The compatibility EmitAssembly entry point always uses symbolic gateways,
// including on the native target, so its listings can differ from this method.
func (u *Unit) Listing() (string, error) {
	if u != nil && u.baseline != nil {
		u.baseline.listingOnce.Do(u.baseline.renderListing)
		return u.baseline.listing, u.baseline.listingErr
	}
	if u == nil || u.optimizedUnit == nil || u.input == nil {
		return "", ErrInvalidFunction
	}
	if u.hostGateways {
		return "", fmt.Errorf("managed C calls for %s need the target host's gateways; use ListingSymbolic: %w", targetArchitecture(u.options), ErrUnsupported)
	}
	u.listingOnce.Do(u.renderListing)
	return u.listing, u.listingErr
}

// finishLiterals completes the deferred literal pool once, before the first
// listing or encoding reads the physical program.
func (u *Unit) finishLiterals() {
	u.literalsOnce.Do(func() {
		if u.physical.literalsPending {
			finishLiterals(&u.physical)
		}
	})
}

func (u *Unit) renderListing() {
	u.finishLiterals()
	assembler := newMachineAssembler(u.options, emitText)
	if assembler == nil {
		u.listingErr = ErrUnsupported
		return
	}
	artifact, err := assembleProgram(u.physical, assembler)
	u.listing, u.listingErr = artifact.assembly, err
}

// ListingSymbolic renders the function with symbolic names for Go/C runtime
// gateways instead of their host addresses, so it describes the code the same
// way on every host. Without managed C calls it equals Listing. Its errors
// are Lower's.
func (u *Unit) ListingSymbolic() (string, error) {
	if u != nil && u.baseline != nil {
		return u.Listing()
	}
	if u == nil || u.optimizedUnit == nil || u.input == nil {
		return "", ErrInvalidFunction
	}
	if u.kind == emitLayoutText {
		u.listingOnce.Do(u.renderListing)
		return u.listing, u.listingErr
	}
	u.symbolicOnce.Do(func() {
		input, err := inputForLowering(u.input.origin, u.input.promoted, u.options)
		if err != nil {
			u.symbolicErr = err
			return
		}
		text, err := lowerUnitInput(input, u.options, emitLayoutText, u.boundary)
		if err != nil {
			u.symbolicErr = err
			return
		}
		u.symbolic, u.symbolicErr = text.Listing()
	})
	return u.symbolic, u.symbolicErr
}

// RegisterScopes looks ahead through the program's value scopes. The root is
// scope zero and covers the entire program. It reports fixed call operands and
// call clobbers in source value IDs, before legalization and allocation, so it
// does not predict a narrow-memory pressure retry's byte accesses. It
// returns ErrUnsupported for an unknown target and a *LimitError
// (LimitArguments) for too many entry parameters.
func (u *Unit) RegisterScopes() ([]ScopeRegisterUsage, error) {
	if u != nil && u.baseline != nil {
		return nil, fmt.Errorf("RegisterScopes of a TierBaseline unit: %w", ErrUnsupported)
	}
	if u == nil || u.optimizedUnit == nil || u.input == nil {
		return nil, ErrInvalidFunction
	}
	return registerScopes(u.input.origin, u.options)
}

// encoded is internal: callers must not mutate its bytes/report/relocations.
func (u *Unit) encoded() (emissionTemplate, error) {
	if u == nil || u.optimizedUnit == nil || u.input == nil {
		return emissionTemplate{}, ErrInvalidFunction
	}
	u.encodingOnce.Do(func() {
		u.finishLiterals()
		if err := validateEmissionTarget(u.options, emitNative); err != nil {
			u.encodingErr = err
			return
		}
		assembler := newMachineAssembler(u.options, emitNative)
		if assembler == nil {
			u.encodingErr = ErrUnsupported
			return
		}
		artifact, err := assembleProgram(u.physical, assembler)
		if err != nil {
			u.encodingErr = err
			return
		}
		var unwind goUnwindInfo
		if u.options.ManagedCalls {
			unwind, err = goUnwindFromProgram(u.physical)
			if err != nil {
				u.encodingErr = err
				return
			}
		}
		u.emission = emissionTemplate{Bytes: artifact.code, Report: u.report, Relocations: artifact.relocations, goUnwind: unwind}
	})
	return u.emission, u.encodingErr
}

// Template encodes the function on first request and returns the same
// immutable template and error afterwards. Each Bind creates a separate
// mapping. Go-managed units are checked against the target's managed-frame
// rules: a frame above the limit returns a *LimitError (LimitFrameBytes),
// and a target without managed frames, a workspace or a call without a frame
// return ErrBackendLimit. Native encoding requires the host architecture and
// a CPU profile the host supports (ErrUnsupported, ErrCPUUnsupported).
// This holds for external units too: their template's bytes are encoded
// for the host architecture. Listing describes code for any target.
func (u *Unit) Template() (*execution.Template, error) {
	if u != nil && u.baseline != nil {
		return u.baseline.templateFor(u.view)
	}
	if u == nil || u.optimizedUnit == nil || u.input == nil {
		return nil, ErrInvalidFunction
	}
	u.templateOnce.Do(func() {
		if u.boundary == boundaryExternal {
			emission, err := u.encoded()
			if err != nil {
				u.templateErr = err
				return
			}
			u.executable = newExecutableTemplate(emission, u.view, u.input.arity, u.input.paramBits, u.input.source.ParameterTypes, u.options.ABI, false, false, true, nil, u.options.Harden)
			return
		}
		// Lower already checked the program against its call context.
		boundary := u.boundary
		if boundary == boundaryManaged {
			target := newMachineTarget(u.options)
			if target == nil {
				u.templateErr = ErrUnsupported
				return
			}
			limit := target.ManagedFrameLimit()
			switch {
			case limit == 0:
				u.templateErr = fmt.Errorf("%s has no managed frame support: %w", target.Architecture(), ErrBackendLimit)
			case u.report.WorkspaceBytes != 0:
				u.templateErr = fmt.Errorf("managed entry requires %d workspace bytes: %w", u.report.WorkspaceBytes, ErrBackendLimit)
			case u.report.StackBytes > limit:
				u.templateErr = &LimitError{Kind: LimitFrameBytes, Needed: u.report.StackBytes, Limit: limit, Arch: target.Architecture(), Detail: "managed frame"}
			case u.report.StackBytes == 0 && u.report.HasFunctionCalls:
				u.templateErr = fmt.Errorf("managed calls require a nonempty frame: %w", ErrBackendLimit)
			}
			if u.templateErr != nil {
				return
			}
		}
		targets, err := callableGoTargets(u.input)
		if err != nil {
			u.templateErr = err
			return
		}
		emission, err := u.encoded()
		if err != nil {
			u.templateErr = err
			return
		}
		u.executable = newExecutableTemplate(emission, u.view, u.input.arity, u.input.paramBits, u.input.source.ParameterTypes, u.options.ABI, boundary == boundaryForeign, boundary == boundaryManaged, false, targets, u.options.Harden)
	})
	return u.executable, u.templateErr
}

func validateCallableInput(b *loweringInput, options compileOptions, boundary callableBoundary) error {
	if b == nil {
		return ErrInvalidFunction
	}
	switch boundary {
	case boundaryManaged:
		if options.ABI != abi.Go && options.ABI != abi.C {
			return ErrUnsupportedABI
		}
		if !options.AssumePointerFree {
			return ErrPointerMapsRequired
		}
		if options.Stack != abi.StackPlatform || options.StackPointer != abi.StackPointerReserved {
			return ErrStackRequired
		}
	case boundaryForeign:
		if len(b.resultParts) != 0 {
			return ErrBackendLimit
		}
		if b.control != nil {
			for _, step := range b.control.Steps {
				if len(step.ResultParts) != 0 {
					return ErrBackendLimit
				}
			}
		}
		if options.ABI != abi.C {
			return ErrUnsupportedABI
		}
		if options.Stack != abi.StackPlatform || options.StackPointer != abi.StackPointerReserved {
			return ErrStackRequired
		}
	}
	for _, ins := range b.code {
		if ins.Op == opSyscall || boundary == boundaryLeaf && (ins.Op == opCallC || ins.Op == opCallGo) || boundary == boundaryForeign && ins.Op == opCallGo {
			return ErrExternalCallOnly
		}
	}
	return nil
}
