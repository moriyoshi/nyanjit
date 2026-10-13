package compile

import (
	"fmt"
	"math"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
)

type physReg = machine.Register

// machineTarget owns selection and allocation rules. It never emits code.
type machineTarget interface {
	Architecture() string
	ABIFlavor() ABIFlavor
	PhysicalRegisters() []physReg
	RegisterBits() uint8
	NativeByteOrder() ir.ByteOrder
	SupportsOrdinalSP() bool
	Registers() []physReg
	ParamRegister(int) physReg
	ResultRegister() physReg
	ScratchRegister() physReg
	WorkspaceRegister() physReg
	RegisterName(physReg) string
	CanScaled(uint64, uint64) bool
	CanBinaryImmediate(op, uint8, uint64) bool
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
	binary(op, uint8, physReg, physReg, physReg)
	floatBinary(op, uint8, physReg, physReg, physReg)
	floatUnary(op, uint8, physReg, physReg)
	floatFMA(uint8, physReg, physReg, physReg, physReg)
	binaryImmediate(op, uint8, physReg, physReg, uint64)
	scaled(physReg, physReg, physReg, uint64, uint64)
	selectValue(physReg, physReg, physReg, physReg)
	increment(physReg, physReg)
	decrement(physReg, physReg)
	narrow(physReg, physReg, uint8)
	signExtend(physReg, physReg, uint8)
	shift(op, uint8, physReg, physReg, uint8)
	carryBit(op, uint8, physReg, physReg, physReg, machine.CarryMode)
	wordCarry(op, uint8, physReg, physReg, physReg, physReg, physReg, machine.CarryMode)
	mulHigh(uint8, physReg, physReg, physReg)
	shiftBy(op, uint8, physReg, physReg, physReg)
	load64(physReg, physReg, ir.ByteOrder)
	store64(physReg, physReg, ir.ByteOrder)
	load8(physReg, physReg)
	store8(physReg, physReg)
	loadAddress(physReg, physReg, physReg, uint64, uint64, ir.ByteOrder)
	storeAddress(physReg, physReg, physReg, uint64, uint64, ir.ByteOrder)
	loadOffset(physReg, physReg, uint64, ir.ByteOrder)
	storeOffset(physReg, physReg, uint64, ir.ByteOrder)
	syscall()
	callC(CallEffect)
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
	selectFlags(physReg, physReg, physReg, BranchCondition)
	currentFlags() flagState
	binaryCarry(op, physReg, physReg, physReg)
	mulWide(physReg)
	incomingWord(physReg, int)
	forgetFlags()
	mulAdd(physReg, physReg, physReg, physReg)
	flagsClobbered() bool
	callStackBytes() int
	linkSavedInFrame() bool
	body(physReg, int) (physicalProgram, error)
	bodyBare(physReg, int) (physicalProgram, error)
	bindFlagValue(int, int, int, op, bool)
	aliasCarryValue(int, int, uint8)
	aliasFlagValue(int, int, uint8)
	vectorInstruction(machine.Instruction)
	vectorSpill(physReg, int)
	vectorReload(physReg, int)
	finish(physReg, int) (physicalProgram, error)
}

type emissionArtifact struct {
	code        []byte
	assembly    string
	stackBytes  int
	relocations []Relocation
	goUnwind    goUnwindInfo
}

type emissionKind uint8

const (
	emitNative emissionKind = iota
	emitText
)

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
	code        []byte
	report      EmissionReport
	assembly    string
	relocations []Relocation
	goUnwind    goUnwindInfo
}

func lowerFunction(ops []instruction, leaves []scopeEvent, arity, result int, resultParts []int, options CompileOptions, context ...*loweringInput) (loweringResult, error) {
	return lowerFunctionMode(ops, leaves, arity, result, resultParts, options, emitNative, context...)
}

func lowerFunctionMode(ops []instruction, leaves []scopeEvent, arity, result int, resultParts []int, options CompileOptions, kind emissionKind, context ...*loweringInput) (loweringResult, error) {
	if err := validateEmissionTarget(options, kind); err != nil {
		return loweringResult{}, err
	}
	if options.ABI != ABIC && options.ABI != ABIGo {
		return loweringResult{}, ErrUnsupportedABI
	}
	if options.StackBytesAvailable < 0 {
		return loweringResult{}, ErrInvalidFunction
	}
	if options.Stack != StackNoUse && options.Stack != StackPlatform {
		return loweringResult{}, ErrInvalidFunction
	}
	if options.StackPointer != StackPointerReserved && options.StackPointer != StackPointerOrdinary {
		return loweringResult{}, ErrInvalidFunction
	}
	if options.Stack == StackNoUse && options.StackBytesAvailable != 0 {
		return loweringResult{}, fmt.Errorf("StackNoUse requires a zero platform stack budget (got %d): %w", options.StackBytesAvailable, ErrInvalidFunction)
	}
	if options.ManagedCalls && (options.Stack != StackPlatform || options.StackPointer != StackPointerReserved) {
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
	emitter := newMachineEmitter(options)
	if emitter == nil {
		return loweringResult{}, ErrUnsupported
	}
	assembler := newMachineAssembler(options, kind)
	if assembler == nil {
		return loweringResult{}, ErrUnsupported
	}
	if options.StackPointer == StackPointerOrdinary && !target.SupportsOrdinalSP() {
		return loweringResult{}, ErrUnsupportedStackRole
	}
	program, err := selectProgramResultParts(ops, leaves, arity, result, resultParts, target)
	if err != nil {
		return loweringResult{}, fmt.Errorf("select %s instructions: %w", target.Architecture(), err)
	}
	if len(context) > 0 && context[0] != nil {
		bindVariableHomes(&program, context[0].source, 0, context[0].homes)
	}
	if err := bridgeManagedCCalls(&program, options, kind); err != nil {
		return loweringResult{}, err
	}
	entry, err := entryLayout(target, entryWordsFromIR(ops, target.RegisterBits()))
	if err != nil {
		return loweringResult{}, err
	}
	if target.Architecture() == "386" && options.ABI == ABIGo {
		emitter.(*physicalEmitter).goReturnOffset = entry.result.Words[0].StackOffset
	}
	if len(resultParts) != 0 {
		if options.ABI != ABIC || target.RegisterBits() != 64 || len(resultParts) != 2 || resultParts[0] != result {
			return loweringResult{}, ErrBackendLimit
		}
		pair, supported := target.Integer128Result()
		if !supported {
			return loweringResult{}, ErrBackendLimit
		}
		entry.result.Words = []ABIWordLocation{
			{Storage: ABIRegister, Register: target.RegisterName(pair[0])},
			{Storage: ABIRegister, Register: target.RegisterName(pair[1])},
		}
	}
	for _, ins := range program.Instructions {
		if (ins.Op == mSyscall || ins.Op == mCallC || ins.Op == mCallGo) && options.Stack != StackPlatform {
			return loweringResult{}, ErrStackRequired
		}
		if ins.Op == mCallGo && !options.ManagedCalls && options.ABI != ABIGo {
			return loweringResult{}, ErrExternalCallOnly
		}
	}
	physical, workspaceBytes, written, err := allocateAndEmit(program, target, emitter)
	if err != nil {
		return loweringResult{}, fmt.Errorf("allocate %s registers: %w", target.Architecture(), err)
	}
	if target.Architecture() == "ppc64" || target.Architecture() == "ppc64le" {
		instructions := make([]machine.Instruction, len(physical.insns))
		for n, i := range physical.insns {
			instructions[n] = i.ins
		}
		s := emitter.(*physicalEmitter).machineStream()
		s.Replace(instructions)
		if err := machine.ElidePPC64DeadFlags(s, nil, nil); err != nil {
			return loweringResult{}, err
		}
		for n, i := range s.Instructions() {
			physical.insns[n].ins = i
		}
	}
	if err := relaxFunctionLiterals(&physical, emitter, target.Architecture()); err != nil {
		return loweringResult{}, err
	}
	finishLiterals(&physical)
	recordImplicitWrites(target, physical, written)
	output, err := assembleProgram(physical, assembler)
	if err != nil {
		return loweringResult{}, fmt.Errorf("assemble %s instructions: %w", target.Architecture(), err)
	}
	var goUnwind goUnwindInfo
	if kind == emitNative && target.ManagedFrameLimit() > 0 {
		goUnwind, err = goUnwindFromProgram(physical)
		if err != nil {
			return loweringResult{}, err
		}
	}
	report := EmissionReport{
		Architecture: target.Architecture(),
		PPC64CABI:    ppc64EntryConvention(target),
		PPC64CPU:     ppc64CPULevel(target), CPU: resolvedCPU(options),
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
		CodeBytes:          len(output.code),
		WorkspaceBytes:     workspaceBytes,
		WorkspaceAlignment: int(target.RegisterBits() / 8),
		StackBytes:         output.stackBytes,
		CallStackBytes:     emitter.callStackBytes(),
		SpillStorage:       SpillNone,
	}
	for _, v := range program.Values {
		if v.Class == regalloc.Vector {
			report.WorkspaceAlignment = 16
			break
		}
	}
	if len(resultParts) != 0 {
		report.ResultWords = 2
	}
	if workspaceBytes != 0 {
		report.WorkspaceRegister = target.RegisterName(target.WorkspaceRegister())
		report.SpillStorage = SpillWorkspace
	}
	if output.stackBytes != 0 {
		report.SpillStorage = SpillStack
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
		case mLoad64, mLoad8, mLoadAddress, mLoadOffset:
			report.ExplicitMemoryReads = true
			report.MayReadMemory = true
		case mStore64, mStore8, mStoreAddress, mStoreOffset:
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
	for _, register := range target.PhysicalRegisters() {
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
	report.ClobberedFPRegisters, err = fpClobbersFromPhysical(physical, emitter, target.Architecture())
	report.FPStatusModified = fpStatusModifiedFromPhysical(physical)
	report.ClobberedSpecialRegisters = specialWritesFromPhysical(physical, target)
	if err != nil {
		return loweringResult{}, err
	}
	return loweringResult{code: output.code, report: report, assembly: output.assembly, relocations: output.relocations, goUnwind: goUnwind}, nil
}

func selectProgram(ops []instruction, leaves []scopeEvent, arity, result int, target machineTarget) (machineProgram, error) {
	return selectProgramExports(ops, leaves, arity, result, nil, target)
}

func selectProgramResultParts(ops []instruction, leaves []scopeEvent, arity, result int, parts []int, target machineTarget) (machineProgram, error) {
	if len(parts) == 0 {
		return selectProgram(ops, leaves, arity, result, target)
	}
	if len(parts) != 2 || parts[0] != result {
		return machineProgram{}, ErrInvalidFunction
	}
	program, err := selectProgramExports(ops, leaves, arity, result, parts[1:], target)
	if err != nil {
		return machineProgram{}, err
	}
	program.Exports = nil
	program.ResultParts = append([]int(nil), parts...)
	return program, nil
}

func selectProgramExports(ops []instruction, leaves []scopeEvent, arity, result int, exports []int, target machineTarget) (machineProgram, error) {
	return machine.SelectProgram(ops, leaves, arity, result, exports, abi.MaxArgumentsFor(target.Architecture()), target)
}

const maxSpillSlots = 4096

type valueLocation = regalloc.ValueLocation
type exportLocation = regalloc.Home

func allocateAndEmit(program machineProgram, target machineTarget, emitter machineEmitter) (physicalProgram, int, map[physReg]bool, error) {
	return allocateAndEmitMode(program, target, emitter, true, false)
}

func allocateBlock(program machineProgram, target machineTarget, emitter machineEmitter) (physicalProgram, int, map[physReg]bool, error) {
	return allocateAndEmitMode(program, target, emitter, false, false)
}

func allocateBranchBlock(program machineProgram, target machineTarget, emitter machineEmitter) (physicalProgram, int, map[physReg]bool, error) {
	return allocateAndEmitMode(program, target, emitter, false, true)
}

func allocateAndEmitMode(program machineProgram, target machineTarget, emitter machineEmitter, final, bare bool) (physicalProgram, int, map[physReg]bool, error) {
	for id, v := range program.Values {
		if v.EntryReg >= 0 && machine.IsVectorRegister(physReg(v.EntryReg)) {
			program.Values[id].Class = regalloc.Float
		}
	}
	markFloatingHomeGets(&program)
	if len(floatRegisters(target.Architecture())) != 0 {
		program = assignFloatResidency(program)
	}
	program = foldFloatLiterals(program)
	if validator, ok := target.(interface{ validateProgram(machineProgram) error }); ok {
		if err := validator.validateProgram(program); err != nil {
			return physicalProgram{}, 0, nil, err
		}
	}
	if target.Architecture() == "386" {
		physical := emitter.(*physicalEmitter)
		for _, ins := range program.Instructions {
			if ins.Op != mCallC && ins.Op != mCallGo {
				continue
			}
			bytes := 0
			for _, words := range selectedArgumentWords(program, ins.Args[1:]) {
				bytes += 4 * words
			}
			if ins.Op == mCallGo {
				bytes += 8
			}
			physical.callAreaBytes = max(physical.callAreaBytes, bytes)
		}
	}
	registers := allocationRegisters(target, program.HomePlan, emitter.(*physicalEmitter).stack)
	if program.LoopPressure && selectedGeneralPeak(program) > len(registers) {
		present := map[physReg]bool{}
		for _, r := range registers {
			present[r] = true
		}
		for _, r := range preservedAllocatable(target) {
			if !present[r] {
				registers = append(registers, r)
			}
		}
	}
	activeHomeRegion := program.HomeRegion
	homeReserved := func(r physReg) bool {
		if program.HomePlan == nil {
			return false
		}
		for _, h := range program.HomePlan.Regions[activeHomeRegion] {
			if h.Register == int(r) {
				return true
			}
		}
		return false
	}
	needsFPBank := false
	for _, v := range program.Values {
		needsFPBank = needsFPBank || v.Class == regalloc.Vector || v.Class == regalloc.Float
	}
	if program.HomePlan != nil {
		for _, floating := range program.HomePlan.FloatHomes {
			needsFPBank = needsFPBank || floating
		}
	}
	// A scope-only fragment can restore FP homes without an FP SSA producer.
	if needsFPBank {
		registers = append(registers, floatRegisters(target.Architecture())...)
	}
	entryArity := 0
	for _, value := range program.Values {
		if value.Param >= entryArity {
			entryArity = value.Param + 1
		}
	}
	entryWords := make([]int, entryArity)
	for _, value := range program.Values {
		if value.Param >= 0 {
			entryWords[value.Param] = max(1, int(value.Words))
		}
	}
	entry, err := entryLayout(target, entryWords)
	if err != nil {
		return physicalProgram{}, 0, nil, err
	}
	if target.Architecture() == "386" && target.ABIFlavor() == ABIGo {
		entry.result = ABIValueLocation{Words: []ABIWordLocation{
			{Storage: ABIRegister, Register: "EAX"},
			{Storage: ABIRegister, Register: "EDX"},
		}}
	}
	written := make(map[physReg]bool)
	locations := make([]valueLocation, len(program.Values))
	for id := range locations {
		locations[id] = valueLocation{Slot: -1}
	}
	residency := regalloc.NewResidency(len(registers), len(program.Values))
	contents := residency.Owners
	remaining := make([]int, len(program.Values))
	uses := make([][]int, len(program.Values))
	useCursor := make([]int, len(program.Values))
	for pos, ins := range program.Instructions {
		for _, id := range ins.Inputs() {
			if id < 0 || id >= len(program.Values) {
				return physicalProgram{}, 0, nil, ErrInvalidFunction
			}
			remaining[id]++
			uses[id] = append(uses[id], pos)
		}
	}
	if !program.DiscardResult {
		remaining[program.Result]++
		uses[program.Result] = append(uses[program.Result], len(program.Instructions))
	}
	if len(program.ResultParts) != 0 {
		if len(program.ResultParts) != 2 || program.ResultParts[0] != program.Result || program.ResultParts[1] < 0 || program.ResultParts[1] >= len(program.Values) {
			return physicalProgram{}, 0, nil, ErrInvalidFunction
		}
		remaining[program.ResultParts[1]]++
		uses[program.ResultParts[1]] = append(uses[program.ResultParts[1]], len(program.Instructions))
	}
	for _, id := range program.Exports {
		remaining[id]++
		uses[id] = append(uses[id], len(program.Instructions))
	}
	preferred := make([]int, len(program.Values))
	for id := range preferred {
		preferred[id] = -1
	}
	registerNamed := func(name string) int {
		for i, reg := range registers {
			if target.RegisterName(reg) == name {
				return i
			}
		}
		return -1
	}
	for id, reg := range program.PreferredRegisters {
		if id < 0 || id >= len(preferred) {
			return physicalProgram{}, 0, nil, ErrInvalidFunction
		}
		for index, available := range registers {
			if available == reg {
				preferred[id] = index
				break
			}
		}
	}
	if !bare {
		preferred[program.Result] = registerNamed(target.RegisterName(target.ResultRegister()))
	}
	if program.DirectExports {
		if len(program.ExportRegisters) != len(program.Exports) {
			return physicalProgram{}, 0, nil, ErrInvalidFunction
		}
		for i, id := range program.Exports {
			if program.ExportRegisters[i] >= 0 {
				preferred[id] = registerNamed(target.RegisterName(physReg(program.ExportRegisters[i])))
			}
		}
	}
	definedAt := make([]int, len(program.Values))
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
	// Fixed call operands prefer their producers' registers. Carry that
	// preference backward through operations that can reuse an input register.
	// Within a scope, the final call use can guide a value with earlier uses.
	for pos, ins := range program.Instructions {
		if ins.Op != mSyscall && ins.Op != mCallC && ins.Op != mCallGo {
			continue
		}
		kind := SystemCall
		if ins.Op == mCallC || ins.Op == mCallGo {
			kind = FunctionCall
		}
		abi := ABIC
		if ins.Op == mCallGo {
			abi = ABIGo
		}
		effect, err := preflightWithEffect(CallSpec{Architecture: target.Architecture(), Kind: kind, ABI: abi, ArgumentCount: len(ins.Args) - 1, ArgumentWords: selectedArgumentWords(program, ins.Args[1:])}, ins.Effect)
		if err != nil {
			return physicalProgram{}, 0, nil, err
		}
		first := effect.NumberRegister
		if ins.Op == mCallC || ins.Op == mCallGo {
			first = target.RegisterName(target.CallTargetRegister())
		}
		fixed := append([]string{first}, effect.ArgumentRegisters...)
		scope := innermostScope(pos)
		for i, id := range ins.Args {
			if i >= len(fixed) || fixed[i] == "" || len(uses[id]) == 0 || uses[id][len(uses[id])-1] != pos || definedAt[id] < scope.Start {
				continue
			}
			// A prior call may overwrite the desired home before this use.
			safe := true
			for between := definedAt[id] + 1; between < pos; between++ {
				switch program.Instructions[between].Op {
				case mSyscall, mCallC, mCallGo:
					safe = false
				}
				if !safe {
					break
				}
			}
			if safe {
				preferred[id] = registerNamed(fixed[i])
			}
		}
	}
	for pos := len(program.Instructions) - 1; pos >= 0; pos-- {
		ins := program.Instructions[pos]
		if ins.Dst < 0 || preferred[ins.Dst] < 0 {
			continue
		}
		input := -1
		inputPosition := 0
		switch ins.Op {
		case mMove, mInc, mDec, mNarrow, mSignExtend, mShift, mShiftBy, mCarryBit, mWordCarry, mMulHigh, mBinary, mFloatBinary, mFloatFMA, mFloatUnary, mBinaryImmediate, mScaled, mMulAdd:
			input = ins.A
		case mSelect:
			input, inputPosition = ins.C, 2
		}
		if input >= 0 && len(uses[input]) == 1 && target.CanReuse(ins, inputPosition) {
			preferred[input] = preferred[ins.Dst]
		}
	}
	for id, value := range program.Values {
		if remaining[id] == 0 || value.Param < 0 && value.EntryReg < 0 {
			continue
		}
		if value.Param >= 0 {
			words := entry.arguments[value.Param].Words
			valueWordCount := int(value.Words)
			if valueWordCount == 0 {
				valueWordCount = 1
			}
			if len(words) != valueWordCount || len(words) > 2 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			if words[0].Storage == ABIStack {
				for word, location := range words {
					if location.Storage != ABIStack || location.StackOffset < 0 {
						return physicalProgram{}, 0, nil, ErrBackendLimit
					}
					locations[id].Incoming[word] = location.StackOffset
				}
				locations[id].IncomingWords = uint8(len(words))
				continue
			}
			group := regalloc.Group{Indices: [2]int{-1, -1}, Words: uint8(len(words))}
			for word, location := range words {
				if location.Storage != ABIRegister {
					return physicalProgram{}, 0, nil, ErrBackendLimit
				}
				group.Indices[word] = registerNamed(location.Register)
			}
			if err := residency.Claim(id, group); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			continue
		}
		if value.Words > 1 {
			return physicalProgram{}, 0, nil, ErrBackendLimit
		}
		found := -1
		for i, candidate := range registers {
			if candidate == physReg(value.EntryReg) {
				found = i
				break
			}
		}
		if err := residency.Claim(id, regalloc.SingleRegister(found)); err != nil {
			return physicalProgram{}, 0, nil, err
		}
	}
	if program.HomePlan != nil {
		program.ReservedSlots = max(program.ReservedSlots, program.HomePlan.ReservedSlots)
	}
	slotLimit := maxSpillSlots
	if target.Architecture() == "arm64" && emitter.linkSavedInFrame() {
		// The frame's first word is LR, so the highest logical spill slot
		// must still fit the arm64 scaled 12-bit stack offset.
		slotLimit--
	}
	slots := program.ReservedSlots
	if slots > slotLimit {
		return physicalProgram{}, 0, nil, ErrBackendLimit
	}
	for id, value := range program.Values {
		if value.EntrySlot >= 0 && remaining[id] > 0 {
			words := int(value.Words)
			if words == 0 {
				words = 1
			}
			if value.EntrySlot+words > slots {
				return physicalProgram{}, 0, nil, ErrInvalidFunction
			}
			locations[id].Slot = value.EntrySlot
			locations[id].SlotWords = uint8(words)
		}
	}
	freeSlots := make([]int, 0)
	allocateSlot := func() (int, error) {
		if count := len(freeSlots); count != 0 {
			slot := freeSlots[count-1]
			freeSlots = freeSlots[:count-1]
			return slot, nil
		}
		if slots == slotLimit {
			return -1, ErrBackendLimit
		}
		slot := slots
		slots++
		return slot, nil
	}
	allocateGroupSlots := func(words int) (int, error) {
		if words == 1 {
			return allocateSlot()
		}
		if words < 1 || words > 2 || slots+words > slotLimit {
			return -1, ErrBackendLimit
		}
		base := slots
		slots += words
		return base, nil
	}
	nextUse := func(id int) int {
		if useCursor[id] >= len(uses[id]) {
			return math.MaxInt
		}
		return uses[id][useCursor[id]]
	}
	storageWords := func(id int) int {
		if program.Values[id].Class == regalloc.Vector {
			return 2
		}
		if program.Values[id].Words == 0 {
			return 1
		}
		return int(program.Values[id].Words)
	}
	vectorParity := 0
	if target.Architecture() == "arm64" {
		if p, ok := emitter.(*physicalEmitter); ok && p.stack == StackPlatform {
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
		if p, ok := emitter.(*physicalEmitter); ok && p.stack == StackPlatform {
			vectorParity = (p.vectorHomes + 1) % 2
			if hasCall {
				vectorParity = p.vectorHomes % 2
			}
		}
	}
	allocateValueSlot := func(id int) (int, error) {
		if program.Values[id].Class == regalloc.Vector {
			if slots%2 != vectorParity {
				slots++
			}
			if slots+2 > slotLimit {
				return -1, ErrBackendLimit
			}
			slot := slots
			slots += 2
			return slot, nil
		}
		return allocateGroupSlots(storageWords(id))
	}
	spillValue := func(id int, group regalloc.Group, slot int) {
		if program.Values[id].Class == regalloc.Float {
			emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatSpill, A: registers[group.First()], Slot: slot, Bits: 64})
		} else if program.Values[id].Class == regalloc.Vector {
			emitter.vectorSpill(registers[group.First()], slot)
		} else {
			for word := 0; word < int(group.Words); word++ {
				emitter.spill(registers[group.Indices[word]], slot+word)
			}
		}
	}
	allocationPosition := -1
	allowedHome := -1
	chooseClassGroup := func(words int, pinned map[int]bool, preferred regalloc.Group, class regalloc.Class) (regalloc.Group, error) {
		filtered := make(map[int]bool, len(registers))
		for i, r := range registers {
			filtered[i] = pinned[i] || homeReserved(r) && int(r) != allowedHome || (machine.IsVectorRegister(r) != (class == regalloc.Vector || class == regalloc.Float))
		}
		pinned = filtered
		group, err := residency.Reserve(words, pinned, preferred, nextUse, func(id int, group regalloc.Group) error {
			if program.Values[id].Constant || locations[id].Slot >= 0 {
				return nil
			}
			slot, err := allocateValueSlot(id)
			if err != nil {
				return err
			}
			locations[id].Slot, locations[id].SlotWords = slot, uint8(storageWords(id))
			spillValue(id, group, slot)
			return nil
		})
		if err != nil {
			return group, fmt.Errorf("allocate class %d at selected instruction %d in scope %d (pins %v): %w", class, allocationPosition, activeHomeRegion, pinned, err)
		}
		return group, nil
	}
	valueWords := func(id int) int {
		if program.Values[id].Words == 0 {
			return 1
		}
		return int(program.Values[id].Words)
	}
	chooseForValue := func(id int, pinned map[int]bool) (regalloc.Group, error) {
		preference := regalloc.Group{}
		if index := preferred[id]; index >= 0 && valueWords(id) == 1 {
			preference = regalloc.SingleRegister(index)
		}
		return chooseClassGroup(valueWords(id), pinned, preference, program.Values[id].Class)
	}
	ensureGroup := func(id int, pinned map[int]bool) (regalloc.Group, error) {
		if group := residency.Group(id); group.Words != 0 {
			if int(group.Words) != valueWords(id) {
				return regalloc.Group{}, ErrInvalidFunction
			}
			return group, nil
		}
		group, err := chooseForValue(id, pinned)
		if err != nil {
			return regalloc.Group{}, err
		}
		if locations[id].Slot >= 0 {
			if int(locations[id].SlotWords) != storageWords(id) {
				return regalloc.Group{}, ErrInvalidFunction
			}
			if program.Values[id].Class == regalloc.Float {
				emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatReload, A: registers[group.First()], Slot: locations[id].Slot, Bits: 64})
			} else if program.Values[id].Class == regalloc.Vector {
				emitter.vectorReload(registers[group.First()], locations[id].Slot)
			} else {
				for word := 0; word < int(group.Words); word++ {
					reg := registers[group.Indices[word]]
					emitter.reload(reg, locations[id].Slot+word)
					written[reg] = true
				}
			}
		} else if locations[id].IncomingWords != 0 {
			if int(locations[id].IncomingWords) != valueWords(id) {
				return regalloc.Group{}, ErrInvalidFunction
			}
			for word := 0; word < int(group.Words); word++ {
				reg := registers[group.Indices[word]]
				emitter.incomingWord(reg, locations[id].Incoming[word])
				written[reg] = true
			}
		} else if program.Values[id].Constant && program.Values[id].Symbol != "" {
			if group.Words != 1 {
				return regalloc.Group{}, ErrBackendLimit
			}
			reg := registers[group.Indices[0]]
			emitter.symbol(reg, program.Values[id].Symbol)
			written[reg] = true
		} else if program.Values[id].Constant && program.Values[id].Class == regalloc.Float {
			w := program.Values[id].Bits
			if w == 0 {
				w = 64
			}
			emitter.(*physicalEmitter).literal(registers[group.First()], w, literalBytes(w, program.Values[id].Imm, target.NativeByteOrder()))
		} else if program.Values[id].Constant {
			parts, err := regalloc.SplitIntegerWords(program.Values[id].Imm, target.RegisterBits(), int(group.Words))
			if err != nil {
				return regalloc.Group{}, err
			}
			for word := 0; word < int(group.Words); word++ {
				reg := registers[group.Indices[word]]
				emitter.constant(reg, parts[word])
				written[reg] = true
			}
		} else {
			return regalloc.Group{}, ErrInvalidFunction
		}
		if err := residency.Claim(id, group); err != nil {
			return regalloc.Group{}, err
		}
		return group, nil
	}
	consumeInputs := func(inputs []int) {
		for _, id := range inputs {
			remaining[id]--
			useCursor[id]++
			if remaining[id] == 0 {
				residency.Release(id)
				if slot := locations[id].Slot; slot >= 0 {
					if slot >= program.ReservedSlots {
						for word := 0; word < int(locations[id].SlotWords); word++ {
							freeSlots = append(freeSlots, slot+word)
						}
					}
					locations[id].Slot = -1
					locations[id].SlotWords = 0
				}
			}
		}
	}
	physicalByName := func(name string) (physReg, bool) {
		for _, reg := range target.PhysicalRegisters() {
			if target.RegisterName(reg) == name {
				return reg, true
			}
		}
		return 0, false
	}
	registerIndex := func(reg physReg) int {
		for i, candidate := range registers {
			if candidate == reg {
				return i
			}
		}
		return -1
	}
	spillHomeOwner := func(r physReg, pinned map[int]bool) error {
		index := registerIndex(r)
		if index < 0 {
			return ErrBackendLimit
		}
		id := contents[index]
		if id < 0 {
			return nil
		}
		group := residency.Group(id)
		if remaining[id] > 0 && !program.Values[id].Constant {
			protected := map[int]bool{index: true}
			for i, v := range pinned {
				protected[i] = v || protected[i]
			}
			moved, e := chooseForValue(id, protected)
			if e == nil {
				for word := 0; word < int(group.Words); word++ {
					to, from := registers[moved.Indices[word]], registers[group.Indices[word]]
					emitHomeMove(emitter, program.Values[id].Bits, to, from)
					written[to] = true
				}
				residency.Release(id)
				return residency.Claim(id, moved)
			}
			if locations[id].Slot < 0 {
				slot, err := allocateValueSlot(id)
				if err != nil {
					return err
				}
				locations[id].Slot, locations[id].SlotWords = slot, uint8(storageWords(id))
				spillValue(id, group, slot)
			}
		}
		residency.Release(id)
		return nil
	}
	transitionHomes := func(next int) error {
		if program.HomePlan == nil {
			return nil
		}
		old, new := program.HomePlan.Regions[activeHomeRegion], program.HomePlan.Regions[next]
		for id := range program.HomePlan.Slots {
			h, exists := old[id]
			if !exists {
				continue
			}
			n, ok := new[id]
			if ok && h.Register >= 0 && h.Register != n.Register && (program.HomePlan.Parents[activeHomeRegion] != next || program.HomePlan.Modified[activeHomeRegion][id]) {
				emitHomeSpill(emitter, physReg(h.Register), h.Slot)
			}
		}
		// Snapshot relocation must respect every destination home, including
		// homes restored earlier in this transition.
		activeHomeRegion = next
		for id := range program.HomePlan.Slots {
			h, exists := new[id]
			if !exists {
				continue
			}
			if h.Register < 0 {
				continue
			}
			o, ok := old[id]
			if ok && o.Register == h.Register {
				continue
			}
			if ok {
				if err := spillHomeOwner(physReg(h.Register), nil); err != nil {
					return err
				}
				emitHomeReload(emitter, physReg(h.Register), h.Slot)
				written[physReg(h.Register)] = true
			}
		}
		return nil
	}
	type homeValueKey struct {
		variable int
		part     uint64
	}
	lastHomeValue := map[homeValueKey]int{}
	callHomeSaved := []machine.Home{}
	beforeHomeCall := func(ins machineInstruction) error {
		callHomeSaved = nil
		if program.HomePlan == nil {
			return nil
		}
		kind, flavor := FunctionCall, ABIC
		if ins.Op == mSyscall {
			kind = SystemCall
		}
		if ins.Op == mCallGo {
			flavor = ABIGo
		}
		effect, err := preflightWithEffect(CallSpec{Architecture: target.Architecture(), Kind: kind, ABI: flavor, ArgumentCount: len(ins.Args) - 1, ArgumentWords: selectedArgumentWords(program, ins.Args[1:])}, ins.Effect)
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
		for id := range program.HomePlan.Slots {
			h, exists := program.HomePlan.Regions[activeHomeRegion][id]
			if !exists {
				continue
			}
			if h.Register >= 0 && clobbers[homeRegisterName(target, physReg(h.Register))] {
				emitHomeSpill(emitter, physReg(h.Register), h.Slot)
				callHomeSaved = append(callHomeSaved, h)
			}
		}
		return nil
	}
	afterHomeCall := func() error {
		for _, h := range callHomeSaved {
			if err := spillHomeOwner(physReg(h.Register), nil); err != nil {
				return err
			}
			emitHomeReload(emitter, physReg(h.Register), h.Slot)
			written[physReg(h.Register)] = true
		}
		callHomeSaved = nil
		return nil
	}
	for position, ins := range program.Instructions {
		allocationPosition = position
		allowedHome = -1
		if ins.Dst >= 0 && program.HomePlan != nil && position+1 < len(program.Instructions) {
			next := program.Instructions[position+1]
			if next.Op == machine.SelectedHomeSet && next.A == ins.Dst {
				h := program.HomePlan.Regions[activeHomeRegion][next.Variable]
				if h.Register >= 0 && valueWords(ins.Dst) == 1 {
					index := registerIndex(physReg(h.Register))
					owner := contents[index]
					usesHere := 0
					for _, x := range ins.Inputs() {
						if x == owner {
							usesHere++
						}
					}
					if owner < 0 || remaining[owner] == usesHere {
						allowedHome = h.Register
						preferred[ins.Dst] = index
					}
				}
			}
		}
		// Constants are rematerializable. Keeping them virtual until a use
		// lets a fixed call operand materialize directly in its ABI register.
		if ins.Op == mConst {
			continue
		}
		inputs := ins.Inputs()
		if ins.Op == machine.SelectedHomeBoundary {
			next := int(ins.Imm)
			if ins.Arithmetic == ir.OpScopeLeave && program.HomePlan != nil {
				next = program.HomePlan.Parents[next]
			}
			if err := transitionHomes(next); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			continue
		}
		if ins.Op == machine.SelectedHomeGet || ins.Op == machine.SelectedHomeSet {
			if program.HomePlan == nil || ins.Variable < 0 || ins.Variable >= len(program.HomePlan.Slots) {
				return physicalProgram{}, 0, nil, ErrInvalidFunction
			}
			h, ok := program.HomePlan.Regions[activeHomeRegion][ins.Variable]
			if !ok {
				h = machine.Home{Slot: program.HomePlan.Slots[ins.Variable], Register: -1}
			}
			h.Slot += int(ins.Imm) * int(64/target.RegisterBits())
			if ins.Op == machine.SelectedHomeSet {
				if h.Register >= 0 && program.Values[ins.A].Constant && program.Values[ins.A].Symbol == "" && valueWords(ins.A) == 1 {
					if err := spillHomeOwner(physReg(h.Register), nil); err != nil {
						return physicalProgram{}, 0, nil, err
					}
					if machine.IsVectorRegister(physReg(h.Register)) {
						emitter.(*physicalEmitter).literal(physReg(h.Register), ins.Bits, literalBytes(ins.Bits, program.Values[ins.A].Imm, target.NativeByteOrder()))
					} else {
						emitter.constant(physReg(h.Register), program.Values[ins.A].Imm)
					}
					written[physReg(h.Register)] = true
					lastHomeValue[homeValueKey{ins.Variable, ins.Imm}] = ins.A
					consumeInputs(inputs)
					continue
				}
				g, err := ensureGroup(ins.A, nil)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if h.Register >= 0 && residency.First(ins.A) != registerIndex(physReg(h.Register)) {
					protected := map[int]bool{}
					for word := 0; word < int(g.Words); word++ {
						protected[g.Indices[word]] = true
					}
					if e := spillHomeOwner(physReg(h.Register), protected); e != nil {
						return physicalProgram{}, 0, nil, e
					}
				}
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				for word := 0; word < int(g.Words); word++ {
					r := registers[g.Indices[word]]
					if h.Register >= 0 {
						if physReg(h.Register) != r {
							emitHomeMove(emitter, ins.Bits, physReg(h.Register), r)
						}
						written[physReg(h.Register)] = true
					} else {
						emitHomeSpill(emitter, r, h.Slot+word)
					}
				}
				lastHomeValue[homeValueKey{ins.Variable, ins.Imm}] = ins.A
				consumeInputs(inputs)
			} else {
				var g regalloc.Group
				var err error
				if h.Register >= 0 && contents[registerIndex(physReg(h.Register))] < 0 {
					g = regalloc.SingleRegister(registerIndex(physReg(h.Register)))
				} else {
					g, err = chooseForValue(ins.Dst, nil)
				}
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				for word := 0; word < int(g.Words); word++ {
					r := registers[g.Indices[word]]
					if h.Register >= 0 {
						if r != physReg(h.Register) {
							emitHomeMove(emitter, ins.Bits, r, physReg(h.Register))
						}
					} else {
						emitHomeReload(emitter, r, h.Slot+word)
					}
					written[r] = true
				}
				if source, ok := lastHomeValue[homeValueKey{ins.Variable, ins.Imm}]; ok {
					emitter.aliasFlagValue(ins.Dst, source, ins.Bits)
				}
				if err := residency.Claim(ins.Dst, g); err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if remaining[ins.Dst] == 0 {
					residency.Release(ins.Dst)
				}
			}
			continue
		}
		if ins.Op == mCallC || ins.Op == mCallGo || ins.Op == mSyscall {
			if err := beforeHomeCall(ins); err != nil {
				return physicalProgram{}, 0, nil, err
			}
		}
		if target.Architecture() == "386" && (ins.Op == mCallC || ins.Op == mCallGo) {
			if emitter.(*physicalEmitter).stack != StackPlatform {
				return physicalProgram{}, 0, nil, ErrStackRequired
			}
			callABI := ABIC
			if ins.Op == mCallGo {
				callABI = ABIGo
			}
			if _, err := PreflightCall(CallSpec{Architecture: target.Architecture(), Kind: FunctionCall, ABI: callABI, ArgumentCount: len(inputs) - 1, ArgumentWords: selectedArgumentWords(program, inputs[1:])}); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			argumentOffset := uint64(0)
			for _, id := range inputs[1:] {
				group, err := ensureGroup(id, nil)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if group.Words != 2 && group.Words != 1 {
					return physicalProgram{}, 0, nil, ErrBackendLimit
				}
				for word := 0; word < int(group.Words); word++ {
					emitter.storeOffset(4, registers[group.Indices[word]], argumentOffset+uint64(word*4), ir.ByteOrderNative)
				}
				argumentOffset += uint64(group.Words) * 4
			}
			callee, err := ensureGroup(inputs[0], nil)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			if callee.Words != 1 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			calleeLow := registers[callee.Indices[0]]
			seenClobbers := make(map[int]bool)
			for index, id := range contents {
				if id < 0 || seenClobbers[id] ||
					(ins.Op == mCallC && registers[index] != 0 && registers[index] != 1 && registers[index] != 2) {
					continue
				}
				seenClobbers[id] = true
				usesHere := 0
				for _, input := range inputs {
					if input == id {
						usesHere++
					}
				}
				if remaining[id] > usesHere && !program.Values[id].Constant && locations[id].Slot < 0 {
					group := residency.Group(id)
					slot, err := allocateGroupSlots(int(group.Words))
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					locations[id].Slot, locations[id].SlotWords = slot, group.Words
					for word := 0; word < int(group.Words); word++ {
						emitter.spill(registers[group.Indices[word]], slot+word)
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
				emitter.callC(CallEffect{})
				written[0], written[1], written[2] = true, true, true
			}
			seenClobbers = make(map[int]bool)
			for index, id := range contents {
				if id < 0 || seenClobbers[id] ||
					(ins.Op == mCallC && registers[index] != 0 && registers[index] != 1 && registers[index] != 2) {
					continue
				}
				seenClobbers[id] = true
				residency.Release(id)
			}
			result := regalloc.Group{Indices: [2]int{registerIndex(0), registerIndex(2)}, Words: 2}
			if result.Indices[0] < 0 || result.Indices[1] < 0 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			if err := residency.Claim(ins.Dst, result); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			if err := afterHomeCall(); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}
		if ins.Op == mSyscall || ins.Op == mCallC || ins.Op == mCallGo {
			for id, value := range program.Values {
				if value.Class != regalloc.Vector && value.Class != regalloc.Float {
					continue
				}
				group := residency.Group(id)
				if group.Words == 0 {
					continue
				}
				if remaining[id] > 0 && !value.Constant && locations[id].Slot < 0 {
					slot, err := allocateValueSlot(id)
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					locations[id].Slot, locations[id].SlotWords = slot, uint8(storageWords(id))
					spillValue(id, group, slot)
				}
				residency.Release(id)
			}
		}
		if ins.Op == mSyscall || ins.Op == mCallC || ins.Op == mCallGo {
			kind := SystemCall
			if ins.Op == mCallC || ins.Op == mCallGo {
				kind = FunctionCall
			}
			abi := ABIC
			if ins.Op == mCallGo {
				abi = ABIGo
			}
			effect, err := preflightWithEffect(CallSpec{Architecture: target.Architecture(), Kind: kind, ABI: abi, ArgumentCount: len(inputs) - 1, ArgumentWords: selectedArgumentWords(program, inputs[1:])}, ins.Effect)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			if ins.Effect != nil {
				custom := *ins.Effect
				if kind != FunctionCall || custom.Kind != FunctionCall || custom.ABI != abi ||
					custom.PPC64CABI != effect.PPC64CABI || custom.FunctionPointer != effect.FunctionPointer ||
					custom.Architecture != effect.Architecture || custom.ResultRegister != effect.ResultRegister ||
					custom.NumberRegister != "" || custom.RequiresCallStack != effect.RequiresCallStack ||
					custom.StackAlignment != effect.StackAlignment || len(custom.ArgumentRegisters) != len(effect.ArgumentRegisters) {
					return physicalProgram{}, 0, nil, ErrInvalidFunction
				}
				for i, name := range custom.ArgumentRegisters {
					if name != effect.ArgumentRegisters[i] {
						return physicalProgram{}, 0, nil, ErrInvalidFunction
					}
				}
				resultIncluded := false
				for _, name := range custom.ClobberedRegisters {
					if _, ok := physicalByName(name); !ok {
						return physicalProgram{}, 0, nil, ErrInvalidFunction
					}
					if name == custom.ResultRegister {
						resultIncluded = true
					}
				}
				if !resultIncluded {
					return physicalProgram{}, 0, nil, ErrInvalidFunction
				}
				effect = custom
			}
			overwritten := make(map[string]bool)
			for _, name := range effect.ClobberedRegisters {
				overwritten[name] = true
			}
			fixed := make([]string, 1, len(inputs))
			if ins.Op == mSyscall {
				fixed[0] = effect.NumberRegister
			} else {
				fixed[0] = target.RegisterName(target.CallTargetRegister())
			}
			overwritten[fixed[0]] = true
			for _, name := range effect.ArgumentRegisters {
				overwritten[name] = true
			}
			fixed = append(fixed, effect.ArgumentRegisters...)
			fixedRegs := make([]physReg, len(fixed))
			for i, name := range fixed {
				reg, ok := physicalByName(name)
				fixedOutsidePool := (ins.Op == mCallC || ins.Op == mCallGo) && i == 0 && reg == target.CallTargetRegister()
				fixedOutsidePool = fixedOutsidePool || ins.Op == mCallGo && target.Architecture() == "riscv64" && i == 9 && reg == 8
				fixedOutsidePool = fixedOutsidePool || (target.Architecture() == "ppc64" || target.Architecture() == "ppc64le") && (ins.Op == mSyscall && i == 0 && reg == 0 || ins.Op == mCallGo && i >= 9 && reg >= 14 && reg <= 17)
				if !ok || registerIndex(reg) < 0 && !fixedOutsidePool {
					return physicalProgram{}, 0, nil, ErrBackendLimit
				}
				fixedRegs[i] = reg
			}
			// Preserve only values that remain live across registers the call
			// will destroy. Call operands themselves stay in their current homes
			// until the parallel move schedule has consumed them.
			seenClobbers := make(map[int]bool)
			for index, id := range contents {
				if id < 0 || seenClobbers[id] || !overwritten[target.RegisterName(registers[index])] {
					continue
				}
				seenClobbers[id] = true
				usesHere := 0
				for _, input := range inputs {
					if input == id {
						usesHere++
					}
				}
				if remaining[id] > usesHere && !program.Values[id].Constant && locations[id].Slot < 0 {
					group := residency.Group(id)
					slot, err := allocateGroupSlots(int(group.Words))
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					locations[id].Slot, locations[id].SlotWords = slot, group.Words
					for word := 0; word < int(group.Words); word++ {
						emitter.spill(registers[group.Indices[word]], slot+word)
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
				if index := residency.First(id); index >= 0 {
					if src := registers[index]; src != fixedRegs[i] {
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
				for index, candidate := range registers {
					if machine.IsVectorRegister(candidate) || contents[index] != -1 {
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
					slot, err := allocateSlot()
					if err != nil {
						return physicalProgram{}, 0, nil, err
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
			freeSlots = append(freeSlots, temporarySlots...)
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
					return physicalProgram{}, 0, nil, ErrInvalidFunction
				}
				written[input.dst] = true
			}
			for index, id := range contents {
				if id >= 0 && overwritten[target.RegisterName(registers[index])] {
					residency.Release(id)
				}
			}
			linkSlot := -1
			returnAddress := target.ReturnAddress()
			hasLink := returnAddress.InRegister()
			linkReg, linkIsGeneral := returnAddress.GeneralRegister()
			if kind == FunctionCall && hasLink && !emitter.linkSavedInFrame() {
				linkSlot, err = allocateSlot()
				if err != nil {
					return physicalProgram{}, 0, nil, err
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
				if ok && !(kind == FunctionCall && hasLink && linkIsGeneral && reg == linkReg) {
					written[reg] = true
				}
			}
			if linkSlot >= 0 {
				emitter.restoreLink(linkSlot)
				freeSlots = append(freeSlots, linkSlot)
			}
			resultReg, ok := physicalByName(effect.ResultRegister)
			if !ok {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			resultIndex := registerIndex(resultReg)
			if resultIndex < 0 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			if err := residency.Claim(ins.Dst, regalloc.SingleRegister(resultIndex)); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			if err := afterHomeCall(); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && ins.Op == mShift && valueWords(ins.Dst) == 2 {
			if emitter.(*physicalEmitter).stack != StackPlatform {
				return physicalProgram{}, 0, nil, ErrStackRequired
			}
			pinned := make(map[int]bool, 2)
			source, err := ensureGroup(ins.A, pinned)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			if source.Words != 2 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			pinned[source.Indices[0]], pinned[source.Indices[1]] = true, true
			rotation := ins.Arithmetic == opRotateLeft || ins.Arithmetic == opRotateRight
			dst := regalloc.Group{}
			if remaining[ins.A] == 1 && !rotation {
				dst = source
			} else {
				dst, err = chooseForValue(ins.Dst, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
			}
			if dst.Words != 2 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			low, high := registers[source.Indices[0]], registers[source.Indices[1]]
			outLow, outHigh := registers[dst.Indices[0]], registers[dst.Indices[1]]
			count := uint8(ins.Imm)
			scratch := target.ScratchRegister()
			shiftOrMove := func(operation op, dst, src physReg, amount uint8) {
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
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			emitter.forgetFlags()
			written[outLow], written[outHigh] = true, true
			if dst.Contains(source.First()) {
				residency.Release(ins.A)
			}
			if err := residency.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && (ins.Op == mLoad64 || ins.Op == mStore64) {
			pinned := make(map[int]bool, 3)
			address, err := ensureGroup(ins.A, pinned)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			if address.Words != 1 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			pinned[address.First()] = true
			addressReg := registers[address.First()]
			if ins.Op == mStore64 {
				value, err := ensureGroup(ins.B, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if value.Words != 2 {
					return physicalProgram{}, 0, nil, ErrBackendLimit
				}
				emitter.storeOffset(addressReg, registers[value.Indices[0]], 0, ins.ByteOrder)
				emitter.storeOffset(addressReg, registers[value.Indices[1]], 4, ins.ByteOrder)
				consumeInputs(inputs)
				continue
			}
			dst, err := chooseForValue(ins.Dst, pinned)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			if dst.Words != 2 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			low, high := registers[dst.Indices[0]], registers[dst.Indices[1]]
			emitter.loadOffset(low, addressReg, 0, ins.ByteOrder)
			emitter.loadOffset(high, addressReg, 4, ins.ByteOrder)
			written[low], written[high] = true, true
			if err := residency.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" &&
			(ins.Op == mStore8 || (ins.Op == mScaled || ins.Op == mLoad8) && valueWords(ins.Dst) == 2) {
			pinned := make(map[int]bool)
			address, err := ensureGroup(ins.A, pinned)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			if address.Words != 2 && !(ins.Op == mStore8 && address.Words == 1) {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			for word := 0; word < int(address.Words); word++ {
				pinned[address.Indices[word]] = true
			}
			if ins.Op == mStore8 {
				value, err := ensureGroup(ins.B, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if value.Words != 1 && value.Words != 2 {
					return physicalProgram{}, 0, nil, ErrBackendLimit
				}
				addrReg := registers[address.Indices[0]]
				valueReg := registers[value.Indices[0]]
				if valueReg < 4 {
					emitter.store8(addrReg, valueReg)
				} else {
					// An x86 byte store can only name AL, CL, DL, or BL.
					// Borrow one of those registers through the spill scheduler.
					byteReg := physReg(0)
					if addrReg == byteReg {
						byteReg = 1
					}
					slot, err := allocateSlot()
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					emitter.spill(byteReg, slot)
					emitter.move(byteReg, valueReg)
					emitter.store8(addrReg, byteReg)
					emitter.reload(byteReg, slot)
					freeSlots = append(freeSlots, slot)
				}
				consumeInputs(inputs)
				continue
			}
			if ins.Op == mScaled {
				index, err := ensureGroup(ins.B, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if index.Words != 2 {
					return physicalProgram{}, 0, nil, ErrBackendLimit
				}
				for _, reg := range index.Indices {
					pinned[reg] = true
				}
				dst, err := chooseForValue(ins.Dst, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if dst.Words != 2 {
					return physicalProgram{}, 0, nil, ErrBackendLimit
				}
				baseLow, baseHigh := registers[address.Indices[0]], registers[address.Indices[1]]
				indexLow, indexHigh := registers[index.Indices[0]], registers[index.Indices[1]]
				dstLow, dstHigh := registers[dst.Indices[0]], registers[dst.Indices[1]]
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
				if err := residency.Claim(ins.Dst, dst); err != nil {
					return physicalProgram{}, 0, nil, err
				}
			} else {
				dst, err := chooseForValue(ins.Dst, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if dst.Words != 2 {
					return physicalProgram{}, 0, nil, ErrBackendLimit
				}
				low, high := registers[dst.Indices[0]], registers[dst.Indices[1]]
				emitter.load8(low, registers[address.Indices[0]])
				emitter.constant(high, 0)
				written[low], written[high] = true, true
				if err := residency.Claim(ins.Dst, dst); err != nil {
					return physicalProgram{}, 0, nil, err
				}
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && ins.Op == mSelect && (valueWords(ins.Dst) == 2 || valueWords(ins.A) == 2) {
			if emitter.(*physicalEmitter).stack != StackPlatform {
				return physicalProgram{}, 0, nil, ErrStackRequired
			}

			for _, id := range inputs {
				group, err := ensureGroup(id, nil)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if !program.Values[id].Constant && locations[id].Slot < 0 {
					slot, err := allocateGroupSlots(int(group.Words))
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					locations[id].Slot, locations[id].SlotWords = slot, group.Words
					spillValue(id, group, slot)
				}
				residency.Release(id)
			}
			// Materialize input homes before reusing the fixed scratch registers.
			seen := make(map[int]bool)
			for _, id := range contents {
				if id < 0 || seen[id] {
					continue
				}
				seen[id] = true
				group := residency.Group(id)
				if !program.Values[id].Constant && locations[id].Slot < 0 {
					slot, err := allocateGroupSlots(int(group.Words))
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					locations[id].Slot, locations[id].SlotWords = slot, group.Words
					for word := 0; word < int(group.Words); word++ {
						emitter.spill(registers[group.Indices[word]], slot+word)
					}
				}
				residency.Release(id)
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
			result := regalloc.SingleRegister(registerIndex(2))
			if valueWords(ins.Dst) == 2 {
				loadWord(3, ins.B, 1)
				loadWord(1, ins.C, 1)
				emitter.selectValue(6, 0, 3, 1)
				written[6] = true
				result = regalloc.Group{Indices: [2]int{registerIndex(2), registerIndex(6)}, Words: 2}
			}
			emitter.forgetFlags()
			if result.Indices[0] < 0 || result.Words == 2 && result.Indices[1] < 0 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			if err := residency.Claim(ins.Dst, result); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && ins.Op == mShiftBy {
			// The paired expansion uses EAX:EDX, CL and EBX. Give operands
			// homes before assigning those fixed locations, through the same
			// scheduler as ordinary pressure spills.
			for _, id := range inputs {
				group, err := ensureGroup(id, nil)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if !program.Values[id].Constant && locations[id].Slot < 0 && locations[id].IncomingWords == 0 {
					slot, err := allocateValueSlot(id)
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					locations[id].Slot, locations[id].SlotWords = slot, uint8(storageWords(id))
					spillValue(id, group, slot)
				}
			}
			seen := map[int]bool{}
			for index, id := range contents {
				if id < 0 || seen[id] || registers[index] > 3 {
					continue
				}
				seen[id] = true
				group := residency.Group(id)
				if !program.Values[id].Constant && locations[id].Slot < 0 && locations[id].IncomingWords == 0 {
					slot, err := allocateValueSlot(id)
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					locations[id].Slot, locations[id].SlotWords = slot, uint8(storageWords(id))
					spillValue(id, group, slot)
				}
				residency.Release(id)
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
			result := regalloc.SingleRegister(registerIndex(0))
			if valueWords(ins.Dst) == 2 {
				result = regalloc.Group{Indices: [2]int{registerIndex(0), registerIndex(2)}, Words: 2}
			}
			if err := residency.Claim(ins.Dst, result); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}

		if target.Architecture() == "386" && ins.Op == mBinary && ins.Bits == 64 && ins.Arithmetic == opMul {
			if emitter.(*physicalEmitter).stack != StackPlatform {
				return physicalProgram{}, 0, nil, ErrStackRequired
			}
			// MUL fixes the low product in EAX:EDX. Give both inputs durable
			// homes before writing those registers, including when a value is
			// also live after this multiplication.
			pinned := make(map[int]bool, 4)
			for _, id := range inputs {
				group, err := ensureGroup(id, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if group.Words != 2 {
					return physicalProgram{}, 0, nil, ErrInvalidFunction
				}
				pinned[group.Indices[0]], pinned[group.Indices[1]] = true, true
			}
			seen := make(map[int]bool)
			for _, id := range contents {
				if id < 0 || seen[id] {
					continue
				}
				seen[id] = true
				group := residency.Group(id)
				displaced := id == ins.A || id == ins.B
				for word := 0; word < int(group.Words); word++ {
					displaced = displaced || registers[group.Indices[word]] <= 3
				}
				if !displaced {
					continue
				}
				if !program.Values[id].Constant && locations[id].Slot < 0 {
					slot, err := allocateGroupSlots(int(group.Words))
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					locations[id].Slot, locations[id].SlotWords = slot, group.Words
					for word := 0; word < int(group.Words); word++ {
						emitter.spill(registers[group.Indices[word]], slot+word)
					}
				}
				residency.Release(id)
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
			result := regalloc.Group{Indices: [2]int{registerIndex(0), registerIndex(2)}, Words: 2}
			if result.Indices[0] < 0 || result.Indices[1] < 0 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			if err := residency.Claim(ins.Dst, result); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}

		if target.Architecture() == "386" && ins.Op == mMove && valueWords(ins.Dst) == 2 && valueWords(ins.A) == 2 {
			source, err := ensureGroup(ins.A, nil)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			dst := source
			if remaining[ins.A] > 1 {
				if locations[ins.A].Slot < 0 && !program.Values[ins.A].Constant {
					slot, err := allocateValueSlot(ins.A)
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					locations[ins.A].Slot, locations[ins.A].SlotWords = slot, 2
					spillValue(ins.A, source, slot)
				}
				residency.Release(ins.A)
				dst, err = chooseForValue(ins.Dst, nil)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				for word := 0; word < 2; word++ {
					reg := registers[dst.Indices[word]]
					if program.Values[ins.A].Constant {
						emitter.constant(reg, uint64(uint32(program.Values[ins.A].Imm>>(32*word))))
					} else {
						emitter.reload(reg, locations[ins.A].Slot+word)
					}
					written[reg] = true
				}
			} else {
				residency.Release(ins.A)
			}
			if err := residency.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}
		if target.Architecture() == "386" && valueWords(ins.Dst) == 2 &&
			(ins.Op == mSignExtend || ins.Op == mMove && valueWords(ins.A) == 1) {
			pinned := make(map[int]bool, 2)
			source, err := ensureGroup(ins.A, pinned)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			for word := 0; word < int(source.Words); word++ {
				pinned[source.Indices[word]] = true
			}
			if source.Words == 2 && remaining[ins.A] > 1 {
				// Only the low word is read by sign extension. Keep the live
				// snapshot in its slot instead of pinning another full pair.
				if locations[ins.A].Slot < 0 && !program.Values[ins.A].Constant {
					slot, err := allocateValueSlot(ins.A)
					if err != nil {
						return physicalProgram{}, 0, nil, err
					}
					locations[ins.A].Slot, locations[ins.A].SlotWords = slot, 2
					spillValue(ins.A, source, slot)
				}
				residency.Release(ins.A)
				for word := 0; word < 2; word++ {
					delete(pinned, source.Indices[word])
				}
			}
			dst := regalloc.Group{}
			if source.Words == 2 && remaining[ins.A] == 1 {
				dst = source
			} else {
				dst, err = chooseForValue(ins.Dst, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
			}
			if dst.Words != 2 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			low, high := registers[dst.Indices[0]], registers[dst.Indices[1]]
			input := registers[source.Indices[0]]
			if ins.Op == mSignExtend {
				emitter.signExtend(low, input, uint8(ins.Imm))
				emitter.shift(opShiftRightArithmetic, 32, high, low, 31)
				emitter.forgetFlags()
			} else {
				emitter.move(low, input)
				emitter.constant(high, 0)
			}
			written[low], written[high] = true, true
			if dst.Contains(source.First()) {
				residency.Release(ins.A)
			}
			if err := residency.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
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
				group, err := ensureGroup(id, nil)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				slot, err := allocateValueSlot(id)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				locations[id].Slot, locations[id].SlotWords = slot, 2
				spillValue(id, group, slot)
			}
			dst, err := chooseForValue(ins.Dst, nil)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			pinned := map[int]bool{dst.Indices[0]: true, dst.Indices[1]: true}
			temp, err := chooseClassGroup(1, pinned, regalloc.Group{}, regalloc.General)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			right := registers[temp.First()]
			loadWord := func(reg physReg, id, word int) {
				if program.Values[id].Constant {
					emitter.constant(reg, uint64(uint32(program.Values[id].Imm>>(32*word))))
				} else {
					emitter.reload(reg, locations[id].Slot+word)
				}
				written[reg] = true
			}
			for word := 0; word < 2; word++ {
				reg := registers[dst.Indices[word]]
				loadWord(reg, ins.A, word)
				loadWord(right, ins.B, word)
				if word == 1 && (ins.Arithmetic == opAdd || ins.Arithmetic == opSub) {
					emitter.binaryCarry(ins.Arithmetic, reg, reg, right)
				} else {
					emitter.binary(ins.Arithmetic, 32, reg, reg, right)
				}
			}
			emitter.forgetFlags()
			if err := residency.Claim(ins.Dst, dst); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}
		if ins.Dst >= 0 && valueWords(ins.Dst) == 2 && (ins.Op == mMove ||
			ins.Op == mBinary && ins.Bits == 64 &&
				(ins.Arithmetic == opAdd || ins.Arithmetic == opSub || ins.Arithmetic == opAnd || ins.Arithmetic == opOr || ins.Arithmetic == opXor)) {
			groups := make([]regalloc.Group, len(inputs))
			pinned := make(map[int]bool, len(inputs)*2)
			for i, id := range inputs {
				group, err := ensureGroup(id, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				if group.Words != 2 {
					return physicalProgram{}, 0, nil, ErrInvalidFunction
				}
				groups[i] = group
				for word := 0; word < 2; word++ {
					pinned[group.Indices[word]] = true
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
			if remaining[ins.A] == usesHere(ins.A) && target.CanReuse(ins, 0) {
				dstGroup = groups[0]
			} else if ins.Op == mBinary && ins.Arithmetic != opSub && remaining[ins.B] == usesHere(ins.B) {
				// All supported operations except subtraction are commutative.
				dstGroup = groups[1]
				groups[0], groups[1] = groups[1], groups[0]
			} else {
				var err error
				dstGroup, err = chooseForValue(ins.Dst, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
			}
			if dstGroup.Words != 2 {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			for word := 0; word < 2; word++ {
				dst := registers[dstGroup.Indices[word]]
				left := registers[groups[0].Indices[word]]
				if ins.Op == mMove {
					if dst != left {
						emitter.move(dst, left)
						written[dst] = true
					}
				} else {
					right := registers[groups[1].Indices[word]]
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
				if residency.Group(id).Contains(dstGroup.First()) {
					residency.Release(id)
				}
			}
			if err := residency.Claim(ins.Dst, dstGroup); err != nil {
				return physicalProgram{}, 0, nil, err
			}
			consumeInputs(inputs)
			if remaining[ins.Dst] == 0 {
				residency.Release(ins.Dst)
			}
			continue
		}
		pinned := make(map[int]bool, len(inputs))
		// Instruction-local fixed registers participate in allocation. Relocate
		// their live owners before loading operands; the normal spill scheduler
		// supplies durable homes when register pressure forces an eviction.
		if constraints, ok := target.(interface {
			FixedClobbers(machineInstruction) []physReg
		}); ok {
			fixed := constraints.FixedClobbers(ins)
			for _, reg := range fixed {
				if index := registerIndex(reg); index >= 0 {
					pinned[index] = true
				}
			}
			for _, reg := range fixed {
				index := registerIndex(reg)
				if index < 0 || contents[index] < 0 {
					continue
				}
				id := contents[index]
				old := residency.Group(id)
				group, err := chooseForValue(id, pinned)
				if err != nil {
					return physicalProgram{}, 0, nil, err
				}
				for word := 0; word < int(old.Words); word++ {
					to, from := registers[group.Indices[word]], registers[old.Indices[word]]
					emitter.move(to, from)
					written[to] = true
				}
				residency.Release(id)
				if err := residency.Claim(id, group); err != nil {
					return physicalProgram{}, 0, nil, err
				}
			}
			for _, reg := range fixed {
				written[reg] = true
			}
		}
		inputRegs := make([]physReg, len(inputs))
		selectFromFlags := false
		if ins.Op == mSelect && target.Architecture() == "arm64" {
			flags := emitter.currentFlags()
			selectFromFlags = flags.Zero.Valid && flags.Zero.Value == ins.A
		}
		for i, id := range inputs {
			if selectFromFlags && i == 0 {
				continue
			}
			group, err := ensureGroup(id, pinned)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			if group.Words != 1 && !(ins.Op == mNarrow && i == 0 && valueWords(ins.Dst) == 1 && target.RegisterBits() == 32) {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			for word := 0; word < int(group.Words); word++ {
				pinned[group.Indices[word]] = true
			}
			inputRegs[i] = registers[group.First()]
		}
		if ins.Op == mVector && ins.Arithmetic == ir.OpVStore {
			emitter.vectorInstruction(machine.Instruction{Op: machine.OpVector, Arithmetic: ins.Arithmetic, Bits: ins.Bits, VectorType: ins.Type, A: inputRegs[0], B: inputRegs[1]})
			consumeInputs(inputs)
			continue
		}
		if ins.Op == mStore64 || ins.Op == mStore8 || ins.Op == mStoreAddress || ins.Op == mStoreOffset {
			if ins.Op == mStoreAddress {
				emitter.storeAddress(inputRegs[0], inputRegs[1], inputRegs[2], ins.Imm, ins.Disp, ins.ByteOrder)
			} else if ins.Op == mStoreOffset {
				emitter.storeOffset(inputRegs[0], inputRegs[1], ins.Disp, ins.ByteOrder)
			} else if ins.Op == mStore8 {
				emitter.store8(inputRegs[0], inputRegs[1])
			} else {
				emitter.store64(inputRegs[0], inputRegs[1], ins.ByteOrder)
			}
			consumeInputs(inputs)
			continue
		}
		dstIndex := -1
		if hint := preferred[ins.Dst]; hint >= 0 && !pinned[hint] && contents[hint] == -1 && (!homeReserved(registers[hint]) || int(registers[hint]) == allowedHome) &&
			machine.IsVectorRegister(registers[hint]) == (program.Values[ins.Dst].Class == regalloc.Float || program.Values[ins.Dst].Class == regalloc.Vector) {
			dstIndex = hint
		}
		seenInputs := make(map[int]bool, len(inputs))
		for inputPos, id := range inputs {
			if dstIndex >= 0 {
				break
			}
			if seenInputs[id] || program.Values[id].Class != program.Values[ins.Dst].Class || !(target.CanReuse(ins, inputPos) || program.Values[ins.Dst].Class == regalloc.Float && (fpInstruction(ins) || ins.Op == machine.SelectedFloatMove) && (target.Architecture() != "amd64" || inputPos == 0)) {
				continue
			}
			seenInputs[id] = true
			usesHere := 0
			for _, input := range inputs {
				if input == id {
					usesHere++
				}
			}
			if remaining[id] == usesHere && residency.Group(id).Words == 1 && (!homeReserved(registers[residency.First(id)]) || int(registers[residency.First(id)]) == allowedHome) {
				dstIndex = residency.First(id)
				break
			}
		}
		if dstIndex < 0 {
			var err error
			var group regalloc.Group
			group, err = chooseForValue(ins.Dst, pinned)
			if err != nil {
				// A destructive instruction can reuse a still-live SSA operand
				// after giving that snapshot a durable home. Variable homes remain
				// reserved; their contents cannot be overwritten by this fallback.
				reused := false
				for inputPos, id := range inputs {
					if !target.CanReuse(ins, inputPos) || program.Values[id].Class != program.Values[ins.Dst].Class {
						continue
					}
					source := residency.Group(id)
					if source.Words != 1 && !(ins.Op == mNarrow && source.Words == 2) {
						continue
					}
					reg := registers[source.First()]
					if homeReserved(reg) && int(reg) != allowedHome {
						continue
					}
					if !program.Values[id].Constant && locations[id].Slot < 0 {
						slot, saveErr := allocateValueSlot(id)
						if saveErr != nil {
							return physicalProgram{}, 0, nil, saveErr
						}
						locations[id].Slot, locations[id].SlotWords = slot, uint8(storageWords(id))
						spillValue(id, source, slot)
					}
					group = regalloc.SingleRegister(source.First())
					reused = true
					break
				}
				if !reused {
					return physicalProgram{}, 0, nil, fmt.Errorf("allocate result for selected operation %v/%v at %d: %w", ins.Op, ins.Arithmetic, position, err)
				}
			}
			if group.Words != 1 {
				return physicalProgram{}, 0, nil, fmt.Errorf("scalar operation selected a %d-word result: %w", group.Words, ErrBackendLimit)
			}
			dstIndex = group.First()
		}
		dst := registers[dstIndex]
		floatOperand := func(id, position int) physReg {
			if ins.LiteralMask&(1<<position) == 0 {
				return registers[residency.First(id)]
			}
			n := uint8(position)
			if target.Architecture() == "amd64" && ins.Op == mFloatFMA {
				n = []uint8{1, 2, 0}[position]
			}
			if target.Architecture() == "arm64" {
				n += 16
			}
			reg := machine.VectorRegister(n)
			width := ins.Bits
			if ins.Op == mFloatUnary && ins.Arithmetic == ir.OpFConvert {
				width = 96 - width
			}
			emitter.(*physicalEmitter).literal(reg, width, literalBytes(width, program.Values[id].Imm, target.NativeByteOrder()))
			return reg
		}
		switch ins.Op {
		case mVector:
			if ins.Arithmetic == ir.OpVConst {
				emitter.(*physicalEmitter).literal(dst, 128, ins.Data)
				break
			}
			physical := machine.Instruction{Op: machine.OpVector, Arithmetic: ins.Arithmetic, Bits: ins.Bits, VectorType: ins.Type, A: dst, Imm: ins.Imm}
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
			if program.Values[ins.Dst].Class == regalloc.Float {
				w := ins.Bits
				if w == 0 {
					w = 64
				}
				emitter.(*physicalEmitter).literal(dst, w, literalBytes(w, ins.Imm, target.NativeByteOrder()))
			} else {
				emitter.constant(dst, ins.Imm)
			}
		case machine.SelectedFloatMove:
			emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatMove, Bits: ins.Bits, A: dst, B: inputRegs[0]})
		case machine.SelectedFloatBits:
			if target.Architecture() == "amd64" {
				data := make([]byte, 16)
				copy(data, literalBytes(ins.Bits, ins.Imm, ir.ByteOrderLittleEndian))
				emitter.(*physicalEmitter).literal(machine.VectorRegister(0), 128, data)
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
			if reusableWordCarry(emitter.currentFlags().Carry, ins.Arithmetic, ins.Bits, ins.A, ins.B, program.Values) {
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
				if reusableWordCarry(emitter.currentFlags().Carry, ins.Arithmetic, ins.Bits, ins.Args[2], ins.Args[3], program.Values) {
					mode = machine.CarryFlags
				}
			}
			emitter.wordCarry(ins.Arithmetic, ins.Bits, dst, inputRegs[0], inputRegs[1], inputRegs[2], lowRight, mode)
			emitter.bindFlagValue(ins.Dst, ins.A, ins.B, ins.Arithmetic, false)
		case mMulHigh:
			emitter.mulHigh(ins.Bits, dst, inputRegs[0], inputRegs[1])
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
				emitter.selectFlags(dst, inputRegs[1], inputRegs[2], BranchNotEqual)
			} else {
				emitter.selectValue(dst, inputRegs[0], inputRegs[1], inputRegs[2])
			}
		case mLoad64:
			emitter.load64(dst, inputRegs[0], ins.ByteOrder)
		case mLoad8:
			emitter.load8(dst, inputRegs[0])
		case mLoadAddress:
			emitter.loadAddress(dst, inputRegs[0], inputRegs[1], ins.Imm, ins.Disp, ins.ByteOrder)
		case mLoadOffset:
			emitter.loadOffset(dst, inputRegs[0], ins.Disp, ins.ByteOrder)
		default:
			return physicalProgram{}, 0, nil, ErrInvalidFunction
		}
		if !machine.IsVectorRegister(dst) && ins.Op != machine.SelectedFloatMove && (ins.Op != mMove || dst != inputRegs[0]) {
			written[dst] = true
		}
		if oldID := contents[dstIndex]; oldID >= 0 {
			residency.Release(oldID)
		}
		if err := residency.Claim(ins.Dst, regalloc.SingleRegister(dstIndex)); err != nil {
			return physicalProgram{}, 0, nil, err
		}
		consumeInputs(inputs)
		if remaining[ins.Dst] == 0 {
			residency.Release(ins.Dst)
		}
	}
	exportSlots := make([]int, len(program.Exports))
	if program.DirectExports {
		exportSlots = nil
	}
	if !program.DirectExports {
		for i, id := range program.Exports {
			group, err := ensureGroup(id, nil)
			if err != nil {
				return physicalProgram{}, 0, nil, err
			}
			if slots+int(group.Words) > maxSpillSlots {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			exportSlots[i] = slots
			slots += int(group.Words)
			for word := 0; word < int(group.Words); word++ {
				emitter.spill(registers[group.Indices[word]], exportSlots[i]+word)
			}
		}
	}
	if program.DiscardResult {
		slots = referencedSpillSlots(emitter.(*physicalEmitter).machineStream().Instructions())
		output, err := emitter.bodyBare(target.ResultRegister(), slots)
		return output, workspaceBytesForSlots(target, emitter, slots), written, err
	}
	resultGroup, err := ensureGroup(program.Result, nil)
	if err != nil {
		return physicalProgram{}, 0, nil, err
	}
	resultIndex := resultGroup.First()
	var output physicalProgram
	if final {
		planAllocatedPreservation(target, emitter, written)
	}
	slots = referencedSpillSlots(emitter.(*physicalEmitter).machineStream().Instructions())
	if len(program.ResultParts) != 0 {
		if resultGroup.Words != 1 || target.RegisterBits() != 64 {
			return physicalProgram{}, 0, nil, ErrBackendLimit
		}
		pinned := map[int]bool{resultIndex: true}
		highGroup, err := ensureGroup(program.ResultParts[1], pinned)
		if err != nil {
			return physicalProgram{}, 0, nil, err
		}
		if highGroup.Words != 1 {
			return physicalProgram{}, 0, nil, ErrBackendLimit
		}
		if !final {
			output, err = emitter.bodyBare(registers[resultIndex], slots)
			output.resultWords = [2]physReg{registers[resultIndex], registers[highGroup.First()]}
			output.resultWordCount = 2
		} else {
			pair, supported := target.Integer128Result()
			if !supported {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			abiLow, abiHigh := pair[0], pair[1]
			pairEmitter, ok := emitter.(interface {
				finishPair(physReg, physReg, physReg, physReg, int) (physicalProgram, int, error)
			})
			if !ok {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			if registers[resultIndex] != abiLow {
				written[abiLow] = true
			}
			if registers[highGroup.First()] != abiHigh {
				written[abiHigh] = true
			}
			output, slots, err = pairEmitter.finishPair(registers[resultIndex], registers[highGroup.First()], abiLow, abiHigh, slots)
		}
	} else if resultGroup.Words == 2 {
		if final {
			pairEmitter, ok := emitter.(interface {
				finishPair(physReg, physReg, physReg, physReg, int) (physicalProgram, int, error)
			})
			if !ok || len(entry.result.Words) != 2 || entry.result.Words[0].Storage != ABIRegister || entry.result.Words[1].Storage != ABIRegister {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			low, lowOK := physicalByName(entry.result.Words[0].Register)
			high, highOK := physicalByName(entry.result.Words[1].Register)
			if !lowOK || !highOK {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			if registers[resultGroup.Indices[0]] != low {
				written[low] = true
			}
			if registers[resultGroup.Indices[1]] != high {
				written[high] = true
			}
			output, slots, err = pairEmitter.finishPair(registers[resultGroup.Indices[0]], registers[resultGroup.Indices[1]], low, high, slots)
		} else if bare {
			output, err = emitter.bodyBare(registers[resultIndex], slots)
			output.resultWords = [2]physReg{registers[resultGroup.Indices[0]], registers[resultGroup.Indices[1]]}
			output.resultWordCount = 2
		} else {
			return physicalProgram{}, 0, nil, ErrBackendLimit
		}
	} else if final {
		if len(entry.result.Words) == 2 {
			narrowEmitter, ok := emitter.(interface {
				finishNarrowPair(physReg, physReg, physReg, int) (physicalProgram, error)
			})
			if !ok || entry.result.Words[0].Storage != ABIRegister || entry.result.Words[1].Storage != ABIRegister {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			low, lowOK := physicalByName(entry.result.Words[0].Register)
			high, highOK := physicalByName(entry.result.Words[1].Register)
			if !lowOK || !highOK {
				return physicalProgram{}, 0, nil, ErrBackendLimit
			}
			written[high] = true
			if registers[resultIndex] != low {
				written[low] = true
			}
			output, err = narrowEmitter.finishNarrowPair(registers[resultIndex], low, high, slots)
		} else {
			if registers[resultIndex] != target.ResultRegister() {
				written[target.ResultRegister()] = true
			}
			output, err = emitter.finish(registers[resultIndex], slots)
		}
	} else if bare {
		output, err = emitter.bodyBare(registers[resultIndex], slots)
	} else {
		output, err = emitter.body(registers[resultIndex], slots)
	}
	if err != nil {
		return physicalProgram{}, 0, nil, err
	}
	output.exportSlots = exportSlots
	if program.DirectExports {
		output.exportHomes = make([]exportLocation, len(program.Exports))
		for i, id := range program.Exports {
			location := exportLocation{Float: program.Values[id].Class == regalloc.Float, Bits: program.Values[id].Bits, Register: -1, Slot: locations[id].Slot}
			if index := residency.First(id); index >= 0 {
				location.Register = int(registers[index])
			} else if location.Slot < 0 && program.Values[id].Constant {
				location.Constant, location.Immediate = true, program.Values[id].Imm
			} else if location.Slot < 0 {
				return physicalProgram{}, 0, nil, ErrInvalidFunction
			}
			output.exportHomes[i] = location
		}
	}
	if bare || len(program.ResultParts) != 0 && !final {
		output.resultPhysical = registers[resultIndex]
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
			finalSlots = max(finalSlots, referencedSpillSlots([]machine.Instruction{located.ins}))
		}
		workspaceBytes = finalSlots * int(target.RegisterBits()/8)
	}
	return output, workspaceBytes, written, nil
}

func ppc64CPULevel(target machineTarget) abi.PPC64CPU {
	if policy, ok := target.(interface{ CPU() abi.PPC64CPU }); ok {
		return policy.CPU()
	}
	return abi.PPC64CPUUnspecified
}

func reusableWordCarry(fact machine.FlagFact, operation op, width uint8, left, right int, values []machine.SelectedValue) bool {
	if !fact.Valid || fact.Width != width || fact.Operation != operation {
		return false
	}
	if fact.ImmediateRight {
		return fact.Left == left && right >= 0 && right < len(values) && values[right].Constant && values[right].Imm == fact.Immediate
	}
	return fact.Left == left && fact.Right == right || operation == opAdd && fact.Left == right && fact.Right == left
}
