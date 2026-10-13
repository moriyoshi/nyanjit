package compile

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// TargetSpec says what code a Target generates.
type TargetSpec struct {
	// Architecture selects the Linux target: amd64, arm64, 386, riscv64,
	// loong64 or ppc64le. Empty selects the host. Callable templates require
	// the host architecture; other targets give reports and listings.
	Architecture string
	// CPU selects an ISA level and extensions. Zero selects the detected host
	// CPU for the host architecture, or the target's baseline otherwise.
	CPU abi.CPUProfile
	// Vectors selects how portable SIMD operations are lowered.
	Vectors VectorPolicy
	// Tier selects how much work lowering does. The zero value is
	// TierOptimized.
	Tier Tier
}

// Tier selects how much work lowering does.
type Tier uint8

const (
	// TierOptimized is the optimizing tier: SSA IR, selection, Var
	// promotion and whole-function register allocation. It is the zero
	// value.
	TierOptimized Tier = 0
	// TierBaseline compiles without building SSA. It records each
	// function in a compact form, computes liveness once and allocates and
	// encodes in one forward pass. It compiles about ten times faster than
	// TierOptimized, with slower and larger code. NewTarget returns an error
	// wrapping ErrUnsupported for an architecture without a baseline tier.
	TierBaseline Tier = 1
)

type vectorMode uint8

const (
	vectorsAuto vectorMode = iota
	vectorsOff
	vectorsRequire
)

// VectorPolicy selects how portable SIMD operations are lowered. Its zero
// value is VectorsAuto.
type VectorPolicy struct {
	mode       vectorMode
	extensions string // comma-separated, in request order
}

// VectorsAuto uses the SIMD extensions the target CPU supports.
func VectorsAuto() VectorPolicy { return VectorPolicy{} }

// VectorsOff lowers every vector operation to scalar lanes.
func VectorsOff() VectorPolicy { return VectorPolicy{mode: vectorsOff} }

// VectorsRequire uses exactly the named extensions and makes them part of the
// CPU contract: amd64/386 "sse2" and "pclmulqdq" (which implies "sse2"), arm64
// "neon", and loong64 "lsx" and "lasx" (which implies "lsx"; lowering still
// uses 128-bit LSX). NewTarget rejects an extension the target cannot use or
// that an explicit CPU profile lacks. With no extensions it equals VectorsOff.
func VectorsRequire(extensions ...string) VectorPolicy {
	if len(extensions) == 0 {
		return VectorsOff()
	}
	return VectorPolicy{mode: vectorsRequire, extensions: strings.Join(extensions, ",")}
}

// features is the internal feature list: nil detects, empty forces scalar.
func (p VectorPolicy) features() []string {
	switch p.mode {
	case vectorsOff:
		return []string{}
	case vectorsRequire:
		return strings.Split(p.extensions, ",")
	}
	return nil
}

// String describes the policy, e.g. "auto", "off" or "require(neon)".
func (p VectorPolicy) String() string {
	switch p.mode {
	case vectorsOff:
		return "off"
	case vectorsRequire:
		return "require(" + p.extensions + ")"
	}
	return "auto"
}

// Target is a resolved code generation target. It holds what every lowering
// for the target shares: the resolved CPU profile and vector policy, and a
// pool of reusable compilation buffers.
//
// A Target is safe for concurrent use; share one per target. Its state only
// saves work: the same program and Func give identical output from a fresh
// Target, a warm one, or one under concurrent load. Only this package
// implements Target.
type Target interface {
	// Spec returns the specification the target was created from.
	Spec() TargetSpec
	// Architecture returns the resolved architecture name.
	Architecture() string
	// Vectors describes the selected SIMD baseline and estimates how each
	// vector operation is lowered.
	Vectors() VectorInfo
	resolved() *resolvedTarget
}

type resolvedTarget struct {
	spec         TargetSpec
	architecture string
	features     []string
	cpu          abi.ResolvedCPU
	cpuResolved  bool // cpu is valid; set once NewTarget resolved it
	// native is set when this host can run the target's code; other targets
	// lower with listing layout and their templates report why not.
	native      bool
	scratch     *sync.Pool
	vectorsOnce sync.Once
	vectors     VectorInfo
	// keepFlagWrites skips dead-flag elision. Only tests set it, to compare
	// against the flag-writing forms.
	keepFlagWrites bool
}

var targetCache sync.Map // targetKey(spec) -> *resolvedTarget

// NewTarget resolves spec. Equal specs return the same shared Target, so
// repeated calls cost a lookup. Errors match ErrUnsupported for an unknown
// architecture, ErrBackendLimit for an unusable vector extension,
// ErrCPUUnsupported for an extension beyond an explicit CPU profile, and
// ErrInvalidFunction with abi.ErrInvalidSpec for an invalid CPU profile or
// an unknown Tier, and ErrUnsupported for TierBaseline on an architecture
// without a baseline tier. Whether the host can run the code is checked when a
// template is encoded.
func NewTarget(spec TargetSpec) (Target, error) {
	for i := range recentTargets {
		if t := recentTargets[i].Load(); t != nil && t.spec.equal(spec) {
			return t, nil
		}
	}
	key := targetKey(spec)
	if cached, ok := targetCache.Load(key); ok {
		t := cached.(*resolvedTarget)
		rememberTarget(t)
		return t, nil
	}
	t, err := resolveTarget(spec)
	if err != nil {
		return nil, err
	}
	actual, _ := targetCache.LoadOrStore(key, t)
	rememberTarget(actual.(*resolvedTarget))
	return actual.(*resolvedTarget), nil
}

// resolveTarget resolves spec without consulting or filling the cache.
func resolveTarget(spec TargetSpec) (*resolvedTarget, error) {
	if spec.Tier != TierOptimized && spec.Tier != TierBaseline {
		return nil, fmt.Errorf("unknown tier %d: %w: %w", spec.Tier, abi.ErrInvalidSpec, ErrInvalidFunction)
	}
	spec.CPU = spec.CPU.Clone()
	t := &resolvedTarget{spec: spec, features: spec.Vectors.features()}
	options := compileOptions{Architecture: spec.Architecture, CPU: spec.CPU, Features: t.features}
	t.architecture = targetArchitecture(options)
	if spec.Tier == TierBaseline && baselineArchFor(t.architecture) == nil {
		return nil, fmt.Errorf("no baseline tier for %s: %w", t.architecture, ErrUnsupported)
	}
	if err := validateVectorFeatures(options); err != nil {
		return nil, err
	}
	cpu, err := emissionCPU(options, emitText)
	if err != nil {
		return nil, err
	}
	t.cpu, t.cpuResolved = cpu, true
	t.native = validateEmissionTarget(options, emitLayoutNative) == nil
	t.scratch = newScratchPool()
	return t, nil
}

// recentTargets answers repeated NewTarget calls for the same few specs
// without building a cache key.
var (
	recentTargets [8]atomic.Pointer[resolvedTarget]
	recentNext    atomic.Uint32
)

func rememberTarget(t *resolvedTarget) {
	recentTargets[recentNext.Add(1)%uint32(len(recentTargets))].Store(t)
}

func (s TargetSpec) equal(o TargetSpec) bool {
	return s.Architecture == o.Architecture && s.Vectors == o.Vectors && s.Tier == o.Tier &&
		s.CPU.Level == o.CPU.Level && slices.Equal(s.CPU.Extensions, o.CPU.Extensions)
}

func targetKey(spec TargetSpec) string {
	extensions := slices.Clone(spec.CPU.Extensions)
	return fmt.Sprintf("%s|%s|%q|%d|%s|%d", spec.Architecture, spec.CPU.Level, extensions, spec.Vectors.mode, spec.Vectors.extensions, spec.Tier)
}

func (t *resolvedTarget) Spec() TargetSpec {
	spec := t.spec
	spec.CPU = spec.CPU.Clone()
	return spec
}

func (t *resolvedTarget) Architecture() string      { return t.architecture }
func (t *resolvedTarget) resolved() *resolvedTarget { return t }

func (t *resolvedTarget) Vectors() VectorInfo {
	t.vectorsOnce.Do(func() { t.vectors = vectorInfo(t.options(Func{})) })
	info := t.vectors
	info.Features = slices.Clone(info.Features)
	return info
}

// resolvedFor reports whether options still carry this target's own
// architecture, CPU profile and vector features, so t.cpu is their
// resolution. Lowering replaces CPU with a report's copy, which resolves anew.
func (t *resolvedTarget) resolvedFor(options compileOptions) bool {
	return t.cpuResolved && options.Architecture == t.spec.Architecture && options.CPU.Level == t.spec.CPU.Level &&
		sameSlice(options.CPU.Extensions, t.spec.CPU.Extensions) && sameSlice(options.Features, t.features)
}

// sameSlice reports whether a and b are the same slice, including nil-ness.
func sameSlice(a, b []string) bool {
	if len(a) != len(b) || (a == nil) != (b == nil) {
		return false
	}
	return len(a) == 0 || &a[0] == &b[0]
}

// keepFlagWrites reports whether lowering skips dead-flag elision.
func (o compileOptions) keepFlagWrites() bool { return o.target != nil && o.target.keepFlagWrites }

// options combines the target with one function's settings for the lowering
// pipeline.
func (t *resolvedTarget) options(f Func) compileOptions {
	return compileOptions{
		Architecture: t.spec.Architecture, CPU: t.spec.CPU, Features: t.features, target: t,
		ABI: f.ABI, ManagedCalls: f.Context == ContextGoManaged,
		Stack: f.Stack.Practice, StackPointer: f.Stack.Pointer, StackBytesAvailable: f.Stack.BytesAvailable,
		AssumePointerFree: f.AssumePointerFree, PreferCompactLoops: f.PreferCompactLoops, Harden: f.Harden,
	}
}

// compileOptions is the lowering pipeline's view of one Target and Func.
type compileOptions struct {
	Architecture        string
	CPU                 abi.CPUProfile
	Features            []string // nil detects, empty forces scalar lanes
	ABI                 abi.Flavor
	ManagedCalls        bool
	Stack               abi.StackPractice
	StackPointer        abi.StackPointerRole
	StackBytesAvailable int
	AssumePointerFree   bool
	PreferCompactLoops  bool
	Harden              bool
	// withholdPreserved allocates only the target's volatile registers. A
	// lowering whose preserved-register saves outgrow a small frame budget
	// retries with it.
	withholdPreserved bool
	// target is the Target these options came from, when there is one. Its
	// resolved CPU profile and scratch pool are shared by every lowering.
	target *resolvedTarget
}

// Func describes how one function is entered and what it may do.
type Func struct {
	// ABI is the entry calling convention, abi.C or abi.Go.
	ABI abi.Flavor
	// Context says how the function is entered and what it may call.
	Context Context
	// Stack says where spills and outbound calls may use the stack.
	Stack StackPolicy
	// AssumePointerFree explicitly permits Go frame registration with empty
	// pointer maps. Any address used by generated code must be static, outside
	// Go-managed memory, or independently pinned for its entire live range.
	AssumePointerFree bool
	// PreferCompactLoops permits amd64 LOOP and PPC64 CTR countdown loops
	// when register/flag liveness permits them. PPC64 uses a dedicated CTR;
	// calls or other CTR consumers prevent loop fusion.
	PreferCompactLoops bool
	// Harden enables constant blinding, random executable-entry placement, and
	// checked-index speculation protection. See
	// docs/optional-hardening-design.md for target support and limitations.
	Harden bool
	// Constants substitutes compile-time values for symbols before lowering,
	// so the generated code can specialise on them. When it is non-empty it
	// must bind every symbol the program uses (other than built-in math
	// constants), and every entry must name a used symbol. Leave it empty to
	// bind symbols when a template is bound or linked instead.
	Constants map[string]uint64
}

// StackPolicy says where a function may keep spills and call frames.
type StackPolicy struct {
	// Practice chooses stack use. abi.StackNoUse leaves the stack pointer
	// unchanged and spills to the workspace Code.Call supplies;
	// abi.StackPlatform permits a bounded frame. Outbound calls and system
	// calls require abi.StackPlatform.
	Practice abi.StackPractice
	// Pointer is the stack pointer's role; only abi.StackPointerReserved is
	// supported.
	Pointer abi.StackPointerRole
	// BytesAvailable bounds the frame under abi.StackPlatform.
	BytesAvailable int
}

// Context says how a generated function is entered and what it may call.
type Context uint8

const (
	// ContextNative functions are entered from anywhere through Code.Call
	// and make no outbound calls or system calls.
	ContextNative Context = iota
	// ContextForeign functions are C ABI entries running in a foreign
	// (non-Go-managed) thread context. They may call C functions.
	ContextForeign
	// ContextGoManaged functions run in a Go-managed call chain: a Go call,
	// or a C entry reached through a Go runtime callback. They call Go
	// functions directly and C functions through the runtime gateway. Their
	// frame is registered with the Go runtime when bound, and generated
	// values must be pointer-free (AssumePointerFree).
	ContextGoManaged
	// ContextExternal code runs outside this process, for example written to
	// an executable file. It may make system calls and C calls to symbols
	// resolved when linking. Its template links with Bytes or LinkAt; Bind
	// returns ErrExternalCallOnly.
	ContextExternal
)

func (c Context) boundary() callableBoundary {
	switch c {
	case ContextForeign:
		return boundaryForeign
	case ContextGoManaged:
		return boundaryManaged
	case ContextExternal:
		return boundaryExternal
	}
	return boundaryLeaf
}
