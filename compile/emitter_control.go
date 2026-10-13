package compile

import (
	"errors"
	"fmt"
	"math/bits"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

const (
	branchSign    = machine.BranchSign
	branchNotSign = machine.BranchNotSign
)

type emissionStepKind uint8

const (
	stepLabel emissionStepKind = iota
	stepBuilder
	stepJump
	stepBranch
	stepExit
)

type emissionStep struct {
	kind         emissionStepKind
	label, other ir.Label
	fragment     *ir.Program
	segment      *builderSegment
	predicate    *ir.PredicateExpr
	args         []int
	otherArgs    []int
	condition    ir.BranchCondition
	comparison   int64
}

type builderSegment struct {
	start       int
	ops         []instruction
	leaves      []scopeEvent
	result      int
	resultParts []int
	exports     []int
	arity       int
	bare        bool
	direct      bool
	skip        bool
	// params are the ops reading the label's parameters, by parameter, or
	// -1; repeated is set when two read one.
	params   []int
	repeated bool
}

// Emitter lays out Builder fragments, assigns PCs, and resolves labels.
// Label arguments use their final register or spill homes across fragments.
type Emitter struct {
	source         *ir.Program
	homePlan       *machine.HomePlan
	facts          *programFacts
	scratch        *compileScratch
	steps          []emissionStep
	labels         []bool
	labelSlots     [][]int
	labelRegisters [][]int
	labelWords     [][]uint8
	labelBits      [][]uint8
	reservedSlots  int
	// homeSlots are the reserved slots of the variable homes, which
	// reservedSlots follows with the label parameters'.
	homeSlots int
	// compactPins is, per label, the parameter amd64 compact loops keep in
	// RCX for LOOP, or -1; nil when there is none.
	compactPins []int
	hasValue    bool
	// preserve marks registers a previous lowering pass wrote without
	// saving; lower plans their entry saves up front.
	preserve registerWrites
}

// NewEmitter returns an empty Emitter.
func NewEmitter() *Emitter { return &Emitter{} }

// NewLabel reserves a label that can be bound later, including after a jump to it.
func (e *Emitter) NewLabel() ir.Label {
	if e == nil {
		return -1
	}
	id := ir.Label(len(e.labels))
	e.labels = append(e.labels, false)
	return id
}

func (e *Emitter) validLabel(label ir.Label) bool {
	return e != nil && label >= 0 && int(label) < len(e.labels)
}

func (e *Emitter) edgeHasWide(label ir.Label) bool {
	if e == nil || label < 0 || int(label) >= len(e.labelWords) {
		return false
	}
	for n, words := range e.labelWords[label] {
		if words > 1 && (e.labelRegisters[label][n] < 0 || !backend.IsVectorRegister(physReg(e.labelRegisters[label][n]))) {
			return true
		}
	}
	return false
}

// Bind marks the current stream position. Multiple labels may share a
// position. An unknown or already bound label returns ErrInvalidFunction.
func (e *Emitter) Bind(label ir.Label) error {
	if !e.validLabel(label) || e.labels[label] {
		return ErrInvalidFunction
	}
	e.labels[label] = true
	e.steps = append(e.steps, emissionStep{kind: stepLabel, label: label})
	e.hasValue = false
	return nil
}

// AppendProgram emits a validated straight-line IR fragment. A program that
// is invalid, has control flow or takes parameters returns
// ErrInvalidFunction.
func (e *Emitter) AppendProgram(program ir.Input) error {
	if e == nil || program == nil {
		return ErrInvalidFunction
	}
	fragment, _, sourceErr := validatedSource(program)
	if sourceErr != nil || fragment.Control != nil || fragment.Arity != 0 {
		return ErrInvalidFunction
	}
	e.steps = append(e.steps, emissionStep{kind: stepBuilder, fragment: fragment})
	e.hasValue = true
	return nil
}

// Jump emits an unconditional edge to a label. An unknown label returns
// ErrInvalidFunction.
func (e *Emitter) Jump(to ir.Label) error {
	if !e.validLabel(to) {
		return ErrInvalidFunction
	}
	e.steps = append(e.steps, emissionStep{kind: stepJump, label: to})
	e.hasValue = false
	return nil
}

// Branch compares the last Builder fragment's result with immediate and jumps
// to one of two labels. The comparison is signed and fits both targets.
// It returns ErrInvalidFunction without a preceding fragment result, for an
// unknown label or condition, or for an immediate outside -4095..4095.
func (e *Emitter) Branch(immediate int64, condition ir.BranchCondition, ifTrue, ifFalse ir.Label) error {
	if e == nil || !e.hasValue || !e.validLabel(ifTrue) || !e.validLabel(ifFalse) ||
		condition > ir.BranchAbove || immediate < -4095 || immediate > 4095 {
		return ErrInvalidFunction
	}
	if previous := e.steps[len(e.steps)-1]; previous.fragment != nil && len(previous.fragment.ResultParts) != 0 {
		return ErrInvalidFunction
	}
	e.steps = append(e.steps, emissionStep{kind: stepBranch, label: ifTrue, other: ifFalse, condition: condition, comparison: immediate})
	e.hasValue = false
	return nil
}

// Exit returns the last Builder fragment's result to the caller. Without a
// preceding fragment result it returns ErrInvalidFunction.
func (e *Emitter) Exit() error {
	if e == nil || !e.hasValue {
		return ErrInvalidFunction
	}
	e.steps = append(e.steps, emissionStep{kind: stepExit})
	e.hasValue = false
	return nil
}

// Emit lowers the recorded fragments for t and returns linked native bytes.
// f.Context must be ContextNative or ContextGoManaged and f.Constants must be
// empty; otherwise Emit returns ErrInvalidFunction. It requires the host
// architecture and a CPU profile the host supports (ErrUnsupported,
// ErrCPUUnsupported) and returns ErrUnboundSymbol when a fragment
// references a symbol. Lowering errors are Lower's: ErrUnsupportedABI,
// ErrStackRequired, ErrExternalCallOnly, and ErrBackendLimit or a
// *LimitError for fragments that do not fit the target.
func (e *Emitter) Emit(t Target, f Func) ([]byte, abi.Report, error) {
	options, err := emitterOptions(t, f)
	if err != nil {
		return nil, nil, err
	}
	artifact, report, err := e.lower(options, emitNative)
	if err != nil {
		return nil, nil, err
	}
	if len(artifact.relocations) != 0 {
		return nil, nil, ErrUnboundSymbol
	}
	return artifact.code, abi.ReportOf(report), nil
}

// EmitAssembly lowers the recorded fragments for t and returns a listing.
// Its Func rules and errors match Emit's, except that any target can be
// listed and symbols stay unbound.
func (e *Emitter) EmitAssembly(t Target, f Func) (string, error) {
	options, err := emitterOptions(t, f)
	if err != nil {
		return "", err
	}
	artifact, _, err := e.lower(options, emitText)
	return artifact.assembly, err
}

func emitterOptions(t Target, f Func) (compileOptions, error) {
	if t == nil || t.resolved() == nil || len(f.Constants) != 0 ||
		f.Context != ContextNative && f.Context != ContextGoManaged {
		return compileOptions{}, ErrInvalidFunction
	}
	return t.resolved().options(f), nil
}

// unsavedPreservedError reports callee-preserved registers that a lowering
// pass wrote after their entry saves were planned.
type unsavedPreservedError struct{ registers []physReg }

func (err unsavedPreservedError) Error() string {
	return fmt.Sprintf("callee-preserved registers %v written without a save", err.registers)
}

// lower emits the recorded fragments. Edges, predicates and expansions
// write registers only as the steps are emitted, after the prologue's saves
// are planned; a pass that writes an unsaved callee-preserved register is
// repeated with that register saved. Each repeat saves at least one more
// register, so the passes end.
func (e *Emitter) lower(options compileOptions, kind emissionKind) (emissionArtifact, abi.EmissionReport, error) {
	if e == nil {
		return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
	}
	defer func() { e.preserve = registerWrites{} }()
	for {
		artifact, report, err := e.lowerPass(options, kind)
		var unsaved unsavedPreservedError
		if !errors.As(err, &unsaved) {
			return artifact, report, err
		}
		added := false
		for _, r := range unsaved.registers {
			added = added || !e.preserve[r]
			e.preserve[r] = true
		}
		if !added {
			return emissionArtifact{}, abi.EmissionReport{}, fmt.Errorf("%w: %w", err, ErrInvalidFunction)
		}
		// A pass leases its emitter as the scratch workspace's first stream
		// and rewinds to it between fragments; the next pass starts over.
		if e.scratch != nil {
			e.scratch.machine.Rewind(0)
			e.scratch.selection.rewind()
		}
	}
}

func (e *Emitter) lowerPass(options compileOptions, kind emissionKind) (emissionArtifact, abi.EmissionReport, error) {
	var edgeRegisterBuffer [64]physReg
	var localAllocationBuffers allocationTemps
	allocationBuffers := &localAllocationBuffers
	if e != nil && e.scratch != nil {
		allocationBuffers = &e.scratch.allocation
	}
	if err := validateEmissionTarget(options, kind); err != nil {
		return emissionArtifact{}, abi.EmissionReport{}, err
	}
	if e == nil || len(e.steps) == 0 || e.hasValue {
		return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
	}
	last := e.steps[len(e.steps)-1].kind
	if last != stepJump && last != stepBranch && last != stepExit {
		return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
	}
	if options.ABI != abi.C && options.ABI != abi.Go {
		return emissionArtifact{}, abi.EmissionReport{}, ErrUnsupportedABI
	}
	if options.StackBytesAvailable < 0 || options.StackPointer != abi.StackPointerReserved ||
		options.Stack != abi.StackPlatform && options.Stack != abi.StackNoUse {
		return emissionArtifact{}, abi.EmissionReport{}, ErrStackRequired
	}
	target := newMachineTarget(options)
	if target == nil {
		return emissionArtifact{}, abi.EmissionReport{}, ErrUnsupported
	}
	var barrier physicalInsn
	if options.Harden {
		var err error
		barrier, err = hardeningBarrier(target)
		if err != nil {
			return emissionArtifact{}, abi.EmissionReport{}, err
		}
	}
	emitter := newMachineEmitter(options, e.scratch)
	var assembler machineAssembler
	if !kind.layout() {
		assembler = newMachineAssembler(options, kind)
	}
	if emitter == nil || !kind.layout() && assembler == nil {
		return emissionArtifact{}, abi.EmissionReport{}, ErrUnsupported
	}
	if options.ManagedCalls && target.ManagedFrameLimit() == 0 {
		return emissionArtifact{}, abi.EmissionReport{}, ErrUnsupported
	}
	wideReturn, scalarReturn := false, false
	maxLogicalSlots := 0
	// Label slots are written by edges even when the destination fragment never
	// reads a parameter. They must not overlap edge scratch or ABI save slots.
	for label, list := range e.labelSlots {
		for n, slot := range list {
			if slot >= 0 {
				maxLogicalSlots = max(maxLogicalSlots, slot+int(e.labelWords[label][n]))
			}
		}
	}
	for id, step := range e.steps {
		if step.kind != stepExit {
			continue
		}
		if id == 0 || e.steps[id-1].kind != stepBuilder {
			return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
		}
		previous := e.steps[id-1]
		if previous.segment != nil && len(previous.segment.resultParts) != 0 ||
			previous.fragment != nil && len(previous.fragment.ResultParts) != 0 {
			wideReturn = true
		} else {
			scalarReturn = true
		}
	}
	if wideReturn && scalarReturn {
		return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
	}
	_, supportsWideResult := target.Integer128Result()
	if wideReturn && (options.ABI != abi.C || target.RegisterBits() != 64 || !supportsWideResult) {
		return emissionArtifact{}, abi.EmissionReport{}, fmt.Errorf("%s ABI %d cannot return a wide control-flow result: %w", target.Architecture(), options.ABI, ErrBackendLimit)
	}
	if target.Architecture() == "386" {
		for id, step := range e.steps {
			if step.kind == stepExit || step.kind == stepBranch {
				if id == 0 || e.steps[id-1].kind != stepBuilder {
					return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
				}
				previous := e.steps[id-1]
				if previous.segment != nil {
					if previous.segment.result < 0 || previous.segment.result >= len(previous.segment.ops) ||
						(step.kind == stepBranch && step.predicate == nil && instructionBits(previous.segment.ops[previous.segment.result]) > 32) {
						return emissionArtifact{}, abi.EmissionReport{}, fmt.Errorf("386 control step %d needs a scalar word for its branch result: %w", id, ErrBackendLimit)
					}
				} else if previous.fragment == nil || previous.fragment.Result < 0 ||
					previous.fragment.Result >= len(previous.fragment.Instructions) ||
					step.kind == stepBranch && step.predicate == nil && instructionBits(previous.fragment.Instructions[previous.fragment.Result]) > 32 {
					return emissionArtifact{}, abi.EmissionReport{}, fmt.Errorf("386 control step %d has an invalid or wide branch result: %w", id, ErrBackendLimit)
				}
			}
		}
	}
	if e.reservedSlots > newMachineEmitter(options).spillSlotLimit() {
		return emissionArtifact{}, abi.EmissionReport{}, spillStorageLimitError(e.reservedSlots, e.reservedSlots, newMachineEmitter(options).spillSlotLimit())
	}
	var localControl controlBuffers
	buffers := &localControl
	if e.scratch != nil {
		buffers = &e.scratch.control
	}
	fragments := resetBufferTracked(&buffers.fragments, &buffers.history.fragments, len(e.steps))
	// Fragment copies live until the pass assembles them; they share chunks.
	var fragmentArena []physicalInsn
	// Each fragment's body is copied into fragments before the next fragment
	// emitter builds one, so the bodies share one buffer.
	var bodyBuffer []locatedInsn
	// Fragment emitters live until the pass ends; they share chunks that
	// double up to 16 emitters, so a stale pointer to one keeps little else
	// alive.
	var emitterChunk []physicalEmitter
	leaseFragmentEmitter := func() machineEmitter {
		if len(emitterChunk) == cap(emitterChunk) {
			emitterChunk = make([]physicalEmitter, 0, min(16, max(2, 2*cap(emitterChunk))))
		}
		emitterChunk = emitterChunk[:len(emitterChunk)+1]
		emitter := newMachineEmitterIn(&emitterChunk[len(emitterChunk)-1], options, e.scratch)
		if emitter != nil {
			emitter.(*physicalEmitter).bodyBuffer = &bodyBuffer
		}
		return emitter
	}
	exportSlots := resetBufferTracked(&buffers.exportSlots, &buffers.history.exportSlots, len(e.steps))
	exportHomes := resetBufferTracked(&buffers.exportHomes, &buffers.history.exportHomes, len(e.steps))
	results := resetBufferTracked(&buffers.results, &buffers.history.results, len(e.steps))
	resultRegisters := resetBufferTracked(&buffers.resultRegisters, &buffers.history.resultRegisters, len(e.steps))
	resultWords := resetBufferTracked(&buffers.resultWords, &buffers.history.resultWords, len(e.steps))
	resultWordCounts := resetBufferTracked(&buffers.resultWordCounts, &buffers.history.resultWordCounts, len(e.steps))
	flagReports := resetBufferTracked(&buffers.flagReports, &buffers.history.flagReports, len(e.steps))
	var written registerWrites
	frame := 0
	hasCall := false
	resultLayout, err := entryLayout(target, nil)
	if err != nil {
		return emissionArtifact{}, abi.EmissionReport{}, err
	}
	report := abi.EmissionReport{
		Architecture: target.Architecture(), ABI: options.ABI, PPC64CPU: ppc64CPULevel(target), CPU: reportCPU(options), GoManaged: options.ManagedCalls,
		Stack: options.Stack, StackPointer: options.StackPointer,
		ResultRegister:     target.RegisterName(target.ResultRegister()),
		ResultLocation:     resultLayout.result,
		ResultWords:        1,
		WorkspaceAlignment: int(target.RegisterBits() / 8),
	}
	if wideReturn {
		report.ResultWords = 2
		report.ResultRegister = ""
		pair, _ := target.Integer128Result()
		report.ResultLocation.Words = []abi.WordLocation{
			{Storage: abi.Register, Register: target.RegisterName(pair[0])},
			{Storage: abi.Register, Register: target.RegisterName(pair[1])},
		}
	}
	// With the switch on, Online allocates the segments as one function
	// when it can; the steps then keep its edges' moves.
	var onlineEdges [][2][]physicalInsn
	var onlinePredicates []*ir.PredicateExpr
	var onlineClamps []int
	var onlinePairs []map[int][2]physReg // per builder step: the 386 pairs its predicate compares
	if onlineAllocation {
		labelSlots := maxLogicalSlots
		maxLogicalSlots = 0 // the labels' slots go unused
		control, ok, err := allocateOnlineControl(e, options, kind, target, leaseFragmentEmitter, allocationBuffers,
			func(id int, program *machineProgram, physical physicalProgram, homes map[int]exportLocation, pairs map[int][2]physReg) {
				// reportControlEffects cannot fail: allocateOnlineControl
				// leaves Go calls it would reject to the direct walk.
				_ = reportControlEffects(&report, &hasCall, program, options)
				if s := e.steps[id].segment; s != nil && s.arity > 0 {
					entry, _ := entryLayout(target, entryWordsFromIR(s.ops, target.RegisterBits()))
					report.ArgumentRegisters = make([]string, s.arity)
					report.ArgumentLocations = entry.arguments
					if target.Architecture() == "386" && options.ABI == abi.Go {
						report.ResultLocation = entry.result
					}
					for i := range report.ArgumentRegisters {
						report.ArgumentRegisters[i] = singleABIRegister(entry.arguments[i])
					}
				}
				report.LoweringStats.Blocks++
				report.LoweringStats.Allocations++
				results[id] = program.Result
				resultRegisters[id] = physical.resultPhysical
				resultWords[id] = physical.resultWords
				resultWordCounts[id] = physical.resultWordCount
				flagReports[id] = physical.flags
				exportHomes[id] = homes
				if pairs != nil {
					if onlinePairs == nil {
						onlinePairs = make([]map[int][2]physReg, len(e.steps))
					}
					onlinePairs[id] = pairs
				}
				frame = max(frame, physical.stackBytes)
				emitter.(*physicalEmitter).callAreaBytes = max(emitter.(*physicalEmitter).callAreaBytes, physical.callAreaBytes)
				fragments[id] = carve(&fragmentArena, len(physical.insns))
				for _, located := range physical.insns {
					fragments[id] = append(fragments[id], located.ins)
				}
				maxLogicalSlots = max(maxLogicalSlots, referencedSpillSlots(fragments[id], target.RegisterBits()))
			})
		if err != nil {
			return emissionArtifact{}, abi.EmissionReport{}, err
		}
		if ok {
			onlineEdges, onlinePredicates, onlineClamps = control.edges, control.predicates, control.clamps
			maxLogicalSlots = max(maxLogicalSlots, control.slots)
			for reg, modified := range control.written {
				written[reg] = written[reg] || modified
			}
		} else {
			maxLogicalSlots = labelSlots
		}
	}
	for id, step := range e.steps {
		if onlineEdges != nil {
			break
		}
		if step.kind != stepBuilder {
			continue
		}
		if step.segment != nil && step.segment.skip {
			continue
		}
		if e.scratch != nil {
			e.scratch.machine.Rewind(1)
			e.scratch.selection.rewind()
		}
		var program machineProgram
		var err error
		directJump := false
		directBranch := false
		if step.segment != nil {
			s := step.segment
			if len(s.resultParts) != 0 {
				program, err = selectProgramResultParts(s.ops, s.leaves, s.arity, s.result, s.resultParts, target, e.scratch)
			} else {
				program, err = selectProgramExports(s.ops, s.leaves, s.arity, s.result, s.exports, target, e.scratch)
			}
			if err == nil && id+1 < len(e.steps) && s.direct && e.steps[id+1].kind == stepJump &&
				!e.edgeHasWide(e.steps[id+1].label) {
				edge := e.steps[id+1]
				program.DirectExports = true
				directJump = true
				program.ExportRegisters = make([]int, len(program.Exports))
				for i, value := range program.Exports {
					program.ExportRegisters[i] = -1
					for j, arg := range edge.args {
						if arg == value {
							program.ExportRegisters[i] = e.labelRegisters[edge.label][j]
							break
						}
					}
				}
			}
			if err == nil && id+1 < len(e.steps) && e.steps[id+1].kind == stepBranch &&
				!e.edgeHasWide(e.steps[id+1].label) && !e.edgeHasWide(e.steps[id+1].other) &&
				e.steps[id+1].predicate != nil && len(s.exports) != 0 &&
				!(target.RegisterBits() == 32 && predicateNeedsPairedWords(e.steps[id+1].predicate, s.ops)) {
				edge := e.steps[id+1]
				program.DirectExports = true
				directBranch = true
				program.ExportRegisters = make([]int, len(program.Exports))
				for i, value := range program.Exports {
					program.ExportRegisters[i] = -1
					for j, arg := range edge.args {
						if arg == value {
							program.ExportRegisters[i] = e.labelRegisters[edge.label][j]
							break
						}
					}
					if program.ExportRegisters[i] < 0 {
						for j, arg := range edge.otherArgs {
							if arg == value {
								program.ExportRegisters[i] = e.labelRegisters[edge.other][j]
								break
							}
						}
					}
				}
			}
			if err == nil && target.Architecture() == "amd64" && id+1 < len(e.steps) &&
				e.steps[id+1].kind == stepBranch {
				if value, ok := amd64ZeroBranchValue(e.steps[id+1].predicate, s.ops); ok {
					cheap := true
					for _, ins := range s.ops {
						if ins.Op == opCallC || ins.Op == opCallGo || ins.Op == opSyscall {
							cheap = false
							break
						}
					}
					edge := e.steps[id+1]
					for _, destination := range []struct {
						label ir.Label
						args  []int
					}{{edge.label, edge.args}, {edge.other, edge.otherArgs}} {
						for index, arg := range destination.args {
							if arg == value && e.labelRegisters[destination.label][index] != 1 {
								cheap = false
							}
						}
					}
					if cheap {
						program.PreferredRegisters = map[int]backend.Register{value: 1}
					}
				}
			}
			if s.arity > 0 {
				entry, err := entryLayout(target, entryWordsFromIR(s.ops, target.RegisterBits()))
				if err != nil {
					return emissionArtifact{}, abi.EmissionReport{}, err
				}
				report.ArgumentRegisters = make([]string, s.arity)
				report.ArgumentLocations = entry.arguments
				if target.Architecture() == "386" && options.ABI == abi.Go {
					report.ResultLocation = entry.result
				}
				for i := range report.ArgumentRegisters {
					report.ArgumentRegisters[i] = singleABIRegister(entry.arguments[i])
				}
			}
		} else {
			fragment := step.fragment
			program, err = selectProgramResultParts(fragment.Instructions, fragment.ScopeExits, fragment.Arity, fragment.Result, fragment.ResultParts, target, e.scratch)
		}
		if err != nil {
			return emissionArtifact{}, abi.EmissionReport{}, err
		}
		if directBranch && predicateNeedsFPBits(e.steps[id+1].predicate, step.segment.ops, e.homePlan) {
			directBranch = false
			program.DirectExports = false
		}
		program.ReservedSlots = e.reservedSlots
		for value, desc := range program.Values {
			if desc.EntryReg >= 0 && backend.IsVectorRegister(physReg(desc.EntryReg)) {
				program.Values[value].Class = backend.Float
			}
		}
		if directJump && program.DirectExports {
			for n, x := range program.Exports {
				if x == program.Result && program.ExportRegisters[n] >= 0 && backend.IsVectorRegister(physReg(program.ExportRegisters[n])) {
					program.ResultAsFloat = true
				}
			}
		}
		if step.segment != nil && id+1 < len(e.steps) && e.steps[id+1].kind == stepJump && len(e.steps[id+1].args) == 0 {
			program.DiscardResult = true
		}
		if err := reportControlEffects(&report, &hasCall, &program, options); err != nil {
			return emissionArtifact{}, abi.EmissionReport{}, err
		}
		if step.segment != nil {
			bindVariableHomes(&program, e.source, step.segment.start, e.homePlan, e.facts)
		}
		if err := bridgeManagedCCalls(&program, options, kind); err != nil {
			return emissionArtifact{}, abi.EmissionReport{}, err
		}
		var physical physicalProgram
		var workspace int
		var fragmentWritten registerWrites
		fastFlags := false
		allocations := 0
		report.LoweringStats.Blocks++
		if s := step.segment; s != nil && s.bare && len(s.exports) != 0 && id+1 < len(e.steps) && e.steps[id+1].predicate != nil && mayEstablishBranchFlags(program, target.Architecture()) {
			fastProgram, selectErr := selectProgramExports(s.ops, s.leaves, s.arity, s.result, nil, target, e.scratch)
			if selectErr == nil {
				bindVariableHomes(&fastProgram, e.source, s.start, e.homePlan, e.facts)
				selectErr = bridgeManagedCCalls(&fastProgram, options, kind)
			}
			if selectErr == nil {
				fastProgram.PreferredRegisters = program.PreferredRegisters
				fastEmitter := leaseFragmentEmitter()
				allocations++
				fastPhysical, fastWorkspace, fastWritten, allocErr := allocateBranchBlock(fastProgram, target, fastEmitter, allocationBuffers)
				if allocErr == nil && fastWorkspace == 0 &&
					flagOnlyBranch(e.steps[id+1].predicate, fastProgram, fastPhysical.flags, target.Architecture(), options.Harden) {
					program, physical, workspace, fragmentWritten = fastProgram, fastPhysical, fastWorkspace, fastWritten
					fastFlags = true
				}
			}
		}
		if !fastFlags {
			allocations++
			fragmentEmitter := leaseFragmentEmitter()
			if step.segment != nil && (step.segment.bare || program.DirectExports ||
				target.Architecture() == "386" && program.Values[program.Result].Words == 2) {
				physical, workspace, fragmentWritten, err = allocateBranchBlock(program, target, fragmentEmitter, allocationBuffers)
			} else {
				physical, workspace, fragmentWritten, err = allocateBlock(program, target, fragmentEmitter, allocationBuffers)
			}
		}
		if directJump && (err != nil ||
			!edgeCanUseHomes(e.steps[id+1].args, e.labelRegisters[e.steps[id+1].label], e.labelSlots[e.steps[id+1].label],
				program.Exports, physical.exportHomes, target.Registers().AppendTo(edgeRegisterBuffer[:0]), target.ScratchRegister())) {
			s := step.segment
			program, err = selectProgramExports(s.ops, s.leaves, s.arity, s.result, s.exports, target, e.scratch)
			if err != nil {
				return emissionArtifact{}, abi.EmissionReport{}, err
			}
			bindVariableHomes(&program, e.source, s.start, e.homePlan, e.facts)
			if err = bridgeManagedCCalls(&program, options, kind); err != nil {
				return emissionArtifact{}, abi.EmissionReport{}, err
			}
			program.ReservedSlots = e.reservedSlots
			fragmentEmitter := leaseFragmentEmitter()
			allocations++
			physical, workspace, fragmentWritten, err = allocateBranchBlock(program, target, fragmentEmitter, allocationBuffers)
		}
		if directBranch && (err != nil ||
			(options.Harden || !predicateFullyDecidedByFlags(e.steps[id+1].predicate, physical.flags, target.Architecture())) &&
				!predicateCanUseRegisterHomes(e.steps[id+1].predicate, program.Exports, physical.exportHomes) ||
			!edgeCanUseHomes(e.steps[id+1].args, e.labelRegisters[e.steps[id+1].label], e.labelSlots[e.steps[id+1].label],
				program.Exports, physical.exportHomes, target.Registers().AppendTo(edgeRegisterBuffer[:0]), target.ScratchRegister()) ||
			!edgeCanUseHomes(e.steps[id+1].otherArgs, e.labelRegisters[e.steps[id+1].other], e.labelSlots[e.steps[id+1].other],
				program.Exports, physical.exportHomes, target.Registers().AppendTo(edgeRegisterBuffer[:0]), target.ScratchRegister())) {
			s := step.segment
			program, err = selectProgramExports(s.ops, s.leaves, s.arity, s.result, s.exports, target, e.scratch)
			if err != nil {
				return emissionArtifact{}, abi.EmissionReport{}, err
			}
			bindVariableHomes(&program, e.source, s.start, e.homePlan, e.facts)
			if err = bridgeManagedCCalls(&program, options, kind); err != nil {
				return emissionArtifact{}, abi.EmissionReport{}, err
			}
			program.ReservedSlots = e.reservedSlots
			fragmentEmitter := leaseFragmentEmitter()
			allocations++
			physical, workspace, fragmentWritten, err = allocateBranchBlock(program, target, fragmentEmitter, allocationBuffers)
		}
		if err != nil {
			return emissionArtifact{}, abi.EmissionReport{}, fmt.Errorf("allocate control fragment %d: %w", id, err)
		}
		report.LoweringStats.Allocations += allocations
		report.LoweringStats.Retries += max(0, allocations-1)
		if workspace != 0 {
			if options.Stack == abi.StackPlatform {
				return emissionArtifact{}, abi.EmissionReport{}, fmt.Errorf("control fragment %d needs %d workspace bytes with platform stack storage: %w", id, workspace, ErrBackendLimit)
			}
			report.WorkspaceBytes = max(report.WorkspaceBytes, workspace)
		}
		results[id] = program.Result
		resultRegisters[id] = physical.resultPhysical
		resultWords[id] = physical.resultWords
		resultWordCounts[id] = physical.resultWordCount
		flagReports[id] = physical.flags
		if program.DirectExports {
			for n, home := range physical.exportHomes {
				if home.Slot >= 0 {
					maxLogicalSlots = max(maxLogicalSlots, home.Slot+max(1, int(program.Values[program.Exports[n]].Words)))
				}
			}
			exportHomes[id] = make(map[int]exportLocation, len(program.Exports))
			for i, value := range program.Exports {
				exportHomes[id][value] = physical.exportHomes[i]
			}
		} else if len(program.Exports) != 0 {
			// Only reads follow; fragments without exports keep a nil map.
			exportSlots[id] = make(map[int]int, len(program.Exports))
			for i, value := range program.Exports {
				exportSlots[id][value] = physical.exportSlots[i]
			}
		}
		if step.segment != nil && step.segment.bare && id+1 < len(e.steps) && e.steps[id+1].kind == stepBranch && len(program.Exports) == 0 && e.steps[id+1].predicate != nil && e.steps[id+1].predicate.Kind == ir.PredicateCompare && !e.steps[id+1].predicate.WithValue && e.steps[id+1].predicate.Immediate == 0 && exportHomes[id] == nil && physical.resultWordCount == 1 && !backend.IsVectorRegister(physical.resultPhysical) {
			exportHomes[id] = map[int]exportLocation{program.Result: {Register: int(physical.resultPhysical), Slot: -1}}
		}
		if physical.stackBytes > frame {
			frame = physical.stackBytes
		}
		for reg, modified := range fragmentWritten {
			if modified {
				written[reg] = true
			}
		}
		emitter.(*physicalEmitter).callAreaBytes = max(emitter.(*physicalEmitter).callAreaBytes, physical.callAreaBytes)
		fragments[id] = carve(&fragmentArena, len(physical.insns))
		for _, located := range physical.insns {
			fragments[id] = append(fragments[id], located.ins)
			if located.ins.Op == pSpill || located.ins.Op == pReload || located.ins.Op == pSaveLink || located.ins.Op == pRestoreLink || located.ins.Op == pSaveCFloat || located.ins.Op == pRestoreCFloat {
				if located.ins.Slot+1 > maxLogicalSlots {
					maxLogicalSlots = located.ins.Slot + 1
				}
			}
		}
		maxLogicalSlots = max(maxLogicalSlots, referencedSpillSlots(fragments[id], target.RegisterBits()))
		for _, slot := range physical.exportSlots {
			if slot+1 > maxLogicalSlots {
				maxLogicalSlots = slot + 1
			}
		}
	}
	stream := emitter.(*physicalEmitter)
	if target.Architecture() == "386" && options.ABI == abi.Go {
		entry, err := entryLayout(target, argumentLocationWords(report.ArgumentLocations))
		if err != nil {
			return emissionArtifact{}, abi.EmissionReport{}, err
		}
		stream.goReturnOffset = entry.result.Words[0].StackOffset
	}
	if target.Architecture() == "386" && hasCall {
		frame = stream.frameBytes(maxLogicalSlots, true)
	}
	if options.Stack == abi.StackPlatform && maxLogicalSlots > 0 {
		frame = max(frame, stream.frameBytes(maxLogicalSlots, hasCall))
	}
	// Edge temporaries must never borrow a variable home or the workspace
	// base. The same candidate list is used for planning and emission.
	edgeRegisters := []physReg{}
	var availableScanBuffer [64]physReg
	for _, r := range target.Registers().AppendTo(availableScanBuffer[:0]) {
		if (e.homePlan == nil || !e.homePlan.Reserved[r]) &&
			(options.Stack != abi.StackNoUse || r != target.WorkspaceRegister()) {
			edgeRegisters = append(edgeRegisters, r)
		}
	}
	edgeScratch := target.ScratchRegister()
	if options.Stack == abi.StackNoUse && edgeScratch == target.WorkspaceRegister() {
		if len(edgeRegisters) == 0 {
			return emissionArtifact{}, abi.EmissionReport{}, fmt.Errorf("control edges need a register outside the workspace and variable homes: %w", ErrBackendLimit)
		}
		edgeScratch = edgeRegisters[0]
	}
	edgeTempBase := maxLogicalSlots
	edgeTempSlots := 0
	if wideReturn {
		edgeTempSlots = 1
	}
	type directEdge struct {
		label ir.Label
		args  []int
	}
	for id, step := range e.steps {
		if onlineEdges != nil {
			break // Online's moves use its own slots
		}
		if step.kind == stepJump || step.kind == stepBranch {
			for _, edge := range []struct {
				label ir.Label
				args  []int
			}{{step.label, step.args}, {step.other, step.otherArgs}} {
				if !e.edgeHasWide(edge.label) {
					continue
				}
				words := 0
				for i := range edge.args {
					words += int(e.labelWords[edge.label][i])
				}
				if words > edgeTempSlots {
					edgeTempSlots = words
				}
			}
		}
		if id == 0 || exportHomes[id-1] == nil {
			continue
		}
		var edgeBuffer [2]directEdge
		edges := edgeBuffer[:0]
		switch step.kind {
		case stepJump:
			edges = append(edges, directEdge{step.label, step.args})
		case stepBranch:
			edges = append(edges, directEdge{step.label, step.args}, directEdge{step.other, step.otherArgs})
		default:
			continue
		}
		for _, edge := range edges {
			plan, err := planEdgeTransfer(edge.args, e.labelRegisters[edge.label], e.labelSlots[edge.label],
				exportHomes[id-1], edgeRegisters, edgeScratch)
			if err != nil {
				return emissionArtifact{}, abi.EmissionReport{}, err
			}
			if plan.slots > edgeTempSlots {
				edgeTempSlots = plan.slots
			}
		}
	}
	edgeScratchSlot := edgeTempBase + edgeTempSlots
	needsEdgeScratchSave := onlineEdges == nil && (target.RegisterBits() == 32 || options.Stack == abi.StackNoUse)
	for _, regs := range e.labelRegisters {
		for _, r := range regs {
			needsEdgeScratchSave = needsEdgeScratchSave || onlineEdges == nil && r >= 0 && backend.IsVectorRegister(physReg(r))
		}
	}
	for id, step := range e.steps {
		if onlineEdges == nil && (step.kind == stepJump || step.kind == stepBranch) && id > 0 && exportHomes[id-1] == nil && len(step.args)+len(step.otherArgs) > 0 {
			needsEdgeScratchSave = true
		}
	}
	if needsEdgeScratchSave {
		edgeTempSlots++
	}
	if options.Harden {
		stream.hardenSlots = edgeTempBase + edgeTempSlots
		stream.hardenRegisters = hardenRegisters(target.Architecture())
		edgeTempSlots += len(stream.hardenRegisters)
		stream.hardenComparisons = true
		for _, r := range stream.hardenRegisters {
			written[r] = true
		}
	}
	if edgeTempSlots != 0 {
		limit := stream.spillSlotLimit()
		if edgeTempBase+edgeTempSlots > limit {
			return emissionArtifact{}, abi.EmissionReport{}, spillStorageLimitError(edgeTempBase+edgeTempSlots, e.reservedSlots, limit)
		}
		if required := stream.frameBytes(edgeTempBase+edgeTempSlots, hasCall); options.Stack == abi.StackPlatform && required > frame {
			frame = required
		}
	}
	if hasCall && target.Architecture() == "amd64" {
		// Every fragment shares one frame. Align the shared frame for calls,
		// including when another fragment has the largest spill area.
		frame = ((frame + 7) &^ 15) + 8
	}
	entrySaveBase := edgeTempBase + edgeTempSlots
	for r, preserve := range e.preserve {
		written[r] = written[r] || preserve
	}
	planAllocatedPreservation(target, stream, &written)
	stream.planEntryPreservation(hasCall)
	resources, endSlots := stream.entryResourcePlan(entrySaveBase + len(stream.entrySaves) + len(stream.entryFloatSaves))
	if endSlots > stream.spillSlotLimit() {
		return emissionArtifact{}, abi.EmissionReport{}, spillStorageLimitError(endSlots, e.reservedSlots, stream.spillSlotLimit())
	}
	if endSlots > entrySaveBase {
		if required := stream.frameBytes(endSlots, hasCall); options.Stack == abi.StackPlatform && required > frame {
			frame = required
		}
	}
	if options.Stack == abi.StackNoUse && endSlots > 0 {
		report.WorkspaceBytes = max(report.WorkspaceBytes, endSlots*int(target.RegisterBits()/8))
		report.WorkspaceRegister = target.RegisterName(target.WorkspaceRegister())
		report.SpillStorage = abi.SpillWorkspace
	}
	if hasCall {
		report.CallStackBytes = stream.callBytes(true)
	}
	if options.Stack == abi.StackNoUse && (frame != 0 || report.CallStackBytes != 0) {
		return emissionArtifact{}, abi.EmissionReport{}, fmt.Errorf("control flow requires a %d-byte platform stack frame: %w", frame+report.CallStackBytes, ErrStackRequired)
	}
	if frame+report.CallStackBytes > options.StackBytesAvailable {
		return emissionArtifact{}, abi.EmissionReport{}, ErrStackRequired
	}
	if frame != 0 {
		stream.append(physicalInsn{Op: pFrameEnter, Imm: uint64(frame), CallAreaBytes: stream.callAreaBytes})
	}
	for i, reg := range stream.entrySaves {
		stream.append(physicalInsn{Op: pSpill, A: reg, Slot: entrySaveBase + i})
	}
	for i, reg := range stream.entryFloatSaves {
		stream.append(physicalInsn{Op: pSaveCFloat, A: reg, Slot: entrySaveBase + len(stream.entrySaves) + i})
	}
	for _, save := range resources {
		stream.append(physicalInsn{Op: save.Save, A: save.Register, Slot: save.Slot})
	}
	labels := resetBufferTracked(&buffers.positions, &buffers.history.positions, len(e.labels))
	bound := append(appendBufferTracked(&buffers.bound, &buffers.history.bound, len(e.labels)), e.labels...)
	defer func() { buffers.positions = labels; buffers.bound = bound }()
	newInternalLabel := func() ir.Label {
		label := ir.Label(len(labels))
		labels = append(labels, 0)
		bound = append(bound, false)
		return label
	}
	bindInternal := func(label ir.Label) {
		labels[label], bound[label] = stream.machineStream().PC(), true
		if options.Harden {
			stream.append(barrier)
		}
	}
	currentSlots := map[int]int(nil)
	currentHomes := map[int]exportLocation(nil)
	currentPairs := map[int][2]physReg(nil)
	currentResult := -1
	currentResultRegister := target.ResultRegister()
	currentFlags := flagState{}
	currentOps := []instruction(nil)
	emitEdgeArgs := func(label ir.Label, ids []int) error {
		if len(ids) == 0 {
			return nil
		}
		if label < 0 || int(label) >= len(e.labelSlots) || len(ids) != len(e.labelSlots[label]) {
			return ErrInvalidFunction
		}
		if currentHomes != nil {
			plan, err := planEdgeTransfer(ids, e.labelRegisters[label], e.labelSlots[label],
				currentHomes, edgeRegisters, edgeScratch)
			if err != nil || plan.slots > edgeTempSlots {
				return fmt.Errorf("edge to label %d needs %d temporary slots (available %d; transfer error %v): %w", label, plan.slots, edgeTempSlots, err, ErrBackendLimit)
			}
			type edgeMove struct {
				bits     uint8
				nativeFP bool
				src, dst physReg
				slot     int
			}
			type deferredMove struct {
				home exportLocation
				dst  physReg
			}
			moves := make([]edgeMove, 0, len(ids))
			deferred := make([]deferredMove, 0, len(ids))
			sources := make(map[int]exportLocation, len(ids))
			for _, id := range ids {
				home, ok := currentHomes[id]
				if !ok {
					return ErrInvalidFunction
				}
				if plan.scratchSave >= 0 && home.Register == int(plan.scratch) {
					home.Register, home.Slot = -1, edgeTempBase+plan.scratchSave
				}
				sources[id] = home
			}
			if plan.scratchSave >= 0 {
				emitHomeSpill(stream, plan.scratch, edgeTempBase+plan.scratchSave)
			}
			for relative, sourceSlot := range plan.snapshotSources {
				emitHomeReload(stream, plan.scratch, sourceSlot)
				emitHomeSpill(stream, plan.scratch, edgeTempBase+relative)
				written[plan.scratch] = true
				for id, home := range sources {
					if home.Register < 0 && home.Slot == sourceSlot {
						home.Slot = edgeTempBase + relative
						sources[id] = home
					}
				}
			}
			// Store overflow parameters before moving register destinations so
			// every register source still contains its outgoing value.
			for i, id := range ids {
				if e.labelRegisters[label][i] >= 0 {
					continue
				}
				home := sources[id]
				destination := e.labelSlots[label][i]
				if home.Register >= 0 {
					emitHomeSpill(stream, physReg(home.Register), destination, home.Bits)
				} else if home.Slot == destination {
					continue
				} else if home.Slot >= 0 {
					emitHomeReload(stream, plan.scratch, home.Slot)
					emitHomeSpill(stream, plan.scratch, destination)
					written[plan.scratch] = true
				} else if home.Constant {
					emitHomeConstant(stream, target, plan.scratch, home.Immediate)
					emitHomeSpill(stream, plan.scratch, destination)
					written[plan.scratch] = true
				} else {
					return fmt.Errorf("edge to label %d has no source for value %d: %w", label, id, ErrBackendLimit)
				}
			}
			for i, id := range ids {
				home, ok := sources[id]
				if !ok {
					return ErrInvalidFunction
				}
				if e.labelRegisters[label][i] < 0 {
					continue
				}
				dst := physReg(e.labelRegisters[label][i])
				if home.Register >= 0 {
					if src := physReg(home.Register); src != dst {
						moves = append(moves, edgeMove{src: src, dst: dst, slot: -1, bits: e.labelWidth(label, i), nativeFP: backend.IsVectorRegister(src)})
					}
				} else {
					deferred = append(deferred, deferredMove{home: home, dst: dst})
				}
			}
			for len(moves) != 0 {
				ready := -1
				for i, move := range moves {
					blocked := false
					for j, other := range moves {
						if i != j && other.slot < 0 && other.src == move.dst {
							blocked = true
							break
						}
					}
					if !blocked {
						ready = i
						break
					}
				}
				if ready < 0 {
					src := moves[0].src
					temporary := physReg(0)
					hasTemporary := false
					occupied := make(map[physReg]bool)
					for _, register := range e.labelRegisters[label] {
						if register >= 0 {
							occupied[physReg(register)] = true
						}
					}
					for _, move := range moves {
						occupied[move.dst] = true
						if move.slot < 0 {
							occupied[move.src] = true
						}
					}
					for _, candidate := range edgeRegisters {
						if !occupied[candidate] {
							temporary, hasTemporary = candidate, true
							break
						}
					}
					if target.Architecture() == "386" && backend.IsVectorRegister(src) {
						temporary, hasTemporary = backend.VectorRegister(0), true
					}
					if hasTemporary {
						emitHomeMove(stream, 64, temporary, src)
						written[temporary] = true
					} else {
						if plan.registerCycle < 0 {
							return fmt.Errorf("edge to label %d needs a register or spill slot for a register cycle: %w", label, ErrBackendLimit)
						}
						emitHomeSpill(stream, src, edgeTempBase+plan.registerCycle)
					}
					for i := range moves {
						if moves[i].slot < 0 && moves[i].src == src {
							if hasTemporary {
								moves[i].src = temporary
							} else {
								moves[i].slot = edgeTempBase + plan.registerCycle
							}
						}
					}
					continue
				}
				move := moves[ready]
				if move.slot >= 0 {
					if !backend.IsVectorRegister(move.dst) || move.nativeFP {
						emitHomeReload(stream, move.dst, move.slot, move.bits)
					} else {
						emitHomeSpill(stream, plan.scratch, edgeScratchSlot)
						emitHomeReload(stream, plan.scratch, move.slot)
						emitHomeMove(stream, move.bits, move.dst, plan.scratch)
						emitHomeReload(stream, plan.scratch, edgeScratchSlot)
					}
				} else {
					bits := move.bits
					if move.nativeFP && backend.IsVectorRegister(move.dst) {
						bits = 64
					}
					emitHomeMove(stream, bits, move.dst, move.src)
				}
				written[move.dst] = true
				moves = append(moves[:ready], moves[ready+1:]...)
			}
			for _, move := range deferred {
				if move.home.Slot >= 0 {
					if backend.IsVectorRegister(move.dst) && !move.home.Float {
						emitHomeSpill(stream, plan.scratch, edgeScratchSlot)
						emitHomeReload(stream, plan.scratch, move.home.Slot)
						emitHomeMove(stream, move.home.Bits, move.dst, plan.scratch)
						emitHomeReload(stream, plan.scratch, edgeScratchSlot)
					} else {
						emitHomeReload(stream, move.dst, move.home.Slot, move.home.Bits)
					}
				} else if move.home.Constant {
					emitTypedHomeConstant(stream, target, move.home.Bits, move.dst, move.home.Immediate)
				} else {
					return ErrInvalidFunction
				}
				written[move.dst] = true
			}
			return nil
		}
		reg := edgeScratch
		// A borrowed GPR can already hold a completed label destination.
		scratchLive := false
		withScratch := func(f func()) {
			if scratchLive {
				emitHomeSpill(stream, reg, edgeScratchSlot)
			}
			f()
			if scratchLive {
				emitHomeReload(stream, reg, edgeScratchSlot)
			}
		}
		if e.edgeHasWide(label) {
			offset := 0
			for i, id := range ids {
				slot, ok := currentSlots[id]
				if !ok {
					return ErrInvalidFunction
				}
				for word := 0; word < int(e.labelWords[label][i]); word++ {
					withScratch(func() {
						emitHomeReload(stream, reg, slot+word)
						emitHomeSpill(stream, reg, edgeTempBase+offset+word)
					})
				}
				offset += int(e.labelWords[label][i])
			}
			offset = 0
			for i := range ids {
				if destination := e.labelRegisters[label][i]; destination >= 0 && backend.IsVectorRegister(physReg(destination)) {
					emitHomeReload(stream, physReg(destination), edgeTempBase+offset, e.labelWidth(label, i))
					written[physReg(destination)] = true
					offset += int(e.labelWords[label][i])
					continue
				}
				for word := 0; word < int(e.labelWords[label][i]); word++ {
					if destination := e.labelRegisters[label][i]; destination >= 0 {
						emitHomeReload(stream, physReg(destination), edgeTempBase+offset+word)
						written[physReg(destination)] = true
						scratchLive = scratchLive || physReg(destination) == reg
					} else {
						withScratch(func() {
							emitHomeReload(stream, reg, edgeTempBase+offset+word)
							emitHomeSpill(stream, reg, e.labelSlots[label][i]+word)
						})
					}
				}
				offset += int(e.labelWords[label][i])
			}
			written[reg] = true
			return nil
		}
		for i, id := range ids {
			slot, ok := currentSlots[id]
			if !ok {
				return ErrInvalidFunction
			}
			if destination := e.labelRegisters[label][i]; destination >= 0 {
				if target.Architecture() == "386" && backend.IsVectorRegister(physReg(destination)) {
					emitHomeReload(stream, physReg(destination), slot, e.labelWidth(label, i))
				} else if backend.IsVectorRegister(physReg(destination)) {
					withScratch(func() {
						emitHomeReload(stream, reg, slot)
						emitHomeMove(stream, e.labelWidth(label, i), physReg(destination), reg)
					})
				} else {
					emitHomeReload(stream, physReg(destination), slot)
				}
				written[physReg(destination)] = true
				scratchLive = scratchLive || physReg(destination) == reg
			} else {
				withScratch(func() {
					emitHomeReload(stream, reg, slot)
					emitHomeSpill(stream, reg, e.labelSlots[label][i])
				})
				written[reg] = true
			}
		}
		return nil
	}
	emitComparison := func(node *ir.PredicateExpr, yes, no, fallthroughLabel ir.Label) error {
		switch node.Kind {
		case ir.PredicateCompare:
			// The hardening clamp consumes the bounds comparison's flags.
			// Addition carry facts can prove the branch with the opposite ARM
			// carry polarity; keep an explicit comparison in hardened blocks.
			if plan := flagPlanForPredicate(node, currentFlags, target.Architecture()); plan.valid && !options.Harden {
				// Prefer a single conditional edge when the other destination
				// is already the physical fallthrough (also for Not predicates).
				if len(plan.yes) == 1 && len(plan.no) == 0 && yes == fallthroughLabel {
					stream.append(physicalInsn{Op: pJumpIf, Condition: plan.yes[0] ^ 1, Imm: uint64(no)})
					return nil
				}
				if len(plan.no) == 1 && len(plan.yes) == 0 && no == fallthroughLabel {
					stream.append(physicalInsn{Op: pJumpIf, Condition: plan.no[0] ^ 1, Imm: uint64(yes)})
					return nil
				}
				for _, condition := range plan.yes {
					stream.append(physicalInsn{Op: pJumpIf, Condition: condition, Imm: uint64(yes)})
				}
				for _, condition := range plan.no {
					stream.append(physicalInsn{Op: pJumpIf, Condition: condition, Imm: uint64(no)})
				}
				final := no
				if len(plan.no) != 0 {
					final = yes
				}
				if final != fallthroughLabel {
					stream.append(physicalInsn{Op: pJump, Imm: uint64(final)})
				}
				return nil
			}
			if _, ok := currentPairs[node.Value]; ok {
				return emitPairedRegisters(stream, node, currentPairs, yes, no, fallthroughLabel)
			}
			if target.RegisterBits() == 32 && instructionBits(currentOps[node.Value]) == 64 {
				return emitPairedPredicate(stream, node, currentSlots, edgeScratchSlot, yes, no, fallthroughLabel, &written)
			}
			if !node.WithValue && node.Immediate == 0 &&
				(node.Condition == ir.BranchEqual || node.Condition == ir.BranchNotEqual) &&
				node.Value >= 0 && node.Value < len(currentOps) && currentHomes != nil {
				if home, ok := currentHomes[node.Value]; ok && home.Register >= 0 && !backend.IsVectorRegister(physReg(home.Register)) {
					width := instructionBits(currentOps[node.Value])
					reg := physReg(home.Register)
					switch target.Architecture() {
					case "amd64":
						if width == 64 && reg == 1 {
							branchTo, other := yes, no
							if node.Condition == ir.BranchNotEqual {
								branchTo, other = no, yes
							}
							stream.append(physicalInsn{Op: pBranchZero, A: reg, Bits: 64, Condition: ir.BranchEqual, Imm: uint64(branchTo), Long: true})
							if other != fallthroughLabel {
								stream.append(physicalInsn{Op: pJump, Imm: uint64(other)})
							}
							return nil
						}
					case "arm64":
						if width == 32 || width == 64 {
							branch := physicalInsn{Op: pBranchZero, A: reg, Bits: width, Condition: node.Condition, Imm: uint64(yes), Long: true}
							operation := currentOps[node.Value]
							if operation.Op == opAnd {
								constant := -1
								for _, operand := range []int{operation.A, operation.B} {
									if operand >= 0 && operand < len(currentOps) && currentOps[operand].Op == opConst && currentOps[operand].Symbol == "" {
										constant = operand
										break
									}
								}
								if constant >= 0 {
									mask := currentOps[constant].Imm
									if width == 32 {
										mask &= 0xffffffff
									}
									if bits.OnesCount64(mask) == 1 {
										branch.Op, branch.Disp = pBranchBit, uint64(bits.TrailingZeros64(mask))
									}
								}
							}
							stream.append(branch)
							if no != fallthroughLabel {
								stream.append(physicalInsn{Op: pJump, Imm: uint64(no)})
							}
							return nil
						}
					}
				}
			}
			// A compare overwrites CR0 but preserves XER on PPC. Other
			// targets overwrite their arithmetic carry along with sign/zero.
			if target.Architecture() == "ppc64le" {
				currentFlags.Sign, currentFlags.Zero = machine.FlagFact{}, machine.FlagFact{}
			} else {
				currentFlags = flagState{}
			}
			if currentHomes != nil {
				// Integer comparisons read only general-purpose registers;
				// predicateCanUseRegisterHomes keeps FP homes off this path.
				left, ok := currentHomes[node.Value]
				if !ok || left.Register < 0 || backend.IsVectorRegister(physReg(left.Register)) {
					return ErrInvalidFunction
				}
				if node.WithValue {
					right, ok := currentHomes[node.Other]
					if !ok || right.Register < 0 || backend.IsVectorRegister(physReg(right.Register)) {
						return ErrInvalidFunction
					}
					stream.append(physicalInsn{Op: pCompareRegister, Bits: target.RegisterBits(), Comparison: machine.ComparisonFor(node.Condition), A: physReg(left.Register), B: physReg(right.Register)})
				} else {
					if node.Immediate < -4095 || node.Immediate > 4095 {
						capability, ok := target.(interface {
							CanCompareImmediate(uint8, machine.ComparisonMode, uint64) bool
						})
						if !ok || !capability.CanCompareImmediate(target.RegisterBits(), machine.ComparisonFor(node.Condition), uint64(node.Immediate)) {
							return fmt.Errorf("%s predicate cannot compare immediate %d in its register home: %w", target.Architecture(), node.Immediate, ErrBackendLimit)
						}
					}
					stream.append(physicalInsn{Op: pCompareImmediate, Bits: target.RegisterBits(), Comparison: machine.ComparisonFor(node.Condition), A: physReg(left.Register), Imm: uint64(node.Immediate)})
				}
				stream.append(physicalInsn{Op: pJumpIf, Condition: node.Condition, Imm: uint64(yes)})
				if no != fallthroughLabel {
					stream.append(physicalInsn{Op: pJump, Imm: uint64(no)})
				}
				return nil
			}
			left := currentResultRegister
			if slot, ok := currentSlots[node.Value]; ok {
				left = target.ResultRegister()
				stream.append(physicalInsn{Op: pReload, A: left, Slot: slot})
				written[left] = true
			} else if node.Value != currentResult {
				return ErrInvalidFunction
			}
			right := edgeScratch
			if right == left {
				for _, r := range edgeRegisters {
					if r != left {
						right = r
						break
					}
				}
			}
			borrowed := false
			if right == left {
				var availableScanBuffer [64]physReg
				for _, candidate := range target.Registers().AppendTo(availableScanBuffer[:0]) {
					if candidate != left && candidate != target.WorkspaceRegister() {
						right, borrowed = candidate, true
						break
					}
				}
				if right == left {
					return fmt.Errorf("predicate needs a second non-workspace register: %w", ErrBackendLimit)
				}
			}
			if borrowed {
				emitHomeSpill(stream, right, edgeScratchSlot)
			}
			if node.WithValue {
				if slot, ok := currentSlots[node.Other]; ok {
					stream.append(physicalInsn{Op: pReload, A: right, Slot: slot})
					written[right] = true
				} else if node.Other == currentResult {
					right = left
				} else {
					return ErrInvalidFunction
				}
				stream.append(physicalInsn{Op: pCompareRegister, Bits: target.RegisterBits(), Comparison: machine.ComparisonFor(node.Condition), A: left, B: right})
			} else if node.Immediate >= -4095 && node.Immediate <= 4095 {
				stream.append(physicalInsn{Op: pCompareImmediate, Bits: target.RegisterBits(), Comparison: machine.ComparisonFor(node.Condition), A: left, Imm: uint64(node.Immediate)})
			} else {
				stream.append(physicalInsn{Op: pConst, A: right, Imm: uint64(node.Immediate)})
				written[right] = true
				stream.append(physicalInsn{Op: pCompareRegister, Bits: target.RegisterBits(), Comparison: machine.ComparisonFor(node.Condition), A: left, B: right})
			}
			if borrowed {
				emitHomeReload(stream, right, edgeScratchSlot)
			}
			stream.append(physicalInsn{Op: pJumpIf, Condition: node.Condition, Imm: uint64(yes)})
			if no != fallthroughLabel {
				stream.append(physicalInsn{Op: pJump, Imm: uint64(no)})
			}
			return nil
		default:
			return ErrInvalidFunction
		}
	}
	// Keep the recursive short-circuit order explicitly: emit left, bind its
	// continuation, then emit right. Flags evolve in the same emission order.
	emitPredicate := func(root *ir.PredicateExpr, yes, no, fallthroughLabel ir.Label) error {
		if root == nil {
			return ErrInvalidFunction
		}
		type task struct {
			node                      *ir.PredicateExpr
			yes, no, fallthroughLabel ir.Label
			bind                      bool
		}
		work := []task{{node: root, yes: yes, no: no, fallthroughLabel: fallthroughLabel}}
		for len(work) != 0 {
			f := work[len(work)-1]
			work = work[:len(work)-1]
			if f.bind {
				bindInternal(f.yes)
				continue
			}
			if f.node == nil {
				return ErrInvalidFunction
			}
			node := f.node
			switch node.Kind {
			case ir.PredicateAnd, ir.PredicateOr:
				middle := newInternalLabel()
				leftYes, leftNo := middle, f.no
				if node.Kind == ir.PredicateOr {
					leftYes, leftNo = f.yes, middle
				}
				work = append(work, task{node: node.Right, yes: f.yes, no: f.no, fallthroughLabel: f.fallthroughLabel}, task{yes: middle, bind: true}, task{node: node.Left, yes: leftYes, no: leftNo, fallthroughLabel: middle})
			case ir.PredicateNot:
				work = append(work, task{node: node.Left, yes: f.no, no: f.yes, fallthroughLabel: f.fallthroughLabel})
			case ir.PredicateCompare:
				if err := emitComparison(node, f.yes, f.no, f.fallthroughLabel); err != nil {
					return err
				}
			default:
				return ErrInvalidFunction
			}
		}
		return nil
	}
	// edgeMoves emits the moves of side 0 (to label) or 1 (to other) of the
	// edges of step id.
	edgeMoves := func(id, side int) error {
		if onlineEdges != nil {
			for _, ins := range onlineEdges[id][side] {
				stream.append(ins)
			}
			return nil
		}
		step := e.steps[id]
		if side == 0 {
			return emitEdgeArgs(step.label, step.args)
		}
		return emitEdgeArgs(step.other, step.otherArgs)
	}
	for id, step := range e.steps {
		switch step.kind {
		case stepLabel:
			labels[step.label] = stream.machineStream().PC()
			if options.Harden {
				stream.append(barrier)
			}
		case stepBuilder:
			if step.segment != nil {
				currentOps = step.segment.ops
			} else {
				currentOps = step.fragment.Instructions
			}
			currentSlots, currentResult = exportSlots[id], results[id]
			currentHomes = exportHomes[id]
			currentPairs = nil
			if onlinePairs != nil {
				currentPairs = onlinePairs[id]
			}
			currentResultRegister, currentFlags = resultRegisters[id], flagReports[id]
			for _, ins := range fragments[id] {
				stream.append(ins)
			}
		case stepJump:
			if err := edgeMoves(id, 0); err != nil {
				return emissionArtifact{}, abi.EmissionReport{}, err
			}
			stream.append(physicalInsn{Op: pJump, Imm: uint64(step.label)})
		case stepBranch:
			if onlinePredicates != nil && onlinePredicates[id] != nil {
				step.predicate = onlinePredicates[id]
			}
			moves := len(step.args)+len(step.otherArgs) != 0
			if onlineEdges != nil {
				// A clamp runs on the success edge's own path.
				moves = len(onlineEdges[id][0])+len(onlineEdges[id][1]) != 0 || onlineClamps != nil && onlineClamps[id] >= 0
			}
			if moves {
				yesEdge, noEdge := newInternalLabel(), newInternalLabel()
				if err := emitPredicate(step.predicate, yesEdge, noEdge, -1); err != nil {
					return emissionArtifact{}, abi.EmissionReport{}, err
				}
				bindInternal(yesEdge)
				clamp := options.Harden && step.predicate != nil && step.predicate.Kind == ir.PredicateCompare && step.predicate.Condition == ir.BranchBelow && len(step.args) >= 2 && step.args[1] == step.predicate.Value
				if onlineEdges != nil {
					// Online's edge passes a copy of the index, in a register.
					clamp = onlineClamps != nil && onlineClamps[id] >= 0
				}
				if clamp {
					index := e.steps[id].predicate.Value
					home := exportLocation{Register: -1}
					slot := -1
					if onlineEdges != nil {
						home = exportLocation{Register: onlineClamps[id], Slot: -1}
					} else if currentHomes != nil {
						home = currentHomes[index]
						slot = home.Slot
					} else if at, ok := currentSlots[index]; ok {
						slot = at
					}
					if err := clampIndex(stream, target.Architecture(), instructionBits(currentOps[index]), home, slot); err != nil {
						return emissionArtifact{}, abi.EmissionReport{}, err
					}
				}
				if err := edgeMoves(id, 0); err != nil {
					return emissionArtifact{}, abi.EmissionReport{}, err
				}
				stream.append(physicalInsn{Op: pJump, Imm: uint64(step.label)})
				bindInternal(noEdge)
				if err := edgeMoves(id, 1); err != nil {
					return emissionArtifact{}, abi.EmissionReport{}, err
				}
				stream.append(physicalInsn{Op: pJump, Imm: uint64(step.other)})
				break
			}
			fallthroughLabel := ir.Label(-1)
			for next := id + 1; next < len(e.steps) && e.steps[next].kind == stepLabel; next++ {
				if e.steps[next].label == step.other {
					fallthroughLabel = step.other
				}
			}
			if step.predicate != nil {
				if err := emitPredicate(step.predicate, step.label, step.other, fallthroughLabel); err != nil {
					return emissionArtifact{}, abi.EmissionReport{}, err
				}
			} else {
				stream.append(physicalInsn{Op: pCompareImmediate, Bits: target.RegisterBits(), Comparison: machine.ComparisonFor(step.condition), A: target.ResultRegister(), Imm: uint64(step.comparison)})
				stream.append(physicalInsn{Op: pJumpIf, Condition: step.condition, Imm: uint64(step.label)})
				if step.other != fallthroughLabel {
					stream.append(physicalInsn{Op: pJump, Imm: uint64(step.other)})
				}
			}
		case stepExit:
			if wideReturn {
				if id == 0 || resultWordCounts[id-1] != 2 {
					return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
				}
				low, high := resultWords[id-1][0], resultWords[id-1][1]
				pair, _ := target.Integer128Result()
				abiLow, abiHigh := pair[0], pair[1]
				if low == abiHigh && high == abiLow {
					stream.append(physicalInsn{Op: pSpill, A: low, Slot: edgeTempBase})
					stream.append(physicalInsn{Op: pMove, A: abiHigh, B: high})
					stream.append(physicalInsn{Op: pReload, A: abiLow, Slot: edgeTempBase})
				} else if high == abiLow {
					stream.append(physicalInsn{Op: pMove, A: abiHigh, B: high})
					if low != abiLow {
						stream.append(physicalInsn{Op: pMove, A: abiLow, B: low})
					}
				} else {
					if low != abiLow {
						stream.append(physicalInsn{Op: pMove, A: abiLow, B: low})
					}
					if high != abiHigh {
						stream.append(physicalInsn{Op: pMove, A: abiHigh, B: high})
					}
				}
				written[abiLow], written[abiHigh] = true, true
			} else if target.Architecture() == "386" {
				if id > 0 && resultWordCounts[id-1] == 2 {
					low, high := resultWords[id-1][0], resultWords[id-1][1]
					if low == 2 && high == 0 {
						stream.append(physicalInsn{Op: pMove, A: target.ScratchRegister(), B: low})
						stream.append(physicalInsn{Op: pMove, A: 2, B: high})
						stream.append(physicalInsn{Op: pMove, A: 0, B: target.ScratchRegister()})
						written[target.ScratchRegister()] = true
					} else if low == 2 {
						stream.append(physicalInsn{Op: pMove, A: 0, B: low})
						if high != 2 {
							stream.append(physicalInsn{Op: pMove, A: 2, B: high})
						}
					} else {
						if high != 2 {
							stream.append(physicalInsn{Op: pMove, A: 2, B: high})
						}
						if low != 0 {
							stream.append(physicalInsn{Op: pMove, A: 0, B: low})
						}
					}
					written[0], written[2] = true, true
				} else {
					stream.append(physicalInsn{Op: pConst, A: 2, Imm: 0})
				}
				written[2] = true
			}
			for i := len(resources) - 1; i >= 0; i-- {
				save := resources[i]
				stream.append(physicalInsn{Op: save.Restore, A: save.Register, Slot: save.Slot})
			}
			for i := len(stream.entryFloatSaves) - 1; i >= 0; i-- {
				stream.append(physicalInsn{Op: pRestoreCFloat, A: stream.entryFloatSaves[i], Slot: entrySaveBase + len(stream.entrySaves) + i})
			}
			for i := len(stream.entrySaves) - 1; i >= 0; i-- {
				stream.append(physicalInsn{Op: pReload, A: stream.entrySaves[i], Slot: entrySaveBase + i})
			}
			if frame != 0 {
				stream.append(physicalInsn{Op: pFrameLeave, Imm: uint64(frame)})
			}
			if stream.goReturnOffset != 0 {
				stream.append(physicalInsn{Op: pStoreOffset, A: 4, B: 0, Disp: uint64(stream.goReturnOffset)})
				stream.append(physicalInsn{Op: pStoreOffset, A: 4, B: 2, Disp: uint64(stream.goReturnOffset + 4)})
			}
			stream.append(physicalInsn{Op: pReturn})
		}
	}
	var layoutErr error
	labels, layoutErr = compactControlStream(stream, labels, bound)
	if layoutErr != nil {
		return emissionArtifact{}, abi.EmissionReport{}, layoutErr
	}
	if fuser, ok := target.(machine.LoopFuser); ok && options.PreferCompactLoops {
		labels, bound, layoutErr = fuser.FuseLoops(stream.machineStream(), labels, bound)
		if layoutErr != nil {
			return emissionArtifact{}, abi.EmissionReport{}, layoutErr
		}
	}
	if elider, ok := target.(machine.FlagElider); ok && !options.keepFlagWrites() {
		labels, layoutErr = elider.ElideDeadFlags(stream.machineStream(), labels, bound)
		if layoutErr != nil {
			return emissionArtifact{}, abi.EmissionReport{}, layoutErr
		}
	}
	if stream.hardenErr != nil {
		return emissionArtifact{}, abi.EmissionReport{}, fmt.Errorf("hardening randomness: %w", stream.hardenErr)
	}
	labels, layoutErr = relaxControlLayout(stream.machineStream(), labels, bound, target)
	if layoutErr != nil {
		return emissionArtifact{}, abi.EmissionReport{}, layoutErr
	}
	resolved, err := machine.ResolveBranches(stream.machineStream(), labels, bound, target.BranchFits)
	if err != nil {
		if errors.Is(err, machine.ErrBranchOutOfRange) {
			return emissionArtifact{}, abi.EmissionReport{}, &LimitError{Kind: LimitBranchRange, Arch: target.Architecture(), Detail: "resolving control branches"}
		}
		return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
	}
	physical := physicalProgram{insns: make([]locatedInsn, 0, len(resolved)), codeBytes: stream.machineStream().PC(), stackBytes: frame}
	for _, located := range resolved {
		physical.insns = append(physical.insns, locatedInsn{ins: located.Instruction, pc: located.PC})
	}
	codeBytes := finishOrMeasureLiterals(&physical, kind)
	recordImplicitWrites(target, physical, &written)
	if missing := unsavedPreservedWrites(target, stream, &written); len(missing) != 0 {
		return emissionArtifact{}, abi.EmissionReport{}, unsavedPreservedError{missing}
	}
	artifact := emissionArtifact{physical: &physical, stackBytes: physical.stackBytes}
	if !kind.layout() {
		artifact, err = assembleProgram(physical, assembler)
		if err != nil {
			return emissionArtifact{}, abi.EmissionReport{}, err
		}
	}
	if kind == emitNative && options.ManagedCalls && target.ManagedFrameLimit() > 0 {
		artifact.goUnwind, err = goUnwindFromProgram(physical)
		if err != nil {
			return emissionArtifact{}, abi.EmissionReport{}, err
		}
	}
	report.CodeBytes = codeBytes
	report.StackBytes = frame
	report.FlagsClobbered = stream.flagsClobbered()
	if frame != 0 {
		report.SpillStorage = abi.SpillStack
	}
	var physicalScanBuffer [64]physReg
	for _, reg := range target.PhysicalRegisters().AppendTo(physicalScanBuffer[:0]) {
		if written[reg] {
			if savedByEntry(emitter, reg) {
				continue
			}
			if preserver, ok := target.(interface{ PreservesRegister(physReg) bool }); ok && preserver.PreservesRegister(reg) {
				continue
			}
			report.ClobberedRegisters = append(report.ClobberedRegisters, target.RegisterName(reg))
		}
	}
	report.ClobberedFPRegisters, err = fpClobbersFromPhysical(physical, emitter, target)
	report.FPStatusModified = fpStatusModifiedFromPhysical(physical)
	report.ClobberedSpecialRegisters = specialWritesFromPhysical(physical, target)
	if err != nil {
		return emissionArtifact{}, abi.EmissionReport{}, err
	}
	return artifact, report, nil
}

// reportControlEffects records in report the calls and memory accesses of
// a control fragment's program.
func reportControlEffects(report *abi.EmissionReport, hasCall *bool, program *machineProgram, options compileOptions) error {
	for _, ins := range program.Instructions {
		switch ins.Op {
		case mCallC:
			*hasCall = true
			report.HasFunctionCalls = true
			if ins.Effect == nil || ins.Effect.MayReadMemory {
				report.MayReadMemory = true
			}
			if ins.Effect == nil || ins.Effect.MayWriteMemory {
				report.MayWriteMemory = true
			}
		case mCallGo:
			if !options.ManagedCalls && options.ABI != abi.Go {
				return ErrExternalCallOnly
			}
			*hasCall = true
			report.HasFunctionCalls = true
			if ins.Effect == nil || ins.Effect.MayReadMemory {
				report.MayReadMemory = true
			}
			if ins.Effect == nil || ins.Effect.MayWriteMemory {
				report.MayWriteMemory = true
			}
		case mSyscall:
			report.HasSystemCalls, report.MayReadMemory, report.MayWriteMemory = true, true, true
		case mLoad64, mLoad8, mLoad16, mLoad32, mLoadAddress, mLoadOffset:
			report.ExplicitMemoryReads, report.MayReadMemory = true, true
		case mStore64, mStore8, mStore16, mStore32, mStoreAddress, mStoreOffset:
			report.ExplicitMemoryWrites, report.MayWriteMemory = true, true
		}
	}
	return nil
}

// compactControlStream preserves the root emitter's invalid-function error
// while machine owns physical branch threading and PC recomputation.
func compactControlStream(stream *physicalEmitter, labels []int, bound []bool) ([]int, error) {
	updated, err := machine.CompactBranches(stream.machineStream(), labels, bound)
	if err != nil {
		return nil, ErrInvalidFunction
	}
	return updated, nil
}

// Register comparisons only read their operands, so branch edge values can
// remain in their allocator homes until the selected edge starts moving them.
// The comparisons are integer instructions, so a value whose home is in the
// FP/vector bank (one a destination block reads as a float) must be staged
// as integer bits instead.
func predicateCanUseRegisterHomes(node *ir.PredicateExpr, exports []int, homes []exportLocation) bool {
	if node == nil || len(exports) != len(homes) {
		return false
	}
	home := func(value int) bool {
		for i, id := range exports {
			if id == value {
				return homes[i].Register >= 0 && !backend.IsVectorRegister(physReg(homes[i].Register))
			}
		}
		return false
	}
	for current, err := range predicateComparisons(node) {
		if err != nil {
			return false
		}
		if !home(current.Value) {
			return false
		}
		if current.WithValue {
			if !home(current.Other) {
				return false
			}
		} else if current.Immediate < -4095 || current.Immediate > 4095 {
			return false
		}
	}
	return true
}

// Keep the direct path when its parallel copies require no more temporary
// slots than staging all exported values would require.
func edgeCanUseHomes(ids []int, destinationRegisters, destinationSlots []int, exports []int,
	homes []exportLocation, registers []physReg, scratch physReg) bool {
	if len(ids) != len(destinationRegisters) || len(ids) != len(destinationSlots) || len(exports) != len(homes) {
		return false
	}
	byValue := make(map[int]exportLocation, len(exports))
	for i, value := range exports {
		byValue[value] = homes[i]
	}
	plan, err := planEdgeTransfer(ids, destinationRegisters, destinationSlots, byValue, registers, scratch)
	return err == nil && plan.slots <= len(exports)
}

// mayEstablishBranchFlags rules out trials on flagless ISAs and blocks whose
// selected operations cannot establish arithmetic facts. It is deliberately
// conservative: target expansion and physical homes remain checked afterwards.
func mayEstablishBranchFlags(p machineProgram, architecture string) bool {
	if architecture == "riscv64" || architecture == "loong64" {
		return false
	}
	for _, i := range p.Instructions {
		switch i.Op {
		case mBinary, mBinaryImmediate, mInc, mDec, mNarrow, mSignExtend, mWordCarry:
			return true
		}
	}
	return false
}
