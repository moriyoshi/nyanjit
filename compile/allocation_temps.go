package compile

import (
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
)

// allocationTemps belongs to one lowering. Generations reset sparse block use
// of function-wide variable IDs without an O(variables) clear for every block.
// This is ordinary per-compile storage; recycling across compiles is separate.
type allocationTemps struct {
	history struct {
		homeValues, homeGenerations, definedAt, previousCall, entryWords, liveFP, homesSaved, registers, literalUses machine.BufferHistory
		onlineLastUse, onlineByParam, onlineClasses, onlineInputStart, onlineDefinedAt, onlineSize                   machine.BufferHistory
		onlineVarMarks, onlineSnapshot, onlineKills, onlineHomeAt                                                    machine.BufferHistory
	}
	allocator regalloc.Residency
	// The port onto regalloc.Allocator: Online, the description it reads,
	// and describeOnline's buffers. Neither regalloc type reports what it
	// keeps, so onlineSize, the largest function described since they were
	// last dropped (values, positions and operands), stands for it.
	online     regalloc.Online
	function   regalloc.FunctionBuilder
	onlineSize int
	// The operands and Online's table entries of that function.
	onlineOperandCount, onlineTableCount int
	onlineLastUse                        []int
	onlineByParam                        []int
	onlineParams                         []int
	onlineArguments                      []abi.ValueLocation
	onlineOperands                       []regalloc.Operand
	onlineClasses                        []backend.Class
	onlineInputs                         []int
	onlineInputStart                     []int
	onlineDefinedAt                      []int
	onlinePrefers                        []onlinePrefer
	onlineHinted                         []int
	onlineFinal                          []int
	onlineRuns                           []onlineRun
	onlineSegments                       []onlineSegment
	onlineTransitions                    []onlineTransition
	onlineTransitionAt                   []int32
	onlineHomeDefs                       []onlineHomeDef
	onlineVarMarks                       []uint8
	onlineSnapshot                       []int16
	onlineSnapFrom                       []int32 // per value, the position after which onlineSnapshot applies
	onlineKills                          []int
	onlineHomeAt                         []machine.Home // per position, a home access's home
	onlineExportGroups                   []regalloc.Group
	onlineSegScopes                      []int
	// planOnlineCalls' calls and the effects, locations and registers the
	// builder's call positions point into.
	onlineCalls         []onlineCall
	onlineCallEffects   []abi.CallEffect
	onlineCallLocations []abi.ValueLocation
	onlineCallWords     []abi.WordLocation
	onlineCallFixed     []physReg
	onlineCallNames     []string
	onlineCallWrites    []physReg
	onlineCallHomes     []machine.Home
	onlineArgumentWords []int
	// plan386's kinds per position, the scratch values they need and the
	// stream indices of the temporaries' spills and reloads.
	// onlineSteps are a control program's blocks; see allocateOnlineControl.
	onlineSteps       []onlineStep
	onlineControlInts []int
	// A control program's selected programs, copied out of the selection
	// buffers.
	onlineInstructions []machineInstruction
	onlineValues       []machine.SelectedValue
	onlineScopes       []machine.SelectedScope
	onlineBodies       []locatedInsn // the blocks' emitted bodies
	onlineEscaping     []bool        // per value of a block: an edge passes it or it is returned
	online386          []uint8
	online386Scratch   int
	online386Patches   []int
	mover              allocationMover
	program            allocationProgram
	definedAt          []int
	previousCall       []int
	entryWords         []int
	liveFP             []int
	registers          []physReg
	literalUses        []int // foldFloatLiterals' use counts
	homesSaved         []machine.Home
	homeValues         []int
	homeGenerations    []uint32
	generation         uint32
}

func (t *allocationTemps) beginHomes(count int) {
	t.history.homeValues.Observe(count)
	t.history.homeGenerations.Observe(count)
	if len(t.homeValues) < count {
		t.homeValues = make([]int, count)
		t.homeGenerations = make([]uint32, count)
	}
	t.generation++
	if t.generation == 0 {
		clear(t.homeGenerations)
		t.generation = 1
	}
}
func (t *allocationTemps) setHome(index, value int) {
	t.homeValues[index] = value + 1
	t.homeGenerations[index] = t.generation
}
func (t *allocationTemps) home(index int) int {
	if t.homeGenerations[index] == t.generation {
		return t.homeValues[index]
	}
	return 0
}

func (t *allocationTemps) retainedBytes() int {
	allocator, _ := t.allocator.RetainedBytes(scratchBufferLimit)
	return allocator + bufferBytes(t.homeValues) + bufferBytes(t.homeGenerations) + bufferBytes(t.definedAt) + bufferBytes(t.previousCall) + bufferBytes(t.entryWords) + bufferBytes(t.liveFP) + bufferBytes(t.homesSaved) + bufferBytes(t.registers) + bufferBytes(t.literalUses) +
		t.onlineRetained() + bufferBytes(t.onlineLastUse) + bufferBytes(t.onlineClasses) + bufferBytes(t.onlineInputs) + bufferBytes(t.onlineInputStart) + bufferBytes(t.onlineDefinedAt) + bufferBytes(t.onlineOperands) +
		bufferBytes(t.onlineByParam) + bufferBytes(t.onlineVarMarks) + bufferBytes(t.onlineSnapshot) + bufferBytes(t.onlineSnapFrom) + bufferBytes(t.onlineKills) + bufferBytes(t.onlineHomeAt) + bufferBytes(t.onlineParams) + bufferBytes(t.onlineArguments) +
		bufferBytes(t.onlinePrefers) + bufferBytes(t.onlineHinted) + bufferBytes(t.onlineFinal) + bufferBytes(t.onlineRuns) + bufferBytes(t.onlineSegments) + bufferBytes(t.onlineHomeDefs) + bufferBytes(t.onlineExportGroups) +
		bufferBytes(t.onlineCalls) + bufferBytes(t.onlineCallEffects) + bufferBytes(t.onlineCallLocations) + bufferBytes(t.onlineCallWords) + bufferBytes(t.onlineCallFixed) + bufferBytes(t.onlineCallNames) + bufferBytes(t.onlineCallWrites) + bufferBytes(t.onlineCallHomes) + bufferBytes(t.onlineArgumentWords) + bufferBytes(t.online386) + bufferBytes(t.online386Patches)
}

// onlineBytesPerUnit bounds what Online and the FunctionBuilder keep per
// value, position and operand of the largest function described: the
// builder's copies of each, Online's per-value state, use lists and tables.
const onlineBytesPerUnit = 160

func (t *allocationTemps) onlineRetained() int { return t.onlineSize * onlineBytesPerUnit }

// onlineLargest bounds the largest single buffer among them, which the
// per-buffer limit applies to: the builder's operands, or Online's integer
// tables, four entries per value and two per position.
func (t *allocationTemps) onlineLargest() int {
	return max(t.onlineOperandCount*int(unsafe.Sizeof(regalloc.Operand{})), 8*t.onlineTableCount)
}

// observeOnline records the size of a function described to Online, b.
func (t *allocationTemps) observeOnline(b *regalloc.FunctionBuilder, inputs int) {
	size := b.Values() + 2*b.Positions() + inputs + b.Edges()
	t.history.onlineSize.Observe(size)
	t.onlineSize = max(t.onlineSize, size)
	// Each position has its inputs and at most one definition.
	t.onlineOperandCount = max(t.onlineOperandCount, inputs+b.Positions())
	t.onlineTableCount = max(t.onlineTableCount, 4*b.Values()+2*b.Positions()+5*b.Edges())
}

func (t *allocationTemps) oversized() bool {
	if _, big := t.allocator.RetainedBytes(scratchBufferLimit); big {
		return true
	}
	for _, n := range []int{bufferBytes(t.homeValues), bufferBytes(t.homeGenerations), bufferBytes(t.definedAt), bufferBytes(t.previousCall), bufferBytes(t.entryWords), bufferBytes(t.liveFP), bufferBytes(t.homesSaved), bufferBytes(t.registers), bufferBytes(t.literalUses),
		t.onlineLargest(), bufferBytes(t.onlineLastUse), bufferBytes(t.onlineInputs), bufferBytes(t.onlineOperands)} {
		if n > scratchBufferLimit {
			return true
		}
	}
	return false
}

// poisonAllocationTemps overwrites retained storage in poison builds. The
// Allocator poisons its own buffers in Finish.
func poisonAllocationTemps(t *allocationTemps) {
	for _, buf := range [][]int{t.homeValues, t.definedAt, t.previousCall, t.entryWords, t.liveFP, t.literalUses} {
		buf = buf[:cap(buf)]
		for i := range buf {
			buf[i] = -0x5a5a5a
		}
	}
	t.homeGenerations = t.homeGenerations[:cap(t.homeGenerations)]
	for i := range t.homeGenerations {
		t.homeGenerations[i] = 0x5a5a5a5a
	}
	for i := range t.homesSaved[:cap(t.homesSaved)] {
		t.homesSaved[:cap(t.homesSaved)][i] = machine.Home{Slot: -0x5a5a, Register: -0x5a5a, Bits: 255}
	}
	for i := range t.registers[:cap(t.registers)] {
		t.registers[:cap(t.registers)][i] = 255
	}
}

func (b *allocationTemps) decay() {
	b.allocator.Decay()
	b.allocator.Trim(scratchBufferLimit)
	decayBuffer(&b.homeValues, &b.history.homeValues)
	decayBuffer(&b.homeGenerations, &b.history.homeGenerations)
	decayBuffer(&b.definedAt, &b.history.definedAt)
	decayBuffer(&b.previousCall, &b.history.previousCall)
	decayBuffer(&b.entryWords, &b.history.entryWords)
	decayBuffer(&b.liveFP, &b.history.liveFP)
	decayBuffer(&b.homesSaved, &b.history.homesSaved)
	decayBuffer(&b.registers, &b.history.registers)
	decayBuffer(&b.literalUses, &b.history.literalUses)
	// Online and its description are dropped together, as one buffer.
	if excess := b.history.onlineSize.FinishCycle(b.onlineSize); b.onlineLargest() > scratchBufferLimit || b.onlineRetained() > 4096 && excess {
		b.online, b.function, b.onlineSize = regalloc.Online{}, regalloc.FunctionBuilder{}, 0
		b.onlineOperandCount, b.onlineTableCount = 0, 0
	}
	decayBuffer(&b.onlineLastUse, &b.history.onlineLastUse)
	decayBuffer(&b.onlineClasses, &b.history.onlineClasses)
	decayBuffer(&b.onlineInputStart, &b.history.onlineInputStart)
	decayBuffer(&b.onlineDefinedAt, &b.history.onlineDefinedAt)
	decayBuffer(&b.onlineByParam, &b.history.onlineByParam)
	decayBuffer(&b.onlineVarMarks, &b.history.onlineVarMarks)
	decayBuffer(&b.onlineSnapshot, &b.history.onlineSnapshot)
	decayBuffer(&b.onlineSnapFrom, &b.history.onlineSnapshot)
	decayBuffer(&b.onlineKills, &b.history.onlineKills)
	decayBuffer(&b.onlineHomeAt, &b.history.onlineHomeAt)
	// The rest grow with the function's inputs, positions or exports and
	// go with the description.
	if b.onlineSize == 0 || bufferBytes(b.onlineInputs) > scratchBufferLimit || bufferBytes(b.onlineOperands) > scratchBufferLimit {
		b.onlineInputs, b.onlineOperands, b.onlineParams, b.onlineArguments = nil, nil, nil, nil
		b.onlinePrefers, b.onlineHinted, b.onlineFinal, b.onlineRuns = nil, nil, nil, nil
		b.onlineSegments, b.onlineHomeDefs, b.onlineExportGroups, b.onlineSegScopes = nil, nil, nil, nil
		b.onlineCalls, b.onlineCallEffects, b.onlineCallLocations, b.onlineCallWords = nil, nil, nil, nil
		b.onlineCallFixed, b.onlineCallNames, b.onlineCallHomes, b.onlineArgumentWords, b.onlineCallWrites = nil, nil, nil, nil, nil
		b.online386, b.online386Patches = nil, nil
	}
}
