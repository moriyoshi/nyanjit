// Package abi defines calling conventions, stack policy, and call effects.
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

// CompileOptions selects the generated function's entry ABI, its execution
// context, and its stack policy. Each outbound call chooses its own ABI.
// With StackNoUse, spills use a caller-provided workspace. A zero
// StackBytesAvailable forbids frame use.
type CompileOptions struct {
	// Architecture selects the Linux target for assembly emission: amd64,
	// arm64, 386, riscv64, ppc64, or ppc64le. Empty selects the host platform.
	// Native byte emission and callable compilation require the host target.
	// NativeBits and ParamNative still refer to the Builder's host word width.
	Architecture string
	// CPU selects an ISA level and extensions; zero selects native detection or a deterministic cross-target baseline.
	CPU CPUProfile
	// AMD64FMA enables FMA in an amd64 profile. False leaves CPU unchanged.
	// Callable compilation still checks the executing CPU's capabilities.
	//
	// Deprecated: use CPU.Extensions with "fma" instead.
	AMD64FMA bool
	// Features nil detects native SIMD; an empty non-nil slice forces scalar lanes.
	Features []string
	ABI      Flavor
	// PPC64CABI selects the C entry ELF convention; unspecified uses the
	// toolchain default. Outbound C calls select their convention independently.
	PPC64CABI PPC64CABI
	// PPC64CPU is a compatibility option for existing POWER callers. Prefer CPU.
	// Text emission permits an explicit cross-target level. Native emission
	// rejects a level newer than the executing kernel's CPU capabilities.
	PPC64CPU PPC64CPU
	// ManagedCalls means the function executes in a Go runtime managed call
	// chain. PrepareGo/CompileGo set it and register the generated frame.
	// With this context, C calls use a Go-to-C gateway. A C ABI entry must be
	// reached from a Go-managed call chain, not an arbitrary C thread.
	ManagedCalls        bool
	Stack               StackPractice
	StackPointer        StackPointerRole
	StackBytesAvailable int
	// AssumePointerFree explicitly permits Go frame registration with empty
	// pointer maps. Any address used by generated code must be static, outside
	// Go-managed memory, or independently pinned for its entire live range.
	AssumePointerFree bool
	// PreferCompactLoops permits amd64 LOOP and PPC64 CTR countdown loops
	// when register/flag liveness permits them. PPC64 uses a dedicated CTR;
	// calls or other CTR consumers prevent loop fusion.
	PreferCompactLoops bool
}

// SpillStorage reports where evicted virtual values are kept.
type SpillStorage uint8

const (
	SpillNone SpillStorage = iota
	SpillWorkspace
	SpillStack
)

type Storage uint8

const (
	Register Storage = iota
	Stack
)

type WordLocation struct {
	Storage     Storage
	Register    string
	StackOffset int
}

type ValueLocation struct{ Words []WordLocation }

// EmissionReport describes resources and effects after allocation.
// ClobberedRegisters includes registers overwritten by the generated body,
// excluding trampoline writes. Spill scheduling is the allocator's job.
type EmissionReport struct {
	CPU              CPUProfile
	VectorOperations []VectorOperation
	// AssumedVectorBytes records a scalable vector length; phase 1 leaves it zero.
	AssumedVectorBytes int
	Architecture       string
	ABI                Flavor
	PPC64CABI          PPC64CABI
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
}

type CallKind uint8

const (
	FunctionCall CallKind = iota
	SystemCall
)

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
	// PPC64CABI selects a PPC64 C callee pointer representation. It must be
	// unspecified for Go calls and on other architectures.
	PPC64CABI PPC64CABI
}

type EffectPrecision uint8

const (
	EffectConservative EffectPrecision = iota
	EffectPlatformGuaranteed
)

// CallEffect describes a callee's ABI locations and effects before lowering.
type CallEffect struct {
	Architecture string
	Kind         CallKind
	ABI          Flavor
	// PPC64CABI identifies the resolved platform variant for a PPC64 C call.
	PPC64CABI PPC64CABI
	// FunctionPointer specifies whether the target is code or a descriptor.
	FunctionPointer      FunctionPointerKind
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
