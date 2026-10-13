// Package abi defines calling conventions, stack policy, call effects, CPU
// profiles and emission reports. It is the lowest layer: it imports no other
// nyanjit package.
//
// Construct this package's exported structs with keyed fields. Field order is
// not part of the API: fields may be reordered or packed, though removing a
// field or changing its meaning is a breaking change.
package abi

// Flavor selects the calling convention at a generated function boundary.
type Flavor uint8

const (
	// C uses the platform C integer calling convention.
	C Flavor = iota
	// Go uses the current toolchain's ABIInternal integer register order.
	Go
)

// StackPractice determines whether lowering may use a platform stack frame.
type StackPractice uint8

const (
	StackNoUse StackPractice = iota
	StackPlatform
)

// StackPointerRole says whether SP is reserved or an ordinary register.
type StackPointerRole uint8

const (
	StackPointerReserved StackPointerRole = iota
	StackPointerOrdinary
)

// SpillStorage reports where evicted virtual values are kept.
type SpillStorage uint8

const (
	SpillNone SpillStorage = iota
	SpillWorkspace
	SpillStack
)

// Storage says where one word of a value lives at a function boundary.
type Storage uint8

const (
	Register Storage = iota
	Stack
	// Frame is a slot of the function's own frame, numbered as an allocator
	// numbers its spill slots: a value the client stored there before the
	// function's code runs, such as a parameter an earlier code segment left
	// in memory. Only entry arguments use it.
	Frame
)

// WordLocation places one native word of a value: a named register, a stack
// slot StackOffset bytes above the stack pointer at the boundary (at entry for
// entry layouts, at the call instruction for outbound calls), or a frame slot.
type WordLocation struct {
	Storage     Storage
	Register    string
	StackOffset int
	Slot        int // Frame
}

// ValueLocation places every word of one argument or result, low word first.
type ValueLocation struct{ Words []WordLocation }

// LoweringStats describes block allocation work for performance diagnostics.
// Retries count discarded speculative allocations, not layout fixpoint rounds.
type LoweringStats struct {
	Blocks, Allocations, Retries int
	// NarrowMemoryRetries counts successful byte-expansion retries after a
	// native 16/32-bit memory plan exceeds its register or frame limits.
	NarrowMemoryRetries int
}

// EmissionReport describes resources and effects after allocation.
// ClobberedRegisters excludes trampoline writes.
type EmissionReport struct {
	CPU              CPUProfile
	VectorOperations []VectorOperation
	// AssumedVectorBytes records a scalable vector length; phase 1 leaves it zero.
	AssumedVectorBytes int
	Architecture       string
	ABI                Flavor
	PPC64CPU           PPC64CPU
	GoManaged          bool
	Stack              StackPractice
	StackPointer       StackPointerRole
	ArgumentRegisters  []string
	ResultRegister     string
	ArgumentLocations  []ValueLocation
	ResultLocation     ValueLocation
	// ResultWords counts logical uint64 words; a scalar uint64 on 386 still
	// occupies two physical 32-bit registers.
	ResultWords          int
	WorkspaceRegister    string
	ClobberedRegisters   []string
	ClobberedFPRegisters []string
	// ClobberedSpecialRegisters names modified resources such as LR, CTR,
	// condition fields, and status registers independently of general registers.
	ClobberedSpecialRegisters []string
	FlagsClobbered            bool
	// FPStatusModified reports arithmetic that may change FP exception flags
	// or a call whose FP status effects are not known at this boundary.
	FPStatusModified     bool
	ExplicitMemoryReads  bool
	ExplicitMemoryWrites bool
	MayReadMemory        bool
	MayWriteMemory       bool
	HasSystemCalls       bool
	HasFunctionCalls     bool
	CodeBytes            int
	WorkspaceBytes       int
	WorkspaceAlignment   int
	StackBytes           int
	CallStackBytes       int
	SpillStorage         SpillStorage
	LoweringStats        LoweringStats
}

// CallKind distinguishes ordinary function calls from system calls.
type CallKind uint8

const (
	FunctionCall CallKind = iota
	SystemCall
)

// CallSpec describes an outbound call for PreflightCall: its convention, kind
// and argument shape.
type CallSpec struct {
	// Architecture selects the Linux call convention independently of the
	// host. Empty selects the host platform.
	Architecture  string
	Kind          CallKind
	ABI           Flavor
	ArgumentCount int
	// ArgumentWords supplies each argument's native word count. On 386,
	// nil means one native word per argument. Set two words explicitly for
	// 64-bit arguments on 386; lowering always supplies actual widths.
	ArgumentWords []int
}

// EffectPrecision says how far a CallEffect can be trusted: conservative
// effects assume every caller-saved resource changes, while platform-guaranteed
// effects come from the platform ABI's documented contract.
type EffectPrecision uint8

const (
	EffectConservative EffectPrecision = iota
	EffectPlatformGuaranteed
)

// CallEffect describes a callee's ABI locations and effects before lowering.
type CallEffect struct {
	Architecture         string
	Kind                 CallKind
	ABI                  Flavor
	ArgumentRegisters    []string
	ArgumentLocations    []ValueLocation
	NumberRegister       string
	ResultRegister       string
	ResultLocation       ValueLocation
	ClobberedRegisters   []string
	ClobberedFPRegisters []string
	// ClobberedSpecialRegisters describes callee effects. It does not request
	// implicit saves or restores; preservation is planned during lowering.
	ClobberedSpecialRegisters []string
	FlagsClobbered            bool
	MayReadMemory             bool
	MayWriteMemory            bool
	RequiresCallStack         bool
	StackAlignment            int
	Precision                 EffectPrecision
}
