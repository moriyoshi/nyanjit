package compile

import (
	"fmt"
	"sort"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

type physReg = backend.Register

// machineTarget owns selection and allocation rules. It never emits code.
type machineTarget interface {
	Architecture() string
	ABIFlavor() abi.Flavor
	PhysicalRegisters() backend.RegisterSet
	RegisterBits() uint8
	NativeByteOrder() ir.ByteOrder
	SupportsOrdinalSP() bool
	Registers() backend.RegisterSet
	ParamRegister(int) (physReg, bool)
	ResultRegister() physReg
	ScratchRegister() physReg
	WorkspaceRegister() physReg
	RegisterName(physReg) string
	CanScaled(uint64, uint64) bool
	CanBinaryImmediate(opcode, uint8, uint64) bool
	CanCombineScaled() bool
	CanMulAdd() bool
	CanFoldAddress(uint64, uint64) bool
	CanFoldBaseOffset(uint64) bool
	CanReuse(machineInstruction, int) bool
	CallTargetRegister() physReg
	ReturnAddress() machine.ReturnAddress
	Integer128Result() ([2]physReg, bool)
	ManagedFrameLimit() int
	BranchFits(machine.Instruction, int, int, int) bool
}

// machineOperations is the allocated instruction vocabulary shared by an
// emitter and its architecture-specific assemblers.
type machineOperations interface {
	constant(physReg, uint64)
	symbol(physReg, string)
	move(physReg, physReg)
	binary(opcode, uint8, physReg, physReg, physReg)
	floatBinary(opcode, uint8, physReg, physReg, physReg)
	floatUnary(opcode, uint8, physReg, physReg)
	floatFMA(uint8, physReg, physReg, physReg, physReg)
	binaryImmediate(opcode, uint8, physReg, physReg, uint64)
	scaled(physReg, physReg, physReg, uint64, uint64)
	selectValue(physReg, physReg, physReg, physReg)
	increment(physReg, physReg)
	decrement(physReg, physReg)
	narrow(physReg, physReg, uint8)
	signExtend(physReg, physReg, uint8)
	shift(opcode, uint8, physReg, physReg, uint8)
	carryBit(opcode, uint8, physReg, physReg, physReg, machine.CarryMode)
	wordCarry(opcode, uint8, physReg, physReg, physReg, physReg, physReg, machine.CarryMode)
	mulHigh(uint8, physReg, physReg, physReg)
	shiftBy(opcode, uint8, physReg, physReg, physReg)
	load64(physReg, physReg, ir.ByteOrder)
	store64(physReg, physReg, ir.ByteOrder)
	narrowMemory(bool, uint8, physReg, physReg, ir.ByteOrder)
	load8(physReg, physReg)
	store8(physReg, physReg)
	loadAddress(physReg, physReg, physReg, uint64, uint64, ir.ByteOrder)
	storeAddress(physReg, physReg, physReg, uint64, uint64, ir.ByteOrder)
	loadOffset(physReg, physReg, uint64, ir.ByteOrder)
	storeOffset(physReg, physReg, uint64, ir.ByteOrder)
	syscall()
	callC(abi.CallEffect)
	callGo()
	saveLink(int)
	restoreLink(int)
	spill(physReg, int)
	reload(physReg, int)
}

// machineEmitter receives the allocator's physical registers and spills.
type machineEmitter interface {
	instructionSize(physicalInsn) int
	machineOperations
	selectFlags(physReg, physReg, physReg, ir.BranchCondition)
	currentFlags() flagState
	binaryCarry(opcode, physReg, physReg, physReg)
	mulWide(physReg)
	incomingWord(physReg, int)
	forgetFlags()
	mulAdd(physReg, physReg, physReg, physReg)
	flagsClobbered() bool
	callStackBytes() int
	linkSavedInFrame() bool
	spillSlotLimit() int
	body(physReg, int) (physicalProgram, error)
	bodyBare(physReg, int) (physicalProgram, error)
	bindFlagValue(int, int, int, opcode, bool)
	aliasCarryValue(int, int, uint8)
	aliasFlagValue(int, int, uint8)
	vectorInstruction(machine.Instruction)
	vectorSpill(physReg, int)
	vectorReload(physReg, int)
	finish(physReg, int) (physicalProgram, error)
}

type emissionArtifact struct {
	physical    *physicalProgram
	code        []byte
	assembly    string
	stackBytes  int
	relocations []machine.Relocation
	goUnwind    goUnwindInfo
}

type emissionKind uint8

const (
	emitNative emissionKind = iota
	emitText
	// Layout modes retain physical operations and encode nothing.
	emitLayoutNative
	emitLayoutText
)

func (k emissionKind) layout() bool {
	return k == emitLayoutNative || k == emitLayoutText
}
func (k emissionKind) native() bool {
	return k == emitNative || k == emitLayoutNative
}

type machineOp = machine.SelectedOpcode

const (
	mVector          = machine.SelectedVector
	mConst           = machine.SelectedConst
	mSymbol          = machine.SelectedSymbol
	mMove            = machine.SelectedMove
	mBinary          = machine.SelectedBinary
	mFloatBinary     = machine.SelectedFloatBinary
	mFloatFMA        = machine.SelectedFloatFMA
	mFloatUnary      = machine.SelectedFloatUnary
	mCountZeros      = machine.SelectedCountZeros
	mAndNot          = machine.SelectedAndNot
	mCarryBit        = machine.SelectedCarryBit
	mWordCarry       = machine.SelectedWordCarry
	mMulHigh         = machine.SelectedMulHigh
	mMulHighSigned   = machine.SelectedMulHighSigned
	mDivide          = machine.SelectedDivide
	mIntToFloat      = machine.SelectedIntToFloat
	mFloatToInt      = machine.SelectedFloatToInt
	mFloatCompare    = machine.SelectedFloatCompare
	mShiftBy         = machine.SelectedShiftBy
	mBinaryImmediate = machine.SelectedBinaryImmediate
	mInc             = machine.SelectedInc
	mDec             = machine.SelectedDec
	mNarrow          = machine.SelectedNarrow
	mSignExtend      = machine.SelectedSignExtend
	mShift           = machine.SelectedShift
	mScaled          = machine.SelectedScaled
	mMulAdd          = machine.SelectedMulAdd
	mSelect          = machine.SelectedSelect
	mLoad64          = machine.SelectedLoad64
	mStore64         = machine.SelectedStore64
	mLoad16          = machine.SelectedLoad16
	mStore16         = machine.SelectedStore16
	mLoad32          = machine.SelectedLoad32
	mStore32         = machine.SelectedStore32
	mLoad8           = machine.SelectedLoad8
	mStore8          = machine.SelectedStore8
	mLoadAddress     = machine.SelectedLoadAddress
	mStoreAddress    = machine.SelectedStoreAddress
	mLoadOffset      = machine.SelectedLoadOffset
	mStoreOffset     = machine.SelectedStoreOffset
	mSyscall         = machine.SelectedSyscall
	mCallC           = machine.SelectedCallC
	mCallGo          = machine.SelectedCallGo
)

type machineInstruction = machine.SelectedInstruction
type machineValue = machine.SelectedValue
type machineProgram = machine.SelectedProgram

type loweringResult struct {
	physical    *physicalProgram
	code        []byte
	report      abi.EmissionReport
	assembly    string
	relocations []machine.Relocation
	goUnwind    goUnwindInfo
}

func lowerFunction(ops []instruction, leaves []scopeEvent, arity, result int, resultParts []int, options compileOptions, context ...*loweringInput) (loweringResult, error) {
	return lowerFunctionMode(ops, leaves, arity, result, resultParts, options, emitNative, context...)
}

func lowerFunctionMode(ops []instruction, leaves []scopeEvent, arity, result int, resultParts []int, options compileOptions, kind emissionKind, context ...*loweringInput) (loweringResult, error) {
	if err := validateEmissionTarget(options, kind); err != nil {
		return loweringResult{}, err
	}
	if options.ABI != abi.C && options.ABI != abi.Go {
		return loweringResult{}, ErrUnsupportedABI
	}
	if options.StackBytesAvailable < 0 {
		return loweringResult{}, ErrInvalidFunction
	}
	if options.Stack != abi.StackNoUse && options.Stack != abi.StackPlatform {
		return loweringResult{}, ErrInvalidFunction
	}
	if options.StackPointer != abi.StackPointerReserved && options.StackPointer != abi.StackPointerOrdinary {
		return loweringResult{}, ErrInvalidFunction
	}
	if options.Stack == abi.StackNoUse && options.StackBytesAvailable != 0 {
		return loweringResult{}, fmt.Errorf("StackNoUse requires a zero platform stack budget (got %d): %w", options.StackBytesAvailable, ErrInvalidFunction)
	}
	if options.ManagedCalls && (options.Stack != abi.StackPlatform || options.StackPointer != abi.StackPointerReserved) {
		return loweringResult{}, ErrStackRequired
	}
	target := newMachineTarget(options)
	if target == nil {
		return loweringResult{}, ErrUnsupported
	}
	if options.ManagedCalls && target.ManagedFrameLimit() == 0 {
		return loweringResult{}, ErrUnsupported
	}
	if target.RegisterBits() != 32 && target.RegisterBits() != 64 {
		return loweringResult{}, ErrUnsupported
	}
	var scratch *compileScratch
	if len(context) > 0 && context[0] != nil {
		scratch = context[0].scratch
	}
	emitter := newMachineEmitter(options, scratch)
	if emitter == nil {
		return loweringResult{}, ErrUnsupported
	}
	var assembler machineAssembler
	if !kind.layout() {
		assembler = newMachineAssembler(options, kind)
	}
	if !kind.layout() && assembler == nil {
		return loweringResult{}, ErrUnsupported
	}
	if options.StackPointer == abi.StackPointerOrdinary && !target.SupportsOrdinalSP() {
		return loweringResult{}, ErrUnsupportedStackRole
	}
	program, err := selectProgramResultParts(ops, leaves, arity, result, resultParts, target, scratch)
	if err != nil {
		return loweringResult{}, fmt.Errorf("select %s instructions: %w", target.Architecture(), err)
	}
	if len(context) > 0 && context[0] != nil {
		bindVariableHomes(&program, context[0].source, 0, context[0].homes, context[0].facts)
	}
	if err := bridgeManagedCCalls(&program, options, kind); err != nil {
		return loweringResult{}, err
	}
	var entryWordBuffer [16]int
	var entryFacts *programFacts
	if len(context) > 0 && context[0] != nil {
		entryFacts = context[0].facts
	}
	entry, err := entryLayout(target, entryWordsWithFacts(ops, target.RegisterBits(), entryWordBuffer[:0], entryFacts))
	if err != nil {
		return loweringResult{}, err
	}
	if target.Architecture() == "386" && options.ABI == abi.Go {
		emitter.(*physicalEmitter).goReturnOffset = entry.result.Words[0].StackOffset
	}
	if len(resultParts) != 0 {
		if options.ABI != abi.C || target.RegisterBits() != 64 || len(resultParts) != 2 || resultParts[0] != result {
			return loweringResult{}, ErrBackendLimit
		}
		pair, supported := target.Integer128Result()
		if !supported {
			return loweringResult{}, ErrBackendLimit
		}
		entry.result.Words = []abi.WordLocation{
			{Storage: abi.Register, Register: target.RegisterName(pair[0])},
			{Storage: abi.Register, Register: target.RegisterName(pair[1])},
		}
	}
	for _, ins := range program.Instructions {
		if (ins.Op == mSyscall || ins.Op == mCallC || ins.Op == mCallGo) && options.Stack != abi.StackPlatform {
			return loweringResult{}, ErrStackRequired
		}
		if ins.Op == mCallGo && !options.ManagedCalls && options.ABI != abi.Go {
			return loweringResult{}, ErrExternalCallOnly
		}
	}
	var allocationState *allocationTemps
	if len(context) > 0 && context[0] != nil && context[0].scratch != nil {
		allocationState = &context[0].scratch.allocation
	}
	elider, elide := target.(machine.FlagElider)
	elide = elide && !options.keepFlagWrites()
	if elide {
		// Finishing elides on the body alone, before it adds the frame.
		emitter.(*physicalEmitter).elideFlags = func(s *machine.Stream) error {
			_, err := elider.ElideDeadFlags(s, nil, nil)
			return err
		}
	}
	physical, workspaceBytes, written, err := allocateAndEmit(program, target, emitter, allocationState)
	if err != nil {
		return loweringResult{}, fmt.Errorf("allocate %s registers: %w", target.Architecture(), err)
	}
	if elide && !emitter.(*physicalEmitter).flagsElided {
		// A program finished without finishRegisters still holds its
		// instructions in the stream; resynchronize when they differ.
		s := emitter.(*physicalEmitter).machineStream()
		if len(s.Instructions()) != len(physical.insns) || s.PC() != physical.codeBytes {
			instructions := make([]machine.Instruction, len(physical.insns))
			for n, i := range physical.insns {
				instructions[n] = i.ins
			}
			s.Replace(instructions)
		}
		if _, err := elider.ElideDeadFlags(s, nil, nil); err != nil {
			return loweringResult{}, err
		}
		physical.codeBytes = 0
		for n, i := range s.Instructions() {
			physical.insns[n] = locatedInsn{ins: i, pc: physical.codeBytes}
			physical.codeBytes += s.SizeAt(n)
		}
	}
	if err := relaxFunctionLiterals(&physical, emitter, target.Architecture()); err != nil {
		return loweringResult{}, err
	}
	codeBytes := finishOrMeasureLiterals(&physical, kind)
	recordImplicitWrites(target, physical, &written)
	output := emissionArtifact{physical: &physical, stackBytes: physical.stackBytes}
	if !kind.layout() {
		output, err = assembleProgram(physical, assembler)
		if err != nil {
			return loweringResult{}, fmt.Errorf("assemble %s instructions: %w", target.Architecture(), err)
		}
	}
	var goUnwind goUnwindInfo
	if kind == emitNative && options.ManagedCalls && target.ManagedFrameLimit() > 0 {
		goUnwind, err = goUnwindFromProgram(physical)
		if err != nil {
			return loweringResult{}, err
		}
	}
	report := abi.EmissionReport{
		LoweringStats: abi.LoweringStats{Blocks: 1, Allocations: 1},
		Architecture:  target.Architecture(),
		PPC64CPU:      ppc64CPULevel(target), CPU: reportCPU(options),
		ABI:                options.ABI,
		GoManaged:          options.ManagedCalls,
		Stack:              options.Stack,
		StackPointer:       options.StackPointer,
		ArgumentRegisters:  make([]string, arity),
		ResultRegister:     singleABIRegister(entry.result),
		ArgumentLocations:  entry.arguments,
		ResultLocation:     entry.result,
		ResultWords:        1,
		FlagsClobbered:     emitter.flagsClobbered(),
		CodeBytes:          codeBytes,
		WorkspaceBytes:     workspaceBytes,
		WorkspaceAlignment: int(target.RegisterBits() / 8),
		StackBytes:         output.stackBytes,
		CallStackBytes:     emitter.callStackBytes(),
		SpillStorage:       abi.SpillNone,
	}
	for _, v := range program.Values {
		if v.Class == backend.Vector {
			report.WorkspaceAlignment = 16
			break
		}
	}
	if len(resultParts) != 0 {
		report.ResultWords = 2
	}
	if workspaceBytes != 0 {
		report.WorkspaceRegister = target.RegisterName(target.WorkspaceRegister())
		report.SpillStorage = abi.SpillWorkspace
	}
	if output.stackBytes != 0 {
		report.SpillStorage = abi.SpillStack
	}
	for _, ins := range program.Instructions {
		switch ins.Op {
		case mVector:
			if ins.Arithmetic == ir.OpVLoad {
				report.ExplicitMemoryReads, report.MayReadMemory = true, true
			}
			if ins.Arithmetic == ir.OpVStore {
				report.ExplicitMemoryWrites, report.MayWriteMemory = true, true
			}
		case mLoad64, mLoad8, mLoad16, mLoad32, mLoadAddress, mLoadOffset:
			report.ExplicitMemoryReads = true
			report.MayReadMemory = true
		case mStore64, mStore8, mStore16, mStore32, mStoreAddress, mStoreOffset:
			report.ExplicitMemoryWrites = true
			report.MayWriteMemory = true
		case mSyscall:
			report.HasSystemCalls = true
			report.MayReadMemory = true
			report.MayWriteMemory = true
		case mCallC, mCallGo:
			report.HasFunctionCalls = true
			if ins.Effect == nil || ins.Effect.MayReadMemory {
				report.MayReadMemory = true
			}
			if ins.Effect == nil || ins.Effect.MayWriteMemory {
				report.MayWriteMemory = true
			}
		}
	}
	for i := range report.ArgumentRegisters {
		report.ArgumentRegisters[i] = singleABIRegister(entry.arguments[i])
	}
	var physicalScanBuffer [64]physReg
	for _, register := range target.PhysicalRegisters().AppendTo(physicalScanBuffer[:0]) {
		if written[register] {
			if savedByEntry(emitter, register) {
				continue
			}
			if preserver, ok := target.(interface{ PreservesRegister(physReg) bool }); ok &&
				preserver.PreservesRegister(register) {
				continue
			}
			report.ClobberedRegisters = append(report.ClobberedRegisters, target.RegisterName(register))
		}
	}
	report.ClobberedFPRegisters, err = fpClobbersFromPhysical(physical, emitter, target)
	report.FPStatusModified = fpStatusModifiedFromPhysical(physical)
	report.ClobberedSpecialRegisters = specialWritesFromPhysical(physical, target)
	if err != nil {
		return loweringResult{}, err
	}
	return loweringResult{physical: output.physical, code: output.code, report: report, assembly: output.assembly, relocations: output.relocations, goUnwind: goUnwind}, nil
}

func selectProgram(ops []instruction, leaves []scopeEvent, arity, result int, target machineTarget, scratch ...*compileScratch) (machineProgram, error) {
	return selectProgramExports(ops, leaves, arity, result, nil, target, scratch...)
}

func selectProgramResultParts(ops []instruction, leaves []scopeEvent, arity, result int, parts []int, target machineTarget, scratch ...*compileScratch) (machineProgram, error) {
	if len(parts) == 0 {
		return selectProgram(ops, leaves, arity, result, target, scratch...)
	}
	if len(parts) != 2 || parts[0] != result {
		return machineProgram{}, ErrInvalidFunction
	}
	program, err := selectProgramExports(ops, leaves, arity, result, parts[1:], target, scratch...)
	if err != nil {
		return machineProgram{}, err
	}
	program.Exports = nil
	program.ResultParts = append([]int(nil), parts...)
	return program, nil
}

func selectProgramExports(ops []instruction, leaves []scopeEvent, arity, result int, exports []int, target machineTarget, scratch ...*compileScratch) (machineProgram, error) {
	var buffers *selectionBuffers
	if len(scratch) > 0 && scratch[0] != nil {
		buffers = scratch[0].selection.lease()
	}
	return selectProgramBuffers(ops, leaves, arity, result, exports, abi.MaxArgumentsFor(target.Architecture()), target, buffers)
}

// maxSpillSlots bounds supported word-addressed spill storage, including fixed
// Var/parameter reservations. ARM64 may reserve one of these words for LR.
const maxSpillSlots = 4096

func spillStorageLimitError(required, reserved, limit int) error {
	return &LimitError{Kind: LimitSpillWords, Needed: required, Limit: limit, Detail: fmt.Sprintf("reserved homes/parameters %d", reserved)}
}

type valueLocation = regalloc.ValueLocation
type exportLocation = regalloc.Home

func allocateAndEmit(program machineProgram, target machineTarget, emitter machineEmitter, temps ...*allocationTemps) (physicalProgram, int, registerWrites, error) {
	return allocateAndEmitMode(program, target, emitter, true, false, temps...)
}

func allocateBlock(program machineProgram, target machineTarget, emitter machineEmitter, temps ...*allocationTemps) (physicalProgram, int, registerWrites, error) {
	physical, workspace, written, err := allocateAndEmitMode(program, target, emitter, false, false, temps...)
	physical.flags = registerImageFlags(physical.flags, program, target.RegisterBits())
	return physical, workspace, written, err
}

func allocateBranchBlock(program machineProgram, target machineTarget, emitter machineEmitter, temps ...*allocationTemps) (physicalProgram, int, registerWrites, error) {
	physical, workspace, written, err := allocateAndEmitMode(program, target, emitter, false, true, temps...)
	physical.flags = registerImageFlags(physical.flags, program, target.RegisterBits())
	return physical, workspace, written, err
}

func allocateAndEmitMode(program machineProgram, target machineTarget, emitter machineEmitter, final, bare bool, temps ...*allocationTemps) (physicalProgram, int, registerWrites, error) {
	var allocState *allocationTemps
	if len(temps) > 0 && temps[0] != nil {
		allocState = temps[0]
	} else {
		allocState = new(allocationTemps)
	}
	var operandBuffer [4]int
	// Call specs leak to the heap through abi.PreflightCall, so a stack array
	// here would be heap-allocated on every call. Grow one buffer on the
	// first call instruction instead; call effects never retain it.
	var argumentWordBuffer []int
	argumentWords := func(args []int) []int {
		argumentWordBuffer = selectedArgumentWordsInto(program, args, argumentWordBuffer[:0])
		return argumentWordBuffer
	}
	emitter.(*physicalEmitter).machineStream().Reserve(len(program.Instructions) + 8)
	// Apply the same call/stack contract to straight-line bodies, control-flow
	// fragments and direct Emitter users. A workspace cannot authorize a callee
	// to use a platform stack under StackNoUse.
	if emitter.(*physicalEmitter).stack != abi.StackPlatform {
		for _, ins := range program.Instructions {
			if ins.Op == mCallC || ins.Op == mCallGo || ins.Op == mSyscall {
				return physicalProgram{}, 0, registerWrites{}, fmt.Errorf("outbound calls require StackPlatform: %w", ErrStackRequired)
			}
		}
	}
	if physical, ok := emitter.(*physicalEmitter); ok && physical.harden {
		var err error
		program, err = hardenSelected(program, target)
		if err != nil {
			return physicalProgram{}, 0, registerWrites{}, err
		}
	}
	for id, v := range program.Values {
		if v.EntryReg >= 0 && backend.IsVectorRegister(physReg(v.EntryReg)) {
			program.Values[id].Class = backend.Float
		}
	}
	markFloatingHomeGets(&program)
	if len(floatRegisters(target.Architecture())) != 0 {
		program = assignFloatResidency(program, target.RegisterBits())
	}
	program = foldFloatLiterals(program, allocState)
	if validator, ok := target.(interface{ validateProgram(machineProgram) error }); ok {
		if err := validator.validateProgram(program); err != nil {
			return physicalProgram{}, 0, registerWrites{}, err
		}
	}
	if target.Architecture() == "386" {
		physical := emitter.(*physicalEmitter)
		for _, ins := range program.Instructions {
			if ins.Op != mCallC && ins.Op != mCallGo {
				continue
			}
			bytes := 0
			for _, words := range argumentWords(ins.Args[1:]) {
				bytes += 4 * words
			}
			if ins.Op == mCallGo {
				bytes += 8
			}
			physical.callAreaBytes = max(physical.callAreaBytes, bytes)
		}
	}
	registers := append(appendBufferTracked(&allocState.registers, &allocState.history.registers, 64), allocationRegisters(target, program.HomePlan, emitter.(*physicalEmitter).stack)...)
	defer func() { allocState.registers = registers }()
	// Preserved registers come last. Lower indices are tried first, so they
	// are used only once the volatile ones are taken, and each costs one
	// save and restore for the whole function instead of a spill per use.
	preservedStart := len(registers)
	if !emitter.(*physicalEmitter).withholdPreserved {
		var present [256]bool
		for _, r := range registers {
			present[r] = true
		}
		for _, r := range preservedAllocatable(target) {
			if !present[r] {
				registers = append(registers, r)
			}
		}
	}
	preservedEnd := len(registers)
	activeHomeRegion := program.HomeRegion
	// Reservation masks change only at scope transitions. Keep the active
	// mask beside the allocator instead of repeating a map lookup for every
	// candidate register. Hand-built plans retain their lookup fallback.
	cachedHomeMask := program.HomePlan != nil && program.HomePlan.ReservedRegions != nil
	var reservedHomeMask [4]uint64
	if cachedHomeMask {
		reservedHomeMask = program.HomePlan.ReservedRegions[activeHomeRegion]
	}
	homeReserved := func(r physReg) bool {
		if cachedHomeMask {
			return reservedHomeMask[int(r)/64]&(uint64(1)<<(uint(r)%64)) != 0
		}
		return program.HomePlan.RegisterReserved(activeHomeRegion, r)
	}
	needsFPBank := false
	for _, v := range program.Values {
		needsFPBank = needsFPBank || v.Class == backend.Vector || v.Class == backend.Float
	}
	needsFPBank = needsFPBank || program.HomePlan.NeedsFloating()
	// A scope-only fragment can restore FP homes without an FP SSA producer.
	if needsFPBank {
		registers = append(registers, floatRegisters(target.Architecture())...)
	}
	if len(registers) > 64 {
		return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
	}
	entryArity := 0
	for _, value := range program.Values {
		if value.Param >= entryArity {
			entryArity = value.Param + 1
		}
	}
	entryWords := resetBufferTracked(&allocState.entryWords, &allocState.history.entryWords, entryArity)
	for _, value := range program.Values {
		if value.Param >= 0 {
			entryWords[value.Param] = max(1, int(value.Words))
		}
	}
	entry, err := cachedEntryLayout(target, entryWords)
	if err != nil {
		return physicalProgram{}, 0, registerWrites{}, err
	}
	if target.Architecture() == "386" && target.ABIFlavor() == abi.Go {
		entry.result = abi.ValueLocation{Words: []abi.WordLocation{
			{Storage: abi.Register, Register: "EAX"},
			{Storage: abi.Register, Register: "EDX"},
		}}
	}
	written := &allocState.mover.written
	*written = registerWrites{}
	if len(program.ResultParts) != 0 {
		if len(program.ResultParts) != 2 || program.ResultParts[0] != program.Result || program.ResultParts[1] < 0 || program.ResultParts[1] >= len(program.Values) {
			return physicalProgram{}, 0, registerWrites{}, ErrInvalidFunction
		}
	}
	if program.HomePlan != nil {
		program.ReservedSlots = max(program.ReservedSlots, program.HomePlan.ReservedSlots)
	}
	slotLimit := emitter.spillSlotLimit()
	spillLimitError := func(required int) error {
		return spillStorageLimitError(required, program.ReservedSlots, slotLimit)
	}
	storageWords := func(id int) int { return valueStorageWords(program.Values[id], target.RegisterBits()) }
	vectorParity := 0
	if target.Architecture() == "arm64" {
		if p, ok := emitter.(*physicalEmitter); ok && p.stack == abi.StackPlatform {
			vectorParity = 1
			if p.vectorHomes%2 != 0 {
				vectorParity = 0
			}
		}
	}
	if target.Architecture() == "amd64" {
		hasCall := false
		for _, i := range program.Instructions {
			hasCall = hasCall || i.Op == mCallC || i.Op == mCallGo
		}
		if p, ok := emitter.(*physicalEmitter); ok && p.stack == abi.StackPlatform {
			vectorParity = (p.vectorHomes + 1) % 2
			if hasCall {
				vectorParity = p.vectorHomes % 2
			}
		}
	}
	// Registers reserved for variable homes are blocked, except allowedHome,
	// the home a result is about to be stored to.
	allowedHome, allowedIndex := -1, -1
	homeBlocked := func() uint64 {
		var mask uint64
		for i, r := range registers {
			if homeReserved(r) {
				mask |= 1 << uint(i)
			}
		}
		return mask
	}
	regionBlocked := homeBlocked()
	alloc := &allocState.allocator
	// The allocator names registers by identity. Blocking changes at scope
	// transitions and around home stores; pass a new set only on a change.
	blockedSet := func(mask uint64) backend.RegisterSet {
		var list [64]backend.Register
		n := 0
		for i, r := range registers {
			if mask&(1<<uint(i)) != 0 {
				list[n] = r
				n++
			}
		}
		return backend.NewRegisterSet(list[:n]...)
	}
	lastBlocked := regionBlocked
	block := func(mask uint64) {
		if mask != lastBlocked {
			alloc.Block(blockedSet(mask))
			lastBlocked = mask
		}
	}
	mover := &allocState.mover
	mover.values, mover.emitter, mover.registerBits = program.Values, emitter, target.RegisterBits()
	defer mover.release()
	sequence := &allocState.program
	*sequence = allocationProgram{values: program.Values, instructions: program.Instructions, result: program.Result, resultHigh: -1,
		exports: program.Exports, discardResult: program.DiscardResult, registerBits: target.RegisterBits()}
	if len(program.ResultParts) != 0 {
		sequence.resultHigh = program.ResultParts[1]
	}
	defer sequence.release()
	config := regalloc.Config{
		Registers:     target.Registers().Derive(registers...),
		ReservedSlots: program.ReservedSlots, SlotLimit: slotLimit, VectorSlotParity: vectorParity,
		Blocked: blockedSet(regionBlocked), LimitError: mover.limitError(program.ReservedSlots, slotLimit),
		Preserved: savedOnFirstUse(target, emitter, registers[preservedStart:preservedEnd]),
	}
	if onlineAllocation && onlineEligible(program, target) {
		output, workspace, writes, taken, err := allocateOnline(&onlineSetup{program: program, target: target, emitter: emitter, final: final, bare: bare,
			temps: allocState, config: config, entry: entry, written: written})
		if taken {
			return output, workspace, writes, err
		}
	}
	err = alloc.Reset(config, sequence, mover)
	if err != nil {
		return physicalProgram{}, 0, registerWrites{}, err
	}
	defer alloc.Finish()
	locations := alloc.Locations()
	physicalNames := physicalRegistersByArchitecture[target.Architecture()]
	// Export preferences used to go through general register names, so a
	// value whose last export register is a vector register ends with no
	// preference at all. Keep that until the change is made on purpose.
	vectorExports := false
	if program.DirectExports {
		for _, r := range program.ExportRegisters {
			vectorExports = vectorExports || r >= 0 && backend.IsVectorRegister(physReg(r))
		}
	}
	vectorExport := func(id int) bool {
		last := -1
		for i, export := range program.Exports {
			if export == id && i < len(program.ExportRegisters) && program.ExportRegisters[i] >= 0 {
				last = program.ExportRegisters[i]
			}
		}
		return last >= 0 && backend.IsVectorRegister(physReg(last))
	}
	for id, reg := range program.PreferredRegisters {
		if id < 0 || id >= len(program.Values) {
			return physicalProgram{}, 0, registerWrites{}, ErrInvalidFunction
		}
		if !vectorExports || !vectorExport(id) {
			alloc.Prefer(id, reg)
		}
	}
	if !bare && (!vectorExports || !vectorExport(program.Result)) {
		alloc.Prefer(program.Result, target.ResultRegister())
	}
	if program.DirectExports {
		if len(program.ExportRegisters) != len(program.Exports) {
			return physicalProgram{}, 0, registerWrites{}, ErrInvalidFunction
		}
		for i, id := range program.Exports {
			if r := physReg(program.ExportRegisters[i]); program.ExportRegisters[i] >= 0 && !backend.IsVectorRegister(r) && (!vectorExports || !vectorExport(id)) {
				alloc.Prefer(id, r)
			}
		}
	}
	definedAt := resetBufferTracked(&allocState.definedAt, &allocState.history.definedAt, len(program.Values))
	for id := range definedAt {
		definedAt[id] = -1
	}
	for pos, ins := range program.Instructions {
		if ins.Dst >= 0 {
			definedAt[ins.Dst] = pos
		}
	}
	innermostScope := func(pos int) machine.SelectedScope {
		if len(program.Scopes) == 0 {
			return machine.SelectedScope{End: len(program.Instructions)}
		}
		scope := program.Scopes[0]
		for _, candidate := range program.Scopes[1:] {
			if candidate.Start <= pos && pos < candidate.End &&
				candidate.Start >= scope.Start && candidate.End <= scope.End &&
				(candidate.Start > scope.Start || candidate.End < scope.End || candidate.ID > scope.ID) {
				scope = candidate
			}
		}
		return scope
	}
	previousCall := resetBufferTracked(&allocState.previousCall, &allocState.history.previousCall, len(program.Instructions))
	lastCall := -1
	for pos, i := range program.Instructions {
		previousCall[pos] = lastCall
		if i.Op == mSyscall || i.Op == mCallC || i.Op == mCallGo {
			lastCall = pos
		}
	}
	// Fixed call operands prefer their producers' registers. Carry that
	// preference backward through operations that can reuse an input register.
	// Within a scope, the final call use can guide a value with earlier uses.
	for pos, ins := range program.Instructions {
		if ins.Op != mSyscall && ins.Op != mCallC && ins.Op != mCallGo {
			continue
		}
		kind := abi.SystemCall
		if ins.Op == mCallC || ins.Op == mCallGo {
			kind = abi.FunctionCall
		}
		flavor := abi.C
		if ins.Op == mCallGo {
			flavor = abi.Go
		}
		effect, err := cachedCallEffect(abi.CallSpec{Architecture: target.Architecture(), Kind: kind, ABI: flavor, ArgumentCount: len(ins.Args) - 1, ArgumentWords: argumentWords(ins.Args[1:])})
		if err != nil {
			return physicalProgram{}, 0, registerWrites{}, err
		}
		first := effect.NumberRegister
		if ins.Op == mCallC || ins.Op == mCallGo {
			first = target.RegisterName(target.CallTargetRegister())
		}
		scope := innermostScope(pos)
		for i, id := range ins.Args {
			// The fixed register of operand i: the call target or number,
			// then the argument registers.
			fixed := first
			if i > 0 {
				if i-1 >= len(effect.ArgumentRegisters) {
					continue
				}
				fixed = effect.ArgumentRegisters[i-1]
			}
			if fixed == "" || len(alloc.Uses(id)) == 0 || alloc.Uses(id)[len(alloc.Uses(id))-1] != pos || definedAt[id] < scope.Start {
				continue
			}
			// A prior call may overwrite the desired home before this use.
			safe := previousCall[pos] <= definedAt[id]
			if reg, ok := physicalNames[fixed]; safe && ok {
				alloc.Prefer(id, reg)
			}
		}
	}
	for pos := len(program.Instructions) - 1; pos >= 0; pos-- {
		ins := program.Instructions[pos]
		if ins.Dst < 0 {
			continue
		}
		preference, ok := alloc.Preference(ins.Dst)
		if !ok {
			continue
		}
		input := -1
		inputPosition := 0
		switch ins.Op {
		case mMove, mInc, mDec, mNarrow, mSignExtend, mShift, mShiftBy, mCarryBit, mWordCarry, mMulHigh, mMulHighSigned, mBinary, mFloatBinary, mFloatFMA, mFloatUnary, mBinaryImmediate, mScaled, mMulAdd:
			input = ins.A
		case mSelect:
			input, inputPosition = ins.C, 2
		}
		if input >= 0 && len(alloc.Uses(input)) == 1 && target.CanReuse(ins, inputPosition) {
			alloc.Prefer(input, preference)
		}
	}
	for id, value := range program.Values {
		if alloc.Remaining(id) == 0 || value.Param < 0 && value.EntryReg < 0 {
			continue
		}
		if value.Param >= 0 {
			words := entry.arguments[value.Param].Words
			valueWordCount := int(value.Words)
			if valueWordCount == 0 {
				valueWordCount = 1
			}
			if len(words) != valueWordCount || len(words) > 2 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			if words[0].Storage == abi.Stack {
				for word, location := range words {
					if location.Storage != abi.Stack || location.StackOffset < 0 {
						return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
					}
					locations[id].Incoming[word] = location.StackOffset
				}
				locations[id].IncomingWords = uint8(len(words))
				continue
			}
			group := regalloc.Group{Words: uint8(len(words))}
			for word, location := range words {
				reg, ok := physicalNames[location.Register]
				if location.Storage != abi.Register || !ok {
					return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
				}
				group.Registers[word] = reg
			}
			if err := alloc.Claim(id, group); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			continue
		}
		if value.Words > 1 && value.Class != backend.Float {
			return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
		}
		if err := alloc.Claim(id, regalloc.SingleRegister(physReg(value.EntryReg))); err != nil {
			return physicalProgram{}, 0, registerWrites{}, err
		}
	}
	if program.ReservedSlots > slotLimit {
		return physicalProgram{}, 0, registerWrites{}, spillLimitError(program.ReservedSlots)
	}
	for id, value := range program.Values {
		if value.EntrySlot >= 0 && alloc.Remaining(id) > 0 {
			words := int(value.Words)
			if words == 0 {
				words = 1
			}
			if value.EntrySlot+words > program.ReservedSlots {
				return physicalProgram{}, 0, registerWrites{}, ErrInvalidFunction
			}
			locations[id].Slot = value.EntrySlot
			locations[id].SlotWords = uint8(words)
		}
	}
	spillValue := mover.Spill
	valueWords := func(id int) int {
		if program.Values[id].Class == backend.Float {
			return 1
		}
		if program.Values[id].Words == 0 {
			return 1
		}
		return int(program.Values[id].Words)
	}
	// Only used by 386 word-streaming expansions. Incoming words and constants
	// are already durable; other values get homes from the normal spill scheduler.
	durable386 := func(id int) error {
		if program.Values[id].Constant || locations[id].Slot >= 0 || locations[id].IncomingWords != 0 {
			return nil
		}
		g, err := alloc.Ensure(id, regalloc.Pins{})
		if err != nil {
			return err
		}
		return alloc.Spill(id, g)
	}
	load386 := func(r physReg, id, word int) {
		if word >= valueWords(id) {
			emitter.constant(r, 0)
		} else if program.Values[id].Constant {
			emitter.constant(r, uint64(uint32(program.Values[id].Imm>>(32*word))))
		} else if locations[id].Slot >= 0 {
			emitter.reload(r, locations[id].Slot+word)
		} else {
			emitter.incomingWord(r, locations[id].Incoming[word])
		}
		written[r] = true
	}
	// Call clobbers repeatedly resolve names from the same immutable target set.
	physicalByName := func(name string) (physReg, bool) { register, ok := physicalNames[name]; return register, ok }

	registerIndex := func(reg physReg) int {
		for i, candidate := range registers {
			if candidate == reg {
				return i
			}
		}
		return -1
	}
	// spillHomeOwner frees a home register, moving or spilling its owner.
	spillHomeOwner := func(r physReg, pins regalloc.Pins) error {
		return alloc.Evict(r, pins)
	}
	homeOrders := make(map[int][]int)
	homeIDs := func(region int) []int {
		if ids, ok := homeOrders[region]; ok {
			return ids
		}
		var ids []int
		if program.HomePlan != nil && program.HomePlan.RegisterVariables != nil {
			return program.HomePlan.RegisterVariables[region]
		}
		if program.HomePlan != nil {
			homes := program.HomePlan.Regions
			if homes == nil {
				homes = program.HomePlan.RegisterOverrides
			}
			for id, home := range homes[region] {
				if home.Register >= 0 {
					ids = append(ids, id)
				}
			}
		}
		sort.Ints(ids)
		homeOrders[region] = ids
		return ids
	}
	transitionHomes := func(next int) error {
		if program.HomePlan == nil {
			return nil
		}
		previousRegion := activeHomeRegion
		for _, id := range homeIDs(activeHomeRegion) {
			h, exists := program.HomePlan.Lookup(activeHomeRegion, id)
			if !exists {
				continue
			}
			n, ok := program.HomePlan.Lookup(next, id)
			if ok && h.Register >= 0 && h.Register != n.Register && (program.HomePlan.Parents[activeHomeRegion] != next || program.HomePlan.Modified[activeHomeRegion][id]) {
				emitHomeSpill(emitter, physReg(h.Register), h.Slot, h.Bits)
			}
		}
		// Snapshot relocation must respect every destination home, including
		// homes restored earlier in this transition.
		activeHomeRegion = next
		if cachedHomeMask {
			reservedHomeMask = program.HomePlan.ReservedRegions[next]
		}
		regionBlocked = homeBlocked()
		block(regionBlocked)
		for _, id := range homeIDs(next) {
			h, exists := program.HomePlan.Lookup(next, id)
			if !exists {
				continue
			}
			if h.Register < 0 {
				continue
			}
			o, ok := program.HomePlan.Lookup(previousRegion, id)
			if ok && o.Register == h.Register {
				continue
			}
			if ok {
				if err := spillHomeOwner(physReg(h.Register), regalloc.Pins{}); err != nil {
					return err
				}
				emitHomeReload(emitter, physReg(h.Register), h.Slot, h.Bits)
				written[physReg(h.Register)] = true
			}
		}
		return nil
	}
	lastHomeValue := allocState
	if program.HomePlan != nil {
		lastHomeValue.beginHomes(2 * len(program.HomePlan.Slots))
	}
	callHomeSaved := appendBufferTracked(&allocState.homesSaved, &allocState.history.homesSaved, 0)
	defer func() {
		allocState.homesSaved = callHomeSaved
		allocState.history.homesSaved.Observe(len(callHomeSaved))
	}()
	beforeHomeCall := func(ins machineInstruction) error {
		callHomeSaved = callHomeSaved[:0]
		if program.HomePlan == nil {
			return nil
		}
		kind, flavor := abi.FunctionCall, abi.C
		if ins.Op == mSyscall {
			kind = abi.SystemCall
		}
		if ins.Op == mCallGo {
			flavor = abi.Go
		}
		effect, err := cachedCallEffect(abi.CallSpec{Architecture: target.Architecture(), Kind: kind, ABI: flavor, ArgumentCount: len(ins.Args) - 1, ArgumentWords: argumentWords(ins.Args[1:])})
		if err != nil {
			return err
		}
		clobbers := map[string]bool{}
		for _, name := range effect.ClobberedRegisters {
			clobbers[name] = true
		}
		for _, name := range effect.ClobberedFPRegisters {
			clobbers[name] = true
		}
		for _, name := range effect.ArgumentRegisters {
			clobbers[name] = true
		}
		clobbers[effect.NumberRegister], clobbers[effect.ResultRegister] = true, true
		for _, id := range homeIDs(activeHomeRegion) {
			h, exists := program.HomePlan.Lookup(activeHomeRegion, id)
			if !exists {
				continue
			}
			if h.Register >= 0 && clobbers[target.RegisterName(physReg(h.Register))] {
				emitHomeSpill(emitter, physReg(h.Register), h.Slot, h.Bits)
				callHomeSaved = append(callHomeSaved, h)
				allocState.history.homesSaved.Observe(len(callHomeSaved))
			}
		}
		return nil
	}
	afterHomeCall := func() error {
		for _, h := range callHomeSaved {
			if err := spillHomeOwner(physReg(h.Register), regalloc.Pins{}); err != nil {
				return err
			}
			emitHomeReload(emitter, physReg(h.Register), h.Slot, h.Bits)
			written[physReg(h.Register)] = true
		}
		callHomeSaved = callHomeSaved[:0]
		return nil
	}
	liveFP := appendBufferTracked(&allocState.liveFP, &allocState.history.liveFP, len(registers))
	defer func() { allocState.liveFP = liveFP }()
	for position, ins := range program.Instructions {
		allowedHome, allowedIndex = -1, -1
		if ins.Dst >= 0 && program.HomePlan != nil && position+1 < len(program.Instructions) {
			next := program.Instructions[position+1]
			if next.Op == machine.SelectedHomeSet && next.A == ins.Dst {
				h, _ := program.HomePlan.Lookup(activeHomeRegion, next.Variable)
				if h.Register >= 0 && valueWords(ins.Dst) == 1 {
					index := registerIndex(physReg(h.Register))
					owner := alloc.Owner(physReg(h.Register))
					usesHere := 0
					for _, x := range ins.InputsInto(operandBuffer[:0]) {
						if x == owner {
							usesHere++
						}
					}
					if owner < 0 || alloc.Remaining(owner) == usesHere {
						allowedHome, allowedIndex = h.Register, index
						alloc.Prefer(ins.Dst, physReg(h.Register))
					}
				}
			}
		}
		if allowedIndex >= 0 {
			block(regionBlocked &^ (1 << uint(allowedIndex)))
		} else {
			block(regionBlocked)
		}
		// Constants are rematerializable. Keeping them virtual until a use
		// lets a fixed call operand materialize directly in its ABI register.
		if ins.Op == mConst {
			continue
		}
		inputs := ins.InputsInto(operandBuffer[:0])
		if ins.Op == machine.SelectedHomeBoundary {
			next := int(ins.Imm)
			if ins.Arithmetic == ir.OpScopeLeave && program.HomePlan != nil {
				next = program.HomePlan.Parents[next]
			}
			if err := transitionHomes(next); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			continue
		}
		if ins.Op == machine.SelectedHomeGet || ins.Op == machine.SelectedHomeSet {
			if program.HomePlan == nil || ins.Variable < 0 || ins.Variable >= len(program.HomePlan.Slots) {
				return physicalProgram{}, 0, registerWrites{}, ErrInvalidFunction
			}
			h, ok := program.HomePlan.Lookup(activeHomeRegion, ins.Variable)
			if !ok {
				h = machine.Home{Slot: program.HomePlan.Slots[ins.Variable], Register: -1}
			}
			h.Slot += int(ins.Imm) * int(64/target.RegisterBits())
			if ins.Op == machine.SelectedHomeSet {
				if h.Register >= 0 && program.Values[ins.A].Constant && program.Values[ins.A].Symbol == "" && valueWords(ins.A) == 1 {
					if err := spillHomeOwner(physReg(h.Register), regalloc.Pins{}); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
					if backend.IsVectorRegister(physReg(h.Register)) {
						emitter.(*physicalEmitter).literalValue(physReg(h.Register), ins.Bits, program.Values[ins.A].Imm)
					} else {
						emitter.constant(physReg(h.Register), program.Values[ins.A].Imm)
					}
					written[physReg(h.Register)] = true
					lastHomeValue.setHome(ins.Variable*2+int(ins.Imm), ins.A)
					alloc.Consume(inputs)
					continue
				}
				g, err := alloc.Ensure(ins.A, regalloc.Pins{})
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if h.Register >= 0 && g.Registers[0] != physReg(h.Register) {
					var protected regalloc.Pins
					for word := 0; word < int(g.Words); word++ {
						protected.AddRegister(g.Registers[word])
					}
					if e := spillHomeOwner(physReg(h.Register), protected); e != nil {
						return physicalProgram{}, 0, registerWrites{}, e
					}
				}
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				for word := 0; word < int(g.Words); word++ {
					r := g.Registers[word]
					if h.Register >= 0 {
						if physReg(h.Register) != r {
							emitHomeMove(emitter, ins.Bits, physReg(h.Register), r)
						}
						written[physReg(h.Register)] = true
					} else {
						emitHomeSpill(emitter, r, h.Slot+word, ins.Bits)
					}
				}
				lastHomeValue.setHome(ins.Variable*2+int(ins.Imm), ins.A)
				alloc.Consume(inputs)
			} else {
				var g regalloc.Group
				var err error
				if h.Register >= 0 && alloc.Owner(physReg(h.Register)) < 0 {
					g = regalloc.SingleRegister(physReg(h.Register))
				} else {
					g, err = alloc.Choose(ins.Dst, regalloc.Pins{})
				}
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				for word := 0; word < int(g.Words); word++ {
					r := g.Registers[word]
					if h.Register >= 0 {
						if r != physReg(h.Register) {
							emitHomeMove(emitter, ins.Bits, r, physReg(h.Register))
						}
					} else {
						emitHomeReload(emitter, r, h.Slot+word, ins.Bits)
					}
					written[r] = true
				}
				if source := lastHomeValue.home(ins.Variable*2 + int(ins.Imm)); source != 0 {
					emitter.aliasFlagValue(ins.Dst, source-1, ins.Bits)
				}
				if err := alloc.Claim(ins.Dst, g); err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if alloc.Remaining(ins.Dst) == 0 {
					alloc.Release(ins.Dst)
				}
			}
			continue
		}
		if ins.Op == mCallC || ins.Op == mCallGo || ins.Op == mSyscall {
			if err := beforeHomeCall(ins); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
		}
		if target.Architecture() == "386" && (ins.Op == mCallC || ins.Op == mCallGo) {
			if emitter.(*physicalEmitter).stack != abi.StackPlatform {
				return physicalProgram{}, 0, registerWrites{}, ErrStackRequired
			}
			callABI := abi.C
			if ins.Op == mCallGo {
				callABI = abi.Go
			}
			if _, err := cachedCallEffect(abi.CallSpec{Architecture: target.Architecture(), Kind: abi.FunctionCall, ABI: callABI, ArgumentCount: len(inputs) - 1, ArgumentWords: argumentWords(inputs[1:])}); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			argumentOffset := uint64(0)
			for _, id := range inputs[1:] {
				group, err := alloc.Ensure(id, regalloc.Pins{})
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if group.Words != 2 && group.Words != 1 {
					return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
				}
				for word := 0; word < int(group.Words); word++ {
					emitter.storeOffset(4, group.Registers[word], argumentOffset+uint64(word*4), ir.ByteOrderNative)
				}
				argumentOffset += uint64(group.Words) * 4
			}
			callee, err := alloc.Ensure(inputs[0], regalloc.Pins{})
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			if callee.Words != 1 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			calleeLow := callee.Registers[0]
			seenClobbers := make(map[int]bool)
			for _, r := range registers {
				id := alloc.Owner(r)
				if id < 0 || seenClobbers[id] ||
					(ins.Op == mCallC && !backend.IsVectorRegister(r) && r != 0 && r != 1 && r != 2) {
					continue
				}
				seenClobbers[id] = true
				usesHere := 0
				for _, input := range inputs {
					if input == id {
						usesHere++
					}
				}
				if alloc.Remaining(id) > usesHere && !program.Values[id].Constant && locations[id].Slot < 0 {
					group := alloc.Group(id)
					if err := alloc.Spill(id, group); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
				}
			}
			if calleeLow != 0 {
				emitter.move(0, calleeLow)
				written[0] = true
			}
			if ins.Op == mCallGo {
				emitter.callGo()
				emitter.loadOffset(0, 4, argumentOffset, ir.ByteOrderNative)
				emitter.loadOffset(2, 4, argumentOffset+4, ir.ByteOrderNative)
				for _, register := range registers {
					written[register] = true
				}
			} else {
				emitter.callC(abi.CallEffect{})
				written[0], written[1], written[2] = true, true, true
			}
			seenClobbers = make(map[int]bool)
			for _, r := range registers {
				id := alloc.Owner(r)
				if id < 0 || seenClobbers[id] ||
					(ins.Op == mCallC && !backend.IsVectorRegister(r) && r != 0 && r != 1 && r != 2) {
					continue
				}
				seenClobbers[id] = true
				alloc.Release(id)
			}
			result := regalloc.Group{Registers: [2]physReg{0, 2}, Words: 2}
			if registerIndex(0) < 0 || registerIndex(2) < 0 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			if err := alloc.Claim(ins.Dst, result); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			if err := afterHomeCall(); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if ins.Op == mSyscall || ins.Op == mCallC || ins.Op == mCallGo {
			liveFP = liveFP[:0]
			for _, r := range registers {
				id := alloc.Owner(r)
				if id >= 0 && alloc.Group(id).Registers[0] == r && (program.Values[id].Class == backend.Float || program.Values[id].Class == backend.Vector) {
					liveFP = append(liveFP, id)
				}
			}
			sort.Ints(liveFP)
			for _, id := range liveFP {
				value := program.Values[id]
				group := alloc.Group(id)
				if group.Words == 0 {
					continue
				}
				if alloc.Remaining(id) > 0 && !value.Constant && locations[id].Slot < 0 {
					if err := alloc.Spill(id, group); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
				}
				alloc.Release(id)
			}
		}
		if ins.Op == mSyscall || ins.Op == mCallC || ins.Op == mCallGo {
			kind := abi.SystemCall
			if ins.Op == mCallC || ins.Op == mCallGo {
				kind = abi.FunctionCall
			}
			flavor := abi.C
			if ins.Op == mCallGo {
				flavor = abi.Go
			}
			effect, err := cachedCallEffect(abi.CallSpec{Architecture: target.Architecture(), Kind: kind, ABI: flavor, ArgumentCount: len(inputs) - 1, ArgumentWords: argumentWords(inputs[1:])})
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			if ins.Effect != nil {
				custom := *ins.Effect
				if kind != abi.FunctionCall || custom.Kind != abi.FunctionCall || custom.ABI != flavor ||
					custom.Architecture != effect.Architecture || custom.ResultRegister != effect.ResultRegister ||
					custom.NumberRegister != "" || custom.RequiresCallStack != effect.RequiresCallStack ||
					custom.StackAlignment != effect.StackAlignment || len(custom.ArgumentRegisters) != len(effect.ArgumentRegisters) {
					return physicalProgram{}, 0, registerWrites{}, callEffectMismatch(target)
				}
				for i, name := range custom.ArgumentRegisters {
					if name != effect.ArgumentRegisters[i] {
						return physicalProgram{}, 0, registerWrites{}, callEffectMismatch(target)
					}
				}
				resultIncluded := false
				for _, name := range custom.ClobberedRegisters {
					if _, ok := physicalByName(name); !ok {
						return physicalProgram{}, 0, registerWrites{}, callEffectMismatch(target)
					}
					if name == custom.ResultRegister {
						resultIncluded = true
					}
				}
				if !resultIncluded {
					return physicalProgram{}, 0, registerWrites{}, callEffectMismatch(target)
				}
				effect = custom
			}
			var overwritten registerWrites
			for _, name := range effect.ClobberedRegisters {
				if reg, ok := physicalByName(name); ok {
					overwritten[reg] = true
				}
			}
			fixed := make([]string, 1, len(inputs))
			if ins.Op == mSyscall {
				fixed[0] = effect.NumberRegister
			} else {
				fixed[0] = target.RegisterName(target.CallTargetRegister())
			}
			fixed = append(fixed, effect.ArgumentRegisters...)
			fixedRegs := make([]physReg, len(fixed))
			for i, name := range fixed {
				reg, ok := physicalByName(name)
				fixedOutsidePool := (ins.Op == mCallC || ins.Op == mCallGo) && i == 0 && reg == target.CallTargetRegister()
				fixedOutsidePool = fixedOutsidePool || ins.Op == mCallGo && target.Architecture() == "riscv64" && i == 9 && reg == 8
				fixedOutsidePool = fixedOutsidePool || ins.Op == mCallGo && target.Architecture() == "loong64" && i >= 1 && reg >= 4 && reg <= 19
				fixedOutsidePool = fixedOutsidePool || target.Architecture() == "ppc64le" && (ins.Op == mSyscall && i == 0 && reg == 0 || ins.Op == mCallGo && i >= 9 && reg >= 14 && reg <= 17)
				if !ok || registerIndex(reg) < 0 && !fixedOutsidePool {
					return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
				}
				fixedRegs[i] = reg
				overwritten[reg] = true
			}
			// Preserve only values that remain live across registers the call
			// will destroy. Call operands themselves stay in their current homes
			// until the parallel move schedule has consumed them.
			seenClobbers := make(map[int]bool)
			for _, r := range registers {
				id := alloc.Owner(r)
				if id < 0 || seenClobbers[id] || !overwritten[r] {
					continue
				}
				seenClobbers[id] = true
				usesHere := 0
				for _, input := range inputs {
					if input == id {
						usesHere++
					}
				}
				if alloc.Remaining(id) > usesHere && !program.Values[id].Constant && locations[id].Slot < 0 {
					group := alloc.Group(id)
					// A move to a free register the call preserves replaces a
					// spill before the call and a reload after it. Pinning every
					// occupied or overwritten register lets Choose pick only a
					// free one, never an eviction.
					var unavailable regalloc.Pins
					for _, r := range registers {
						if alloc.Owner(r) >= 0 || overwritten[r] {
							unavailable.AddRegister(r)
						}
					}
					if preserved, err := alloc.Choose(id, unavailable); err == nil {
						mover.Move(id, preserved, group)
						alloc.Release(id)
						if err := alloc.Claim(id, preserved); err != nil {
							return physicalProgram{}, 0, registerWrites{}, err
						}
						continue
					}
					if err := alloc.Spill(id, group); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
				}
			}
			type registerMove struct {
				src, dst physReg
				slot     int // -1 for a register source
			}
			type deferredInput struct {
				id  int
				dst physReg
			}
			moves := make([]registerMove, 0, len(inputs))
			deferred := make([]deferredInput, 0, len(inputs))
			temporarySlots := make([]int, 0)
			for i, id := range inputs {
				if src, ok := alloc.Group(id).First(); ok {
					if src != fixedRegs[i] {
						moves = append(moves, registerMove{src: src, dst: fixedRegs[i], slot: -1})
					}
				} else {
					deferred = append(deferred, deferredInput{id: id, dst: fixedRegs[i]})
				}
			}
			// A destination is safe to write once no pending move reads its
			// old contents. A cycle uses a free register or one spill slot.
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
				if ready >= 0 {
					move := moves[ready]
					if move.slot >= 0 {
						emitter.reload(move.dst, move.slot)
					} else {
						emitter.move(move.dst, move.src)
					}
					written[move.dst] = true
					moves = append(moves[:ready], moves[ready+1:]...)
					continue
				}
				src := moves[0].src
				temporary := physReg(0)
				hasTemporary := false
				for _, candidate := range registers {
					if backend.IsVectorRegister(candidate) || alloc.Owner(candidate) != -1 {
						continue
					}
					used := false
					for _, fixedReg := range fixedRegs {
						used = used || candidate == fixedReg
					}
					for _, move := range moves {
						used = used || move.slot < 0 && candidate == move.src
					}
					if !used {
						temporary, hasTemporary = candidate, true
						break
					}
				}
				if hasTemporary {
					emitter.move(temporary, src)
					written[temporary] = true
					for i := range moves {
						if moves[i].slot < 0 && moves[i].src == src {
							moves[i].src = temporary
						}
					}
				} else {
					slot, err := alloc.AllocateSlot()
					if err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
					emitter.spill(src, slot)
					temporarySlots = append(temporarySlots, slot)
					for i := range moves {
						if moves[i].slot < 0 && moves[i].src == src {
							moves[i].slot = slot
						}
					}
				}
			}
			for _, slot := range temporarySlots {
				alloc.FreeSlots(slot, 1)
			}
			for _, input := range deferred {
				id := input.id
				if slot := locations[id].Slot; slot >= 0 {
					emitter.reload(input.dst, slot)
				} else if program.Values[id].Constant {
					if symbol := program.Values[id].Symbol; symbol != "" {
						emitter.symbol(input.dst, symbol)
					} else {
						emitter.constant(input.dst, program.Values[id].Imm)
					}
				} else {
					return physicalProgram{}, 0, registerWrites{}, ErrInvalidFunction
				}
				written[input.dst] = true
			}
			for _, r := range registers {
				id := alloc.Owner(r)
				if id >= 0 && overwritten[r] {
					alloc.Release(id)
				}
			}
			linkSlot := -1
			returnAddress := target.ReturnAddress()
			hasLink := returnAddress.InRegister()
			linkReg, linkIsGeneral := returnAddress.GeneralRegister()
			if kind == abi.FunctionCall && hasLink && !emitter.linkSavedInFrame() {
				linkSlot, err = alloc.AllocateSlot()
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				emitter.saveLink(linkSlot)
			}
			if ins.Op == mSyscall {
				emitter.syscall()
			} else if ins.Op == mCallC {
				emitter.callC(effect)
			} else {
				emitter.callGo()
			}
			for _, name := range effect.ClobberedRegisters {
				reg, ok := physicalByName(name)
				if ok && !(kind == abi.FunctionCall && hasLink && linkIsGeneral && reg == linkReg) {
					written[reg] = true
				}
			}
			if linkSlot >= 0 {
				emitter.restoreLink(linkSlot)
				alloc.FreeSlots(linkSlot, 1)
			}
			resultReg, ok := physicalByName(effect.ResultRegister)
			if !ok {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			if registerIndex(resultReg) < 0 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			if err := alloc.Claim(ins.Dst, regalloc.SingleRegister(resultReg)); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			if err := afterHomeCall(); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && ins.Op == mVector && ins.Bits == 64 && (ins.Arithmetic == ir.OpVSplat || ins.Arithmetic == ir.OpVExtract || ins.Arithmetic == ir.OpVInsertZero) {
			groups := make([]regalloc.Group, len(inputs))
			var pins regalloc.Pins
			for n, id := range inputs {
				g, err := alloc.Ensure(id, pins)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				groups[n] = g
				for w := 0; w < int(g.Words); w++ {
					pins.AddRegister(g.Registers[w])
				}
			}
			dst, err := alloc.Choose(ins.Dst, pins)
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			physical := physicalInsn{Op: machine.OpVector, Arithmetic: ins.Arithmetic, Bits: ins.Bits, VectorType: ins.Type, A: dst.Registers[0], B: groups[0].Registers[0], Imm: ins.Imm}
			if dst.Words == 2 {
				physical.HighA = dst.Registers[1]
			}
			if groups[0].Words == 2 {
				physical.HighB = groups[0].Registers[1]
			}
			if len(groups) > 1 {
				physical.C = groups[1].Registers[0]
				if groups[1].Words == 2 {
					physical.HighC = groups[1].Registers[1]
				}
			}
			emitter.vectorInstruction(physical)
			for w := 0; w < int(dst.Words); w++ {
				written[dst.Registers[w]] = true
			}
			if err := alloc.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && ins.Op == machine.SelectedFloatMove {
			src, err := alloc.Ensure(ins.A, regalloc.Pins{})
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			var pins regalloc.Pins
			for w := 0; w < int(src.Words); w++ {
				pins.AddRegister(src.Registers[w])
			}
			dst, err := alloc.Choose(ins.Dst, pins)
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			move := physicalInsn{Op: machine.OpFloatMove, Bits: ins.Bits, A: dst.Registers[0], B: src.Registers[0]}
			if src.Words == 2 {
				move.HighB = src.Registers[1]
			}
			if dst.Words == 2 {
				move.HighA = dst.Registers[1]
			}
			emitter.(*physicalEmitter).append(move)
			for w := 0; w < int(dst.Words); w++ {
				written[dst.Registers[w]] = true
			}
			if err := alloc.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && ins.Op == mSymbol && valueWords(ins.Dst) == 2 {
			g, err := alloc.Choose(ins.Dst, regalloc.Pins{})
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			lo, hi := g.Registers[0], g.Registers[1]
			emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpSymbol, Bits: 64, A: lo, HighA: hi, Symbol: ins.Symbol})
			written[lo], written[hi] = true, true
			if err := alloc.Claim(ins.Dst, g); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		workspacePair := target.Architecture() == "386" && emitter.(*physicalEmitter).stack == abi.StackNoUse
		pairedShift := ins.Op == mShift && valueWords(ins.Dst) == 2
		pairedSelect := ins.Op == mSelect && (valueWords(ins.Dst) == 2 || valueWords(ins.A) == 2)
		pairedMul := ins.Op == mBinary && ins.Bits == 64 && ins.Arithmetic == opMul
		wideAddress := target.Architecture() == "386" && (ins.Op == mLoad64 || ins.Op == mStore64) && valueWords(ins.A) == 2
		if workspacePair && (pairedShift || pairedSelect || pairedMul) || wideAddress {
			// Durable homes let each expansion stream words through only the
			// three volatile GPRs. No platform frame or hidden EBP scratch.
			for _, id := range inputs {
				if err := durable386(id); err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				alloc.Release(id)
			}
			if pairedMul {
				seen := map[int]bool{}
				for _, owner := range registers {
					id := alloc.Owner(owner)
					if id < 0 || seen[id] || program.Values[id].Class == backend.Float {
						continue
					}
					seen[id] = true
					if err := durable386(id); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
					alloc.Release(id)
				}
				lowSlot, err := alloc.AllocateSlot()
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				load386(0, ins.A, 0)
				load386(1, ins.B, 0)
				emitter.mulWide(1)
				emitter.spill(0, lowSlot)
				load386(0, ins.A, 1)
				emitter.binary(opMul, 32, 0, 0, 1)
				emitter.binary(opAdd, 32, 2, 2, 0)
				load386(0, ins.A, 0)
				load386(1, ins.B, 1)
				emitter.binary(opMul, 32, 0, 0, 1)
				emitter.binary(opAdd, 32, 2, 2, 0)
				emitter.reload(0, lowSlot)
				alloc.FreeSlots(lowSlot, 1)
				written[0], written[1], written[2] = true, true, true
				g := regalloc.Group{Registers: [2]physReg{0, 2}, Words: 2}
				if err := alloc.Claim(ins.Dst, g); err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
			} else {
				words := 2
				if ins.Op != mStore64 {
					words = valueWords(ins.Dst)
				}
				g, err := alloc.ChooseGroup(words, backend.General, regalloc.Pins{}, regalloc.Group{})
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				var pins regalloc.Pins
				for w := 0; w < words; w++ {
					pins.AddRegister(g.Registers[w])
				}
				lo := g.Registers[0]
				var hi, scratch physReg
				if words == 2 {
					hi = g.Registers[1]
					tmp, err := alloc.ChooseGroup(1, backend.General, pins, regalloc.Group{})
					if err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
					scratch = tmp.Registers[0]
				} else {
					tmp, err := alloc.ChooseGroup(2, backend.General, pins, regalloc.Group{})
					if err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
					hi, scratch = tmp.Registers[0], tmp.Registers[1]
				}
				switch {
				case wideAddress:
					load386(scratch, ins.A, 0)
					if ins.Op == mLoad64 {
						emitter.loadOffset(lo, scratch, 0, ins.ByteOrder)
						emitter.loadOffset(hi, scratch, 4, ins.ByteOrder)
					} else {
						load386(lo, ins.B, 0)
						load386(hi, ins.B, 1)
						emitter.storeOffset(scratch, lo, 0, ins.ByteOrder)
						emitter.storeOffset(scratch, hi, 4, ins.ByteOrder)
					}
				case pairedShift:
					load386(lo, ins.A, 0)
					load386(hi, ins.A, 1)
					emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpShift, Arithmetic: ins.Arithmetic, Bits: 64, A: lo, HighA: hi, B: lo, HighB: hi, C: scratch, Imm: ins.Imm})
				case pairedSelect:
					load386(scratch, ins.A, 0)
					if valueWords(ins.A) == 2 {
						load386(lo, ins.A, 1)
						emitter.binary(opOr, 32, scratch, scratch, lo)
					}
					emitter.constant(lo, 0xffffffff)
					emitter.selectValue(scratch, scratch, lo, scratch) // 0 or all ones
					load386(lo, ins.B, 0)
					load386(hi, ins.C, 0)
					emitter.binary(opXor, 32, lo, lo, hi)
					emitter.binary(opAnd, 32, lo, lo, scratch)
					emitter.binary(opXor, 32, lo, lo, hi)
					if words == 2 {
						slot, err := alloc.AllocateSlot()
						if err != nil {
							return physicalProgram{}, 0, registerWrites{}, err
						}
						emitter.spill(lo, slot)
						load386(hi, ins.B, 1)
						load386(lo, ins.C, 1)
						emitter.binary(opXor, 32, hi, hi, lo)
						emitter.binary(opAnd, 32, hi, hi, scratch)
						emitter.binary(opXor, 32, hi, hi, lo)
						emitter.reload(lo, slot)
						alloc.FreeSlots(slot, 1)
					}
				}
				written[lo], written[hi], written[scratch] = true, true, true
				if ins.Op != mStore64 {
					if err := alloc.Claim(ins.Dst, g); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
				}
			}
			emitter.forgetFlags()
			alloc.Consume(inputs)
			if ins.Op != mStore64 && alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && ins.Op == mShift && valueWords(ins.Dst) == 2 {
			if emitter.(*physicalEmitter).stack != abi.StackPlatform {
				return physicalProgram{}, 0, registerWrites{}, fmt.Errorf("386 paired immediate shift needs preserved GP scratch registers: %w", ErrStackRequired)
			}
			var pinned regalloc.Pins
			source, err := alloc.Ensure(ins.A, pinned)
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			if source.Words != 2 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			pinned.AddRegister(source.Registers[0])
			pinned.AddRegister(source.Registers[1])
			rotation := ins.Arithmetic == opRotateLeft || ins.Arithmetic == opRotateRight
			dst := regalloc.Group{}
			if alloc.Remaining(ins.A) == 1 && !rotation {
				dst = source
			} else {
				dst, err = alloc.Choose(ins.Dst, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
			}
			if dst.Words != 2 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			low, high := source.Registers[0], source.Registers[1]
			outLow, outHigh := dst.Registers[0], dst.Registers[1]
			count := uint8(ins.Imm)
			scratch := target.ScratchRegister()
			shiftOrMove := func(operation opcode, dst, src physReg, amount uint8) {
				if amount == 0 {
					emitter.move(dst, src)
				} else {
					emitter.shift(operation, 32, dst, src, amount)
				}
			}
			switch ins.Arithmetic {
			case opShiftLeft:
				if count == 0 {
					emitter.move(outLow, low)
					emitter.move(outHigh, high)
				} else if count < 32 {
					emitter.shift(opShiftRight, 32, scratch, low, 32-count)
					emitter.shift(opShiftLeft, 32, outHigh, high, count)
					emitter.binary(opOr, 32, outHigh, outHigh, scratch)
					emitter.shift(opShiftLeft, 32, outLow, low, count)
					written[scratch] = true
				} else {
					shiftOrMove(opShiftLeft, outHigh, low, count-32)
					emitter.constant(outLow, 0)
				}
			case opShiftRight, opShiftRightArithmetic:
				if count == 0 {
					emitter.move(outLow, low)
					emitter.move(outHigh, high)
				} else if count < 32 {
					emitter.shift(opShiftLeft, 32, scratch, high, 32-count)
					emitter.shift(opShiftRight, 32, outLow, low, count)
					emitter.binary(opOr, 32, outLow, outLow, scratch)
					emitter.shift(ins.Arithmetic, 32, outHigh, high, count)
					written[scratch] = true
				} else {
					shiftOrMove(ins.Arithmetic, outLow, high, count-32)
					if ins.Arithmetic == opShiftRightArithmetic {
						emitter.shift(opShiftRightArithmetic, 32, outHigh, high, 31)
					} else {
						emitter.constant(outHigh, 0)
					}
				}
			case opRotateLeft, opRotateRight:
				if count >= 32 {
					low, high = high, low
					count -= 32
				}
				if count == 0 {
					emitter.move(outLow, low)
					emitter.move(outHigh, high)
				} else if ins.Arithmetic == opRotateLeft {
					emitter.shift(opShiftRight, 32, scratch, high, 32-count)
					emitter.shift(opShiftLeft, 32, outLow, low, count)
					emitter.binary(opOr, 32, outLow, outLow, scratch)
					emitter.shift(opShiftRight, 32, scratch, low, 32-count)
					emitter.shift(opShiftLeft, 32, outHigh, high, count)
					emitter.binary(opOr, 32, outHigh, outHigh, scratch)
					written[scratch] = true
				} else {
					emitter.shift(opShiftLeft, 32, scratch, high, 32-count)
					emitter.shift(opShiftRight, 32, outLow, low, count)
					emitter.binary(opOr, 32, outLow, outLow, scratch)
					emitter.shift(opShiftLeft, 32, scratch, low, 32-count)
					emitter.shift(opShiftRight, 32, outHigh, high, count)
					emitter.binary(opOr, 32, outHigh, outHigh, scratch)
					written[scratch] = true
				}
			default:
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			emitter.forgetFlags()
			written[outLow], written[outHigh] = true, true
			if dst.Contains(source.Registers[0]) {
				alloc.Release(ins.A)
			}
			if err := alloc.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && (ins.Op == mLoad64 || ins.Op == mStore64) {
			var pinned regalloc.Pins
			address, err := alloc.Ensure(ins.A, pinned)
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			if address.Words != 1 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			pinned.Add(address)
			addressReg := address.Registers[0]
			if ins.Op == mStore64 {
				value, err := alloc.Ensure(ins.B, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if value.Words != 2 {
					return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
				}
				emitter.storeOffset(addressReg, value.Registers[0], 0, ins.ByteOrder)
				emitter.storeOffset(addressReg, value.Registers[1], 4, ins.ByteOrder)
				alloc.Consume(inputs)
				continue
			}
			dst, err := alloc.Choose(ins.Dst, pinned)
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			if dst.Words != 2 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			low, high := dst.Registers[0], dst.Registers[1]
			emitter.loadOffset(low, addressReg, 0, ins.ByteOrder)
			emitter.loadOffset(high, addressReg, 4, ins.ByteOrder)
			written[low], written[high] = true, true
			if err := alloc.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && (ins.Op == mStore8 || ins.Op == mLoad8) {
			// Keep the established allocation (and its listing) whenever both
			// complete groups can fit. Otherwise the old byte path necessarily
			// fails: it pins the whole address before reserving the value/result.
			// A byte access only needs the low address/value word, so stream those
			// words without claiming incomplete SSA groups.
			words := valueWords(ins.A)
			if ins.Op == mStore8 {
				if ins.B != ins.A {
					words += valueWords(ins.B)
				}
			} else {
				words += valueWords(ins.Dst)
			}
			available := 0
			for _, reg := range registers {
				if !backend.IsVectorRegister(reg) {
					available++
				}
			}
			if words > available {
				var pinned regalloc.Pins
				lowWord := func(id int) (physReg, error) {
					if group := alloc.Group(id); group.Words != 0 {
						return group.Registers[0], nil
					}
					group, err := alloc.ChooseGroup(1, backend.General, pinned, regalloc.Group{})
					if err != nil {
						return 0, err
					}
					if !program.Values[id].Constant && locations[id].Slot < 0 && locations[id].IncomingWords == 0 {
						return 0, fmt.Errorf("value %d has no low-word source: %w", id, ErrInvalidFunction)
					}
					if program.Values[id].Constant && program.Values[id].Symbol != "" {
						emitter.symbol(group.Registers[0], program.Values[id].Symbol)
						written[group.Registers[0]] = true
					} else {
						load386(group.Registers[0], id, 0)
					}
					return group.Registers[0], nil
				}
				address, err := lowWord(ins.A)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				pinned.AddRegister(address)
				if ins.Op == mStore8 {
					value, err := lowWord(ins.B)
					if err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
					if value >= 4 {
						return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
					}
					emitter.store8(address, value)
				} else {
					if alloc.Remaining(ins.A) == 1 {
						// The low address register remains pinned for this access,
						// but its dead high word needs neither a register nor a spill.
						alloc.Release(ins.A)
					}
					words := valueWords(ins.Dst)
					if alloc.Remaining(ins.Dst) == 0 {
						words = 1
						// A discarded byte load still performs the access. Keep any
						// live address group intact while taking one scratch register.
						group := alloc.Group(ins.A)
						for word := 0; word < int(group.Words); word++ {
							pinned.AddRegister(group.Registers[word])
						}
					}
					dst, err := alloc.ChooseGroup(words, backend.General, pinned, regalloc.Group{})
					if err != nil && alloc.Remaining(ins.Dst) != 0 && alloc.Group(ins.A).Words == 2 {
						// The address stays live after the load, and both of its
						// words occupy registers the result needs. Give it a memory
						// home and keep only the low word for this access.
						if err := durable386(ins.A); err != nil {
							return physicalProgram{}, 0, registerWrites{}, err
						}
						alloc.Release(ins.A)
						pinned = regalloc.Pins{}
						if address, err = lowWord(ins.A); err != nil {
							return physicalProgram{}, 0, registerWrites{}, err
						}
						pinned.AddRegister(address)
						dst, err = alloc.ChooseGroup(words, backend.General, pinned, regalloc.Group{})
					}
					if err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
					low := dst.Registers[0]
					emitter.load8(low, address)
					written[low] = true
					if dst.Words == 2 {
						high := dst.Registers[1]
						emitter.constant(high, 0)
						written[high] = true
					}
					if alloc.Remaining(ins.Dst) != 0 {
						if err := alloc.Claim(ins.Dst, dst); err != nil {
							return physicalProgram{}, 0, registerWrites{}, err
						}
					}
				}
				alloc.Consume(inputs)
				if ins.Op == mLoad8 && alloc.Remaining(ins.Dst) == 0 {
					alloc.Release(ins.Dst)
				}
				continue
			}
		}
		if target.Architecture() == "386" &&
			(ins.Op == mStore8 || ins.Op == mScaled && valueWords(ins.Dst) == 2 || ins.Op == mLoad8 && (valueWords(ins.Dst) == 2 || valueWords(ins.A) == 2)) {
			var pinned regalloc.Pins
			address, err := alloc.Ensure(ins.A, pinned)
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			if address.Words != 2 && !((ins.Op == mStore8 || ins.Op == mLoad8) && address.Words == 1) {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			for word := 0; word < int(address.Words); word++ {
				pinned.AddRegister(address.Registers[word])
			}
			if ins.Op == mStore8 {
				value, err := alloc.Ensure(ins.B, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if value.Words != 1 && value.Words != 2 {
					return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
				}
				addrReg := address.Registers[0]
				valueReg := value.Registers[0]
				if valueReg < 4 {
					emitter.store8(addrReg, valueReg)
				} else {
					// An x86 byte store can only name AL, CL, DL, or BL.
					// Borrow one of those registers through the spill scheduler.
					byteReg := physReg(0)
					if addrReg == byteReg {
						byteReg = 1
					}
					slot, err := alloc.AllocateSlot()
					if err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
					emitter.spill(byteReg, slot)
					emitter.move(byteReg, valueReg)
					emitter.store8(addrReg, byteReg)
					emitter.reload(byteReg, slot)
					alloc.FreeSlots(slot, 1)
				}
				alloc.Consume(inputs)
				continue
			}
			if ins.Op == mScaled {
				index, err := alloc.Ensure(ins.B, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if index.Words != 2 {
					return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
				}
				pinned.Add(index)
				dst, err := alloc.Choose(ins.Dst, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if dst.Words != 2 {
					return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
				}
				baseLow, baseHigh := address.Registers[0], address.Registers[1]
				indexLow, indexHigh := index.Registers[0], index.Registers[1]
				dstLow, dstHigh := dst.Registers[0], dst.Registers[1]
				if ins.Imm == 1 {
					emitter.binary(opAdd, 32, dstLow, baseLow, indexLow)
					emitter.binaryCarry(opAdd, dstHigh, baseHigh, indexHigh)
				} else {
					shift := uint8(0)
					switch ins.Imm {
					case 2:
						shift = 1
					case 4:
						shift = 2
					case 8:
						shift = 3
					}
					emitter.shift(opShiftLeft, 32, dstHigh, indexHigh, shift)
					emitter.shift(opShiftRight, 32, dstLow, indexLow, 32-shift)
					emitter.binary(opOr, 32, dstHigh, dstHigh, dstLow)
					emitter.shift(opShiftLeft, 32, dstLow, indexLow, shift)
					emitter.binary(opAdd, 32, dstLow, dstLow, baseLow)
					emitter.binaryCarry(opAdd, dstHigh, dstHigh, baseHigh)
				}
				written[dstLow], written[dstHigh] = true, true
				if err := alloc.Claim(ins.Dst, dst); err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
			} else {
				dst, err := alloc.Choose(ins.Dst, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if dst.Words != 1 && dst.Words != 2 {
					return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
				}
				low := dst.Registers[0]
				emitter.load8(low, address.Registers[0])
				written[low] = true
				if dst.Words == 2 {
					high := dst.Registers[1]
					emitter.constant(high, 0)
					written[high] = true
				}
				if err := alloc.Claim(ins.Dst, dst); err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && ins.Op == mSelect && (valueWords(ins.Dst) == 2 || valueWords(ins.A) == 2) {
			if emitter.(*physicalEmitter).stack != abi.StackPlatform {
				return physicalProgram{}, 0, registerWrites{}, fmt.Errorf("386 paired select needs preserved GP scratch registers: %w", ErrStackRequired)
			}

			for _, id := range inputs {
				group, err := alloc.Ensure(id, regalloc.Pins{})
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if !program.Values[id].Constant && locations[id].Slot < 0 {
					if err := alloc.Spill(id, group); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
				}
				alloc.Release(id)
			}
			// Materialize input homes before reusing the fixed scratch registers.
			seen := make(map[int]bool)
			for _, owner := range registers {
				id := alloc.Owner(owner)
				if id < 0 || seen[id] || program.Values[id].Class == backend.Float {
					continue
				}
				seen[id] = true
				group := alloc.Group(id)
				if !program.Values[id].Constant && locations[id].Slot < 0 {
					if err := alloc.Spill(id, group); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
				}
				alloc.Release(id)
			}
			loadWord := func(reg physReg, id, word int) {
				if program.Values[id].Constant {
					emitter.constant(reg, uint64(uint32(program.Values[id].Imm>>(word*32))))
				} else {
					emitter.reload(reg, locations[id].Slot+word)
				}
				written[reg] = true
			}
			loadWord(0, ins.A, 0)
			if valueWords(ins.A) == 2 {
				loadWord(1, ins.A, 1)
				emitter.binary(opOr, 32, 0, 0, 1)
			}
			loadWord(3, ins.B, 0)
			loadWord(1, ins.C, 0)
			emitter.selectValue(2, 0, 3, 1)
			written[2] = true
			result := regalloc.SingleRegister(2)
			if valueWords(ins.Dst) == 2 {
				loadWord(3, ins.B, 1)
				loadWord(1, ins.C, 1)
				emitter.selectValue(6, 0, 3, 1)
				written[6] = true
				result = regalloc.Group{Registers: [2]physReg{2, 6}, Words: 2}
			}
			emitter.forgetFlags()
			if registerIndex(2) < 0 || result.Words == 2 && registerIndex(6) < 0 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			if err := alloc.Claim(ins.Dst, result); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && (ins.Op == mDivide && valueWords(ins.Dst) == 2 ||
			ins.Op == mIntToFloat && valueWords(ins.A) == 2 || ins.Op == mFloatToInt && valueWords(ins.Dst) == 2) {
			if err := allocate386Wide(ins, alloc, emitter.(*physicalEmitter), written, inputs); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			continue
		}
		if target.Architecture() == "386" && ins.Op == mShiftBy {
			// The paired expansion uses EAX:EDX, CL and EBX. Give operands
			// homes before assigning those fixed locations, through the same
			// scheduler as ordinary pressure spills.
			for _, id := range inputs {
				group, err := alloc.Ensure(id, regalloc.Pins{})
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if !program.Values[id].Constant && locations[id].Slot < 0 && locations[id].IncomingWords == 0 {
					if err := alloc.Spill(id, group); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
				}
			}
			seen := map[int]bool{}
			for _, r := range registers {
				id := alloc.Owner(r)
				if id < 0 || seen[id] || r > 3 {
					continue
				}
				seen[id] = true
				group := alloc.Group(id)
				if !program.Values[id].Constant && locations[id].Slot < 0 && locations[id].IncomingWords == 0 {
					if err := alloc.Spill(id, group); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
				}
				alloc.Release(id)
			}
			load := func(reg physReg, id, word int) {
				if word >= valueWords(id) {
					emitter.constant(reg, 0)
				} else if program.Values[id].Constant {
					emitter.constant(reg, uint64(uint32(program.Values[id].Imm>>(32*word))))
				} else if locations[id].Slot >= 0 {
					emitter.reload(reg, locations[id].Slot+word)
				} else {
					emitter.incomingWord(reg, locations[id].Incoming[word])
				}
				written[reg] = true
			}
			load(0, ins.A, 0)
			load(2, ins.A, 1)
			load(1, ins.B, 0)
			load(3, ins.B, 1)
			emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpShiftBy, Arithmetic: ins.Arithmetic, Bits: ins.Bits, A: 0, B: 0, C: 1, D: 3, E: 2})
			result := regalloc.SingleRegister(0)
			if valueWords(ins.Dst) == 2 {
				result = regalloc.Group{Registers: [2]physReg{0, 2}, Words: 2}
			}
			if err := alloc.Claim(ins.Dst, result); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}

		if target.Architecture() == "386" && ins.Op == mBinary && ins.Bits == 64 && ins.Arithmetic == opMul {
			if emitter.(*physicalEmitter).stack != abi.StackPlatform {
				return physicalProgram{}, 0, registerWrites{}, fmt.Errorf("386 paired multiply needs preserved GP scratch registers: %w", ErrStackRequired)
			}
			// MUL fixes the low product in EAX:EDX. Give both inputs durable
			// homes before writing those registers, including when a value is
			// also live after this multiplication.
			var pinned regalloc.Pins
			for _, id := range inputs {
				group, err := alloc.Ensure(id, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if group.Words != 2 {
					return physicalProgram{}, 0, registerWrites{}, ErrInvalidFunction
				}
				pinned.AddRegister(group.Registers[0])
				pinned.AddRegister(group.Registers[1])
			}
			seen := make(map[int]bool)
			for _, owner := range registers {
				id := alloc.Owner(owner)
				if id < 0 || seen[id] || program.Values[id].Class == backend.Float {
					continue
				}
				seen[id] = true
				group := alloc.Group(id)
				displaced := id == ins.A || id == ins.B
				for word := 0; word < int(group.Words); word++ {
					displaced = displaced || group.Registers[word] <= 3
				}
				if !displaced {
					continue
				}
				if !program.Values[id].Constant && locations[id].Slot < 0 {
					if err := alloc.Spill(id, group); err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
				}
				alloc.Release(id)
			}
			// Only the low 64 bits are needed:
			// a*b = a0*b0 + (a1*b0+a0*b1)<<32 (mod 2^64).
			loadWord := func(reg physReg, id, word int) {
				if program.Values[id].Constant {
					emitter.constant(reg, uint64(uint32(program.Values[id].Imm>>(word*32))))
				} else {
					emitter.reload(reg, locations[id].Slot+word)
				}
				written[reg] = true
			}
			loadWord(0, ins.A, 0)
			loadWord(3, ins.B, 0)
			emitter.mulWide(3) // EDX:EAX = a0*b0
			loadWord(1, ins.A, 1)
			emitter.binary(opMul, 32, 1, 1, 3)
			emitter.binary(opAdd, 32, 2, 2, 1)
			loadWord(1, ins.A, 0)
			loadWord(3, ins.B, 1)
			emitter.binary(opMul, 32, 1, 1, 3)
			emitter.binary(opAdd, 32, 2, 2, 1)
			written[0], written[2] = true, true
			emitter.forgetFlags()
			result := regalloc.Group{Registers: [2]physReg{0, 2}, Words: 2}
			if registerIndex(0) < 0 || registerIndex(2) < 0 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			if err := alloc.Claim(ins.Dst, result); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}

		if target.Architecture() == "386" && ins.Op == mMove && valueWords(ins.Dst) == 2 && valueWords(ins.A) == 2 {
			source, err := alloc.Ensure(ins.A, regalloc.Pins{})
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			dst := source
			if alloc.Remaining(ins.A) > 1 {
				if locations[ins.A].Slot < 0 && !program.Values[ins.A].Constant {
					slot, err := alloc.AllocateValueSlot(ins.A)
					if err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
					locations[ins.A].Slot, locations[ins.A].SlotWords = slot, 2
					spillValue(ins.A, slot, source)
				}
				alloc.Release(ins.A)
				dst, err = alloc.Choose(ins.Dst, regalloc.Pins{})
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				for word := 0; word < 2; word++ {
					reg := dst.Registers[word]
					if program.Values[ins.A].Constant {
						emitter.constant(reg, uint64(uint32(program.Values[ins.A].Imm>>(32*word))))
					} else {
						emitter.reload(reg, locations[ins.A].Slot+word)
					}
					written[reg] = true
				}
			} else {
				alloc.Release(ins.A)
			}
			if err := alloc.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && ins.Dst >= 0 && valueWords(ins.Dst) == 2 &&
			(ins.Op == mSignExtend || ins.Op == mMove && valueWords(ins.A) == 1) {
			var pinned regalloc.Pins
			source, err := alloc.Ensure(ins.A, pinned)
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			for word := 0; word < int(source.Words); word++ {
				pinned.AddRegister(source.Registers[word])
			}
			if source.Words == 2 && alloc.Remaining(ins.A) > 1 {
				// Only the low word is read by sign extension. Keep the live
				// snapshot in its slot instead of pinning another full pair.
				if locations[ins.A].Slot < 0 && !program.Values[ins.A].Constant {
					slot, err := alloc.AllocateValueSlot(ins.A)
					if err != nil {
						return physicalProgram{}, 0, registerWrites{}, err
					}
					locations[ins.A].Slot, locations[ins.A].SlotWords = slot, 2
					spillValue(ins.A, slot, source)
				}
				alloc.Release(ins.A)
				pinned.Remove(source)
			}
			dst := regalloc.Group{}
			if source.Words == 2 && alloc.Remaining(ins.A) == 1 {
				dst = source
			} else {
				dst, err = alloc.Choose(ins.Dst, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
			}
			if dst.Words != 2 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			low, high := dst.Registers[0], dst.Registers[1]
			input := source.Registers[0]
			if ins.Op == mSignExtend {
				emitter.signExtend(low, input, uint8(ins.Imm))
				emitter.shift(opShiftRightArithmetic, 32, high, low, 31)
				emitter.forgetFlags()
			} else {
				emitter.move(low, input)
				emitter.constant(high, 0)
			}
			written[low], written[high] = true, true
			if dst.Contains(source.Registers[0]) {
				alloc.Release(ins.A)
			}
			if err := alloc.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && ins.Op == mBinary &&
			ins.Bits == 64 && valueWords(ins.Dst) == 2 &&
			(ins.Arithmetic == opAdd || ins.Arithmetic == opSub || ins.Arithmetic == opAnd || ins.Arithmetic == opOr || ins.Arithmetic == opXor) {
			// Paired operands plus a separate result may exceed the free GPRs,
			// even in a platform frame when variable homes occupy registers. Give each input a
			// complete home, then process one word at a time using a result pair
			// and one temporary. MOV spills/reloads preserve the low-word carry.
			for _, id := range inputs {
				if program.Values[id].Constant || locations[id].Slot >= 0 {
					continue
				}
				group, err := alloc.Ensure(id, regalloc.Pins{})
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				slot, err := alloc.AllocateValueSlot(id)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				locations[id].Slot, locations[id].SlotWords = slot, 2
				spillValue(id, slot, group)
			}
			dst, err := alloc.Choose(ins.Dst, regalloc.Pins{})
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			var pinned regalloc.Pins
			pinned.AddRegister(dst.Registers[0])
			pinned.AddRegister(dst.Registers[1])
			temp, err := alloc.ChooseGroup(1, backend.General, pinned, regalloc.Group{})
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			right := temp.Registers[0]
			loadWord := func(reg physReg, id, word int) {
				if program.Values[id].Constant {
					emitter.constant(reg, uint64(uint32(program.Values[id].Imm>>(32*word))))
				} else {
					emitter.reload(reg, locations[id].Slot+word)
				}
				written[reg] = true
			}
			for word := 0; word < 2; word++ {
				reg := dst.Registers[word]
				loadWord(reg, ins.A, word)
				loadWord(right, ins.B, word)
				if word == 1 && (ins.Arithmetic == opAdd || ins.Arithmetic == opSub) {
					emitter.binaryCarry(ins.Arithmetic, reg, reg, right)
				} else {
					emitter.binary(ins.Arithmetic, 32, reg, reg, right)
				}
			}
			emitter.forgetFlags()
			if err := alloc.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		if ins.Dst >= 0 && valueWords(ins.Dst) == 2 && (ins.Op == mMove ||
			ins.Op == mBinary && ins.Bits == 64 &&
				(ins.Arithmetic == opAdd || ins.Arithmetic == opSub || ins.Arithmetic == opAnd || ins.Arithmetic == opOr || ins.Arithmetic == opXor)) {
			groups := make([]regalloc.Group, len(inputs))
			var pinned regalloc.Pins
			for i, id := range inputs {
				group, err := alloc.Ensure(id, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				if group.Words != 2 {
					return physicalProgram{}, 0, registerWrites{}, ErrInvalidFunction
				}
				groups[i] = group
				for word := 0; word < 2; word++ {
					pinned.AddRegister(group.Registers[word])
				}
			}
			usesHere := func(id int) int {
				count := 0
				for _, input := range inputs {
					if input == id {
						count++
					}
				}
				return count
			}
			dstGroup := regalloc.Group{}
			if alloc.Remaining(ins.A) == usesHere(ins.A) && target.CanReuse(ins, 0) {
				dstGroup = groups[0]
			} else if ins.Op == mBinary && ins.Arithmetic != opSub && alloc.Remaining(ins.B) == usesHere(ins.B) {
				// All supported operations except subtraction are commutative.
				dstGroup = groups[1]
				groups[0], groups[1] = groups[1], groups[0]
			} else {
				var err error
				dstGroup, err = alloc.Choose(ins.Dst, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
			}
			if dstGroup.Words != 2 {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			for word := 0; word < 2; word++ {
				dst := dstGroup.Registers[word]
				left := groups[0].Registers[word]
				if ins.Op == mMove {
					if dst != left {
						emitter.move(dst, left)
						written[dst] = true
					}
				} else {
					right := groups[1].Registers[word]
					if word == 1 && (ins.Arithmetic == opAdd || ins.Arithmetic == opSub) {
						emitter.binaryCarry(ins.Arithmetic, dst, left, right)
					} else {
						emitter.binary(ins.Arithmetic, target.RegisterBits(), dst, left, right)
					}
					written[dst] = true
				}
			}
			if ins.Op == mBinary {
				emitter.forgetFlags()
			}
			for _, id := range inputs {
				if alloc.Group(id).Contains(dstGroup.Registers[0]) {
					alloc.Release(id)
				}
			}
			if err := alloc.Claim(ins.Dst, dstGroup); err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			alloc.Consume(inputs)
			if alloc.Remaining(ins.Dst) == 0 {
				alloc.Release(ins.Dst)
			}
			continue
		}
		var pinned regalloc.Pins
		// Instruction-local fixed registers participate in allocation. Relocate
		// their live owners before loading operands; the normal spill scheduler
		// supplies durable homes when register pressure forces an eviction.
		if constraints, ok := target.(interface {
			FixedClobbers(machineInstruction) []physReg
		}); ok {
			fixed := constraints.FixedClobbers(ins)
			for _, reg := range fixed {
				pinned.AddRegister(reg)
			}
			for _, reg := range fixed {
				id := alloc.Owner(reg)
				if id < 0 {
					continue
				}
				old := alloc.Group(id)
				group, err := alloc.Choose(id, pinned)
				if err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
				for word := 0; word < int(old.Words); word++ {
					to, from := group.Registers[word], old.Registers[word]
					emitter.move(to, from)
					written[to] = true
				}
				alloc.Release(id)
				if err := alloc.Claim(id, group); err != nil {
					return physicalProgram{}, 0, registerWrites{}, err
				}
			}
			for _, reg := range fixed {
				written[reg] = true
			}
		}
		var inputRegisterBuffer [4]physReg
		inputRegs := inputRegisterBuffer[:len(inputs)]
		selectFromFlags := false
		if ins.Op == mSelect && target.Architecture() == "arm64" {
			flags := emitter.currentFlags()
			selectFromFlags = flags.Zero.Valid && flags.Zero.Value == ins.A
		}
		for i, id := range inputs {
			if selectFromFlags && i == 0 {
				continue
			}
			group, err := alloc.Ensure(id, pinned)
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			if group.Words != 1 && !(target.RegisterBits() == 32 && ((ins.Op == mNarrow && i == 0 && valueWords(ins.Dst) == 1) || ins.Op == mLoad16 || ins.Op == mLoad32 || ins.Op == mStore16 || ins.Op == mStore32)) {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			for word := 0; word < int(group.Words); word++ {
				pinned.AddRegister(group.Registers[word])
			}
			inputRegs[i] = group.Registers[0]
		}
		if ins.Op == mVector && ins.Arithmetic == ir.OpVStore {
			physical := machine.Instruction{Op: machine.OpVector, Arithmetic: ins.Arithmetic, Bits: ins.Bits, VectorType: ins.Type, A: inputRegs[0], B: inputRegs[1], Disp: ins.Disp, Indexed: ins.Indexed}
			if ins.Indexed {
				physical.C = inputRegs[2]
			}
			emitter.vectorInstruction(physical)
			alloc.Consume(inputs)
			continue
		}
		if ins.Op == mStore64 || ins.Op == mStore16 || ins.Op == mStore32 || ins.Op == mStore8 || ins.Op == mStoreAddress || ins.Op == mStoreOffset {
			if ins.Op == mStoreAddress {
				emitter.storeAddress(inputRegs[0], inputRegs[1], inputRegs[2], ins.Imm, ins.Disp, ins.ByteOrder)
			} else if ins.Op == mStoreOffset {
				emitter.storeOffset(inputRegs[0], inputRegs[1], ins.Disp, ins.ByteOrder)
			} else if ins.Op == mStore16 || ins.Op == mStore32 {
				emitter.narrowMemory(false, ins.Bits, inputRegs[0], inputRegs[1], ins.ByteOrder)
			} else if ins.Op == mStore8 {
				emitter.store8(inputRegs[0], inputRegs[1])
			} else {
				emitter.store64(inputRegs[0], inputRegs[1], ins.ByteOrder)
			}
			alloc.Consume(inputs)
			continue
		}
		var dst physReg
		chosen := false
		if hint, ok := alloc.Preference(ins.Dst); ok && !pinned.Contains(hint) && alloc.Owner(hint) == -1 && (!homeReserved(hint) || int(hint) == allowedHome) &&
			backend.IsVectorRegister(hint) == (program.Values[ins.Dst].Class == backend.Float || program.Values[ins.Dst].Class == backend.Vector) {
			dst, chosen = hint, true
		}
		var seenInputs regalloc.Pins
		for inputPos, id := range inputs {
			if chosen {
				break
			}
			first, resident := alloc.Group(id).First()
			if resident && seenInputs.Contains(first) || program.Values[id].Class != program.Values[ins.Dst].Class || !(target.CanReuse(ins, inputPos) || program.Values[ins.Dst].Class == backend.Float && (fpInstruction(ins) || ins.Op == machine.SelectedFloatMove) && (target.Architecture() != "amd64" && target.Architecture() != "386" || inputPos == 0)) {
				continue
			}
			if resident {
				seenInputs.AddRegister(first)
			}
			usesHere := 0
			for _, input := range inputs {
				if input == id {
					usesHere++
				}
			}
			if alloc.Remaining(id) == usesHere && alloc.Group(id).Words == 1 && (!homeReserved(first) || int(first) == allowedHome) {
				dst, chosen = first, true
				break
			}
		}
		if !chosen {
			var err error
			var group regalloc.Group
			group, err = alloc.Choose(ins.Dst, pinned)
			if err != nil {
				// A destructive instruction can reuse a still-live SSA operand
				// after giving that snapshot a durable home. Variable homes remain
				// reserved; their contents cannot be overwritten by this fallback.
				reused := false
				for inputPos, id := range inputs {
					if !target.CanReuse(ins, inputPos) || program.Values[id].Class != program.Values[ins.Dst].Class {
						continue
					}
					source := alloc.Group(id)
					if source.Words != 1 && !(ins.Op == mNarrow && source.Words == 2) {
						continue
					}
					reg := source.Registers[0]
					if homeReserved(reg) && int(reg) != allowedHome {
						continue
					}
					if !program.Values[id].Constant && locations[id].Slot < 0 {
						slot, saveErr := alloc.AllocateValueSlot(id)
						if saveErr != nil {
							return physicalProgram{}, 0, registerWrites{}, saveErr
						}
						locations[id].Slot, locations[id].SlotWords = slot, uint8(storageWords(id))
						spillValue(id, slot, source)
					}
					group = regalloc.SingleRegister(source.Registers[0])
					reused = true
					break
				}
				if !reused {
					return physicalProgram{}, 0, registerWrites{}, fmt.Errorf("allocate result for selected operation %v/%v at %d: %w", ins.Op, ins.Arithmetic, position, err)
				}
			}
			if group.Words != 1 {
				return physicalProgram{}, 0, registerWrites{}, fmt.Errorf("scalar operation selected a %d-word result: %w", group.Words, ErrBackendLimit)
			}
			dst = group.Registers[0]
		}
		floatOperand := func(id, position int) physReg {
			if ins.LiteralMask&(1<<position) == 0 {
				return alloc.Group(id).Registers[0]
			}
			n := uint8(position)
			if target.Architecture() == "amd64" && ins.Op == mFloatFMA {
				n = []uint8{1, 2, 0}[position]
			}
			if target.Architecture() == "arm64" {
				n += 16
			}
			reg := backend.VectorRegister(n)
			width := ins.Bits
			if ins.Op == mFloatUnary && ins.Arithmetic == ir.OpFConvert {
				width = 96 - width
			}
			emitter.(*physicalEmitter).literalValue(reg, width, program.Values[id].Imm)
			return reg
		}
		if err := emitSelectedOperation(emitter, target, program.Values, &ins, dst, inputRegs, selectFromFlags, written, floatOperand); err != nil {
			return physicalProgram{}, 0, registerWrites{}, err
		}
		if !backend.IsVectorRegister(dst) && (ins.Op != mMove || dst != inputRegs[0]) {
			written[dst] = true
		}
		if oldID := alloc.Owner(dst); oldID >= 0 {
			alloc.Release(oldID)
		}
		if err := alloc.Claim(ins.Dst, regalloc.SingleRegister(dst)); err != nil {
			return physicalProgram{}, 0, registerWrites{}, err
		}
		alloc.Consume(inputs)
		if alloc.Remaining(ins.Dst) == 0 {
			alloc.Release(ins.Dst)
		}
	}
	exportSlots := make([]int, len(program.Exports))
	if program.DirectExports {
		exportSlots = nil
	}
	if !program.DirectExports {
		for i, id := range program.Exports {
			group, err := alloc.Ensure(id, regalloc.Pins{})
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			base, err := alloc.AppendSlots(int(group.Words))
			if err != nil {
				return physicalProgram{}, 0, registerWrites{}, err
			}
			exportSlots[i] = base
			for word := 0; word < int(group.Words); word++ {
				emitter.spill(group.Registers[word], exportSlots[i]+word)
			}
		}
	}
	if program.DiscardResult {
		slots := referencedSpillSlots(emitter.(*physicalEmitter).machineStream().Instructions(), target.RegisterBits())
		output, err := emitter.bodyBare(target.ResultRegister(), slots)
		return output, workspaceBytesForSlots(target, emitter, slots), *written, err
	}
	resultGroup, err := alloc.Ensure(program.Result, regalloc.Pins{})
	if err != nil {
		return physicalProgram{}, 0, registerWrites{}, err
	}
	resultReg := resultGroup.Registers[0]
	var output physicalProgram
	if final {
		planAllocatedPreservation(target, emitter, written)
	}
	slots := referencedSpillSlots(emitter.(*physicalEmitter).machineStream().Instructions(), target.RegisterBits())
	if len(program.ResultParts) != 0 {
		if resultGroup.Words != 1 || target.RegisterBits() != 64 {
			return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
		}
		var pinned regalloc.Pins
		pinned.Add(resultGroup)
		highGroup, err := alloc.Ensure(program.ResultParts[1], pinned)
		if err != nil {
			return physicalProgram{}, 0, registerWrites{}, err
		}
		if highGroup.Words != 1 {
			return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
		}
		if !final {
			output, err = emitter.bodyBare(resultReg, slots)
			output.resultWords = [2]physReg{resultReg, highGroup.Registers[0]}
			output.resultWordCount = 2
		} else {
			pair, supported := target.Integer128Result()
			if !supported {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			abiLow, abiHigh := pair[0], pair[1]
			pairEmitter, ok := emitter.(interface {
				finishPair(physReg, physReg, physReg, physReg, int) (physicalProgram, int, error)
			})
			if !ok {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			if resultReg != abiLow {
				written[abiLow] = true
			}
			if highGroup.Registers[0] != abiHigh {
				written[abiHigh] = true
			}
			output, slots, err = pairEmitter.finishPair(resultReg, highGroup.Registers[0], abiLow, abiHigh, slots)
		}
	} else if resultGroup.Words == 2 {
		if final {
			pairEmitter, ok := emitter.(interface {
				finishPair(physReg, physReg, physReg, physReg, int) (physicalProgram, int, error)
			})
			if !ok || len(entry.result.Words) != 2 || entry.result.Words[0].Storage != abi.Register || entry.result.Words[1].Storage != abi.Register {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			low, lowOK := physicalByName(entry.result.Words[0].Register)
			high, highOK := physicalByName(entry.result.Words[1].Register)
			if !lowOK || !highOK {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			if resultGroup.Registers[0] != low {
				written[low] = true
			}
			if resultGroup.Registers[1] != high {
				written[high] = true
			}
			output, slots, err = pairEmitter.finishPair(resultGroup.Registers[0], resultGroup.Registers[1], low, high, slots)
		} else if bare {
			output, err = emitter.bodyBare(resultReg, slots)
			output.resultWords = [2]physReg{resultGroup.Registers[0], resultGroup.Registers[1]}
			output.resultWordCount = 2
		} else {
			return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
		}
	} else if final {
		if len(entry.result.Words) == 2 {
			narrowEmitter, ok := emitter.(interface {
				finishNarrowPair(physReg, physReg, physReg, int) (physicalProgram, error)
			})
			if !ok || entry.result.Words[0].Storage != abi.Register || entry.result.Words[1].Storage != abi.Register {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			low, lowOK := physicalByName(entry.result.Words[0].Register)
			high, highOK := physicalByName(entry.result.Words[1].Register)
			if !lowOK || !highOK {
				return physicalProgram{}, 0, registerWrites{}, ErrBackendLimit
			}
			written[high] = true
			if resultReg != low {
				written[low] = true
			}
			output, err = narrowEmitter.finishNarrowPair(resultReg, low, high, slots)
		} else {
			if resultReg != target.ResultRegister() {
				written[target.ResultRegister()] = true
			}
			output, err = emitter.finish(resultReg, slots)
		}
	} else if bare {
		output, err = emitter.bodyBare(resultReg, slots)
	} else {
		output, err = emitter.body(resultReg, slots)
	}
	if err != nil {
		return physicalProgram{}, 0, registerWrites{}, err
	}
	output.exportSlots = exportSlots
	if program.DirectExports {
		output.exportHomes = make([]exportLocation, len(program.Exports))
		for i, id := range program.Exports {
			location := exportLocation{Float: program.Values[id].Class == backend.Float, Bits: program.Values[id].Bits, Register: -1, Slot: locations[id].Slot}
			if r, ok := alloc.Group(id).First(); ok {
				location.Register = int(r)
			} else if location.Slot < 0 && program.Values[id].Constant {
				location.Constant, location.Immediate = true, program.Values[id].Imm
			} else if location.Slot < 0 {
				return physicalProgram{}, 0, registerWrites{}, ErrInvalidFunction
			}
			output.exportHomes[i] = location
		}
	}
	if bare || len(program.ResultParts) != 0 && !final {
		output.resultPhysical = resultReg
	} else if len(entry.result.Words) == 1 {
		output.resultPhysical = target.ResultRegister()
	} else {
		low, _ := physicalByName(entry.result.Words[0].Register)
		output.resultPhysical = low
	}
	if output.resultWordCount == 0 {
		output.resultWords[0] = output.resultPhysical
		output.resultWordCount = 1
	}
	workspaceBytes := 0
	if output.stackBytes == 0 {
		// finishRegisters can append ABI preservation and entry-resource slots.
		finalSlots := slots
		for _, located := range output.insns {
			finalSlots = max(finalSlots, referencedSpillSlots([]machine.Instruction{located.ins}, target.RegisterBits()))
		}
		workspaceBytes = finalSlots * int(target.RegisterBits()/8)
	}
	return output, workspaceBytes, *written, nil
}

// emitSelectedOperation emits the machine code of ins, a selected operation
// with a result, once its registers are chosen: dst for the result and
// inputRegs for its inputs in operand order. floatOperand returns a float
// operand's register, loading a literal one. selectFromFlags says an arm64
// select reads its condition from the flags rather than inputRegs[0].
func emitSelectedOperation(emitter machineEmitter, target machineTarget, values []machine.SelectedValue, ins *machineInstruction, dst physReg,
	inputRegs []physReg, selectFromFlags bool, written *registerWrites, floatOperand func(id, position int) physReg) error {
	switch ins.Op {
	case mVector:
		if ins.Arithmetic == ir.OpVConst {
			emitter.(*physicalEmitter).literal(dst, 128, ins.Data)
			break
		}
		physical := machine.Instruction{Op: machine.OpVector, Arithmetic: ins.Arithmetic, Bits: ins.Bits, VectorType: ins.Type, A: dst, Imm: ins.Imm, Disp: ins.Disp, Indexed: ins.Indexed}
		if len(inputRegs) > 0 {
			physical.B = inputRegs[0]
		}
		if len(inputRegs) > 1 {
			physical.C = inputRegs[1]
		}
		emitter.vectorInstruction(physical)
		if target.Architecture() == "arm64" && ins.Arithmetic == ir.OpVMaskBits {
			written[16] = true
		}
	case mConst:
		if values[ins.Dst].Class == backend.Float {
			w := ins.Bits
			if w == 0 {
				w = 64
			}
			emitter.(*physicalEmitter).literalValue(dst, w, ins.Imm)
		} else {
			emitter.constant(dst, ins.Imm)
		}
	case machine.SelectedFloatMove:
		emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatMove, Bits: ins.Bits, A: dst, B: inputRegs[0]})
	case machine.SelectedFloatBits:
		if target.Architecture() == "amd64" || target.Architecture() == "386" {
			data := make([]byte, 16)
			copy(data, literalBytes(ins.Bits, ins.Imm))
			emitter.(*physicalEmitter).literal(backend.VectorRegister(0), 128, data)
		}
		emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatBits, Arithmetic: ins.Arithmetic, Bits: ins.Bits, A: dst, B: inputRegs[0], Imm: ins.Imm})
	case mSymbol:
		emitter.symbol(dst, ins.Symbol)
	case mMove:
		if ins.Arithmetic == opAdd || ins.Arithmetic == opSub {
			emitter.forgetFlags()
		}
		emitter.move(dst, inputRegs[0])
		emitter.aliasFlagValue(ins.Dst, ins.A, ins.Bits)
	case mBinary:
		emitter.binary(ins.Arithmetic, ins.Bits, dst, inputRegs[0], inputRegs[1])
		emitter.bindFlagValue(ins.Dst, ins.A, ins.B, ins.Arithmetic, false)
	case mFloatBinary:
		left, right := floatOperand(ins.A, 0), floatOperand(ins.B, 1)
		emitter.floatBinary(ins.Arithmetic, ins.Bits, dst, left, right)
	case mAndNot:
		emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpAndNot, Bits: ins.Bits, A: dst, B: inputRegs[0], C: inputRegs[1]})
	case mCountZeros:
		emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpCountZeros, Arithmetic: ins.Arithmetic, Bits: ins.Bits, A: dst, B: inputRegs[0]})
	case mFloatUnary:
		emitter.floatUnary(ins.Arithmetic, ins.Bits, dst, floatOperand(ins.A, 0))
	case mFloatFMA:
		a, b, c := floatOperand(ins.A, 0), floatOperand(ins.B, 1), floatOperand(ins.C, 2)
		emitter.floatFMA(ins.Bits, dst, a, b, c)
	case mBinaryImmediate:
		emitter.binaryImmediate(ins.Arithmetic, ins.Bits, dst, inputRegs[0], ins.Imm)
		emitter.bindFlagValue(ins.Dst, ins.A, ins.B, ins.Arithmetic, true)
	case mInc:
		emitter.increment(dst, inputRegs[0])
		emitter.bindFlagValue(ins.Dst, ins.A, -1, opAdd, true)
	case mDec:
		emitter.decrement(dst, inputRegs[0])
		emitter.bindFlagValue(ins.Dst, ins.A, -1, opSub, true)
	case mNarrow:
		emitter.narrow(dst, inputRegs[0], uint8(ins.Imm))
		emitter.bindFlagValue(ins.Dst, ins.A, -1, ir.OpCastUnsigned, false)
		emitter.aliasCarryValue(ins.Dst, ins.A, uint8(ins.Imm))
		emitter.aliasFlagValue(ins.Dst, ins.A, uint8(ins.Imm))
	case mSignExtend:
		emitter.signExtend(dst, inputRegs[0], uint8(ins.Imm))
		emitter.bindFlagValue(ins.Dst, ins.A, -1, ir.OpSignExtend, false)
		emitter.(*physicalEmitter).machineStream().AliasSignExtendedValue(ins.Dst, ins.A, uint8(ins.Imm), 64)
	case mCarryBit:
		mode := machine.CarryOperands
		if reusableWordCarry(emitter.currentFlags().Carry, ins.Arithmetic, ins.Bits, ins.A, ins.B, values) {
			mode = machine.CarryFlags
		}
		emitter.carryBit(ins.Arithmetic, ins.Bits, dst, inputRegs[0], inputRegs[1], mode)
	case mWordCarry:
		mode := machine.CarryValue
		lowRight := physReg(0)
		if ins.Imm == 1 {
			mode = machine.CarryBitValue
		}
		if len(ins.Args) != 0 {
			mode = machine.CarryOperands
			lowRight = inputRegs[3]
			if reusableWordCarry(emitter.currentFlags().Carry, ins.Arithmetic, ins.Bits, ins.Args[2], ins.Args[3], values) {
				mode = machine.CarryFlags
			}
		}
		emitter.wordCarry(ins.Arithmetic, ins.Bits, dst, inputRegs[0], inputRegs[1], inputRegs[2], lowRight, mode)
		emitter.bindFlagValue(ins.Dst, ins.A, ins.B, ins.Arithmetic, false)
	case mMulHigh:
		emitter.mulHigh(ins.Bits, dst, inputRegs[0], inputRegs[1])
	case mMulHighSigned:
		emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpMulHighSigned, Bits: ins.Bits, A: dst, B: inputRegs[0], C: inputRegs[1]})
	case mDivide:
		emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpDivide, Arithmetic: ins.Arithmetic, Bits: ins.Bits, A: dst, B: inputRegs[0], C: inputRegs[1], Imm: ins.Imm})
	case mIntToFloat:
		emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpIntToFloat, Arithmetic: ins.Arithmetic, Bits: ins.Bits, A: dst, B: inputRegs[0], Imm: ins.Imm})
	case mFloatToInt:
		emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatToInt, Arithmetic: ins.Arithmetic, Bits: ins.Bits, A: dst, B: inputRegs[0], Imm: ins.Imm, Disp: ins.Disp})
	case mFloatCompare:
		emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatCompare, Bits: ins.Bits, A: dst, B: inputRegs[0], C: inputRegs[1], Imm: ins.Imm})
	case mShiftBy:
		emitter.shiftBy(ins.Arithmetic, ins.Bits, dst, inputRegs[0], inputRegs[1])
	case mShift:
		emitter.shift(ins.Arithmetic, ins.Bits, dst, inputRegs[0], uint8(ins.Imm))
	case mScaled:
		emitter.scaled(dst, inputRegs[0], inputRegs[1], ins.Imm, ins.Disp)
	case mMulAdd:
		emitter.mulAdd(dst, inputRegs[0], inputRegs[1], inputRegs[2])
	case mSelect:
		if selectFromFlags {
			emitter.selectFlags(dst, inputRegs[1], inputRegs[2], ir.BranchNotEqual)
		} else {
			emitter.selectValue(dst, inputRegs[0], inputRegs[1], inputRegs[2])
		}
	case mLoad64:
		emitter.load64(dst, inputRegs[0], ins.ByteOrder)
	case mLoad16, mLoad32:
		emitter.narrowMemory(true, ins.Bits, dst, inputRegs[0], ins.ByteOrder)
	case mLoad8:
		emitter.load8(dst, inputRegs[0])
	case mLoadAddress:
		emitter.loadAddress(dst, inputRegs[0], inputRegs[1], ins.Imm, ins.Disp, ins.ByteOrder)
	case mLoadOffset:
		emitter.loadOffset(dst, inputRegs[0], ins.Disp, ins.ByteOrder)
	default:
		return ErrInvalidFunction
	}
	return nil
}

func ppc64CPULevel(target machineTarget) abi.PPC64CPU {
	if policy, ok := target.(interface{ CPU() abi.PPC64CPU }); ok {
		return policy.CPU()
	}
	return abi.PPC64CPUUnspecified
}

func reusableWordCarry(fact machine.FlagFact, operation opcode, width uint8, left, right int, values []machine.SelectedValue) bool {
	if !fact.Valid || fact.Width != width || fact.Operation != operation {
		return false
	}
	if fact.ImmediateRight {
		return fact.Left == left && right >= 0 && right < len(values) && values[right].Constant && values[right].Imm == fact.Immediate
	}
	return fact.Left == left && fact.Right == right || operation == opAdd && fact.Left == right && fact.Right == left
}

// callEffectMismatch reports a custom call effect that IR validation accepted
// but the selected target's calling convention does not match.
func callEffectMismatch(target machineTarget) error {
	return fmt.Errorf("call effect does not match the %s calling convention: %w", target.Architecture(), ErrInvalidFunction)
}
