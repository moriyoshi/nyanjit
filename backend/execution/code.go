package execution

import (
	"crypto/rand"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// ErrClosed is returned when a closed code object is invoked.
var ErrClosed = errors.New("nyanjit: code is closed")

// ErrArity is returned when Call receives the wrong number of arguments.
var ErrArity = errors.New("nyanjit: argument count does not match function")

// ErrInvalidCodeOptions indicates an inconsistent execution boundary.
var ErrInvalidCodeOptions = errors.New("execution: invalid code options")

// ErrResultWidth means the chosen call method cannot represent the function result.
var ErrResultWidth = errors.New("execution: result width does not match call method")

// CodeOptions describes a mapped function's execution boundary.
type CodeOptions struct {
	// Harden randomizes the aligned entry offset within the owned mapping.
	// Compiler-generated templates propagate compile.Func.Harden here. Raw
	// byte callers remain responsible for constant and index hardening.
	Harden         bool
	Arity          int
	WorkspaceBytes int
	ABI            abi.Flavor
	Foreign        bool
	GoManaged      bool
	// ResultWords is the number of logical 64-bit words returned. Zero means
	// one for callers constructing CodeOptions before this field existed.
	ResultWords int
	// ArgumentWords is the number of native ABI words the entry's parameters
	// occupy. Zero means one word per parameter, which is exact on 64-bit hosts.
	ArgumentWords int
	// ParamBits lists each entry parameter's scalar width, with zero for a
	// vector parameter. Func requires it to check a Go signature.
	ParamBits []uint8
	// ParameterTypes contains exact aggregate entry types; nil entries are raw
	// scalar words. Func checks these types; raw Call/CallPair require callers
	// to supply the matching aggregate types and memory layouts.
	ParameterTypes []reflect.Type
	// StackBytes bounds body and outgoing-call stack usage. Raw-byte callers
	// must supply an accurate bound.
	StackBytes int
	// GoCallTargets describes fixed outbound Go calls. Callable templates reject
	// generated and dynamic targets so per-entry stack bounds also bound chains.
	GoCallTargets []GoCallTarget
	// External marks code that runs outside this process. NewCode and
	// Template.Bind reject it with ErrExternalCallOnly; link it with
	// Template.Bytes or Template.LinkAt instead.
	External bool
}

// ErrExternalCallOnly means the code may only run outside this process, for
// example because it makes system calls or calls unchecked external symbols.
var ErrExternalCallOnly = errors.New("nyanjit: outbound calls require an external execution boundary")

// Code owns a generated function and its executable mapping. Close stops new
// calls through Code; reclamation waits for calls already in progress without
// blocking Close. Func values retain process-lifetime mappings.
// Registered Go-call mappings remain allocated because
// the Go runtime cannot remove their function metadata.
type Code struct {
	mu          sync.Mutex
	mem         []byte
	arena       *Arena     // owns mem when non-nil
	shared      *sharedRun // pages mem shares with other Codes from one BindAll
	entry       uintptr    // immutable after publication; state controls entry admission
	options     CodeOptions
	state       atomic.Uint64
	funcExposed bool
}

// codeClosed shares a word with the in-flight count. Closing and acquiring a
// call therefore have one atomic order: a call either owns a reference before
// Close, or observes the closed bit and cannot enter the mapping.
const codeClosed uint64 = 1 << 63

// The assembly trampoline retains the argument-array address across CALL, so
// a local array would escape on every invocation.
var callArgumentSlots = sync.Pool{New: func() any { return new(ArgumentWords) }}

// FrameRegistrar registers runtime metadata before the entry point is exposed.
// linked reports whether the runtime already retains the mapping if registration
// fails, in which case the mapping cannot be released.
type FrameRegistrar func(mem []byte) (linked bool, err error)

// NewCode maps machine bytes and optionally registers their runtime frame.
// It returns ErrEmptyCode for no bytes; ErrExternalCallOnly for External
// options; ErrInvalidCodeOptions for inconsistent options or a register
// function that does not match GoManaged; backend.ErrBackendLimit (as
// compile.ErrBackendLimit) when StackBytes exceeds the call boundary's stack
// limit (640 bytes on 64-bit hosts, 240 on 386, 4096 for foreign entries);
// backend.ErrInvalidFunction for a GoCallTarget that is not an ordinary Go
// function; ErrUnsupported where native or foreign execution is unsupported;
// and otherwise the error from mapping memory or from register.
func NewCode(code []byte, options CodeOptions, register FrameRegistrar) (*Code, error) {
	return newCode(code, options, register, nil, nil)
}

func newCode(code []byte, options CodeOptions, register FrameRegistrar, relocations []machine.Relocation, arena *Arena) (*Code, error) {
	m, err := prepareCode(code, options, register, relocations)
	if err != nil {
		return nil, err
	}
	var mem []byte
	if arena != nil {
		mem, err = arena.place(m.image, m.relocate)
	} else {
		mem, err = mapCode(m.image, m.relocate)
	}
	if err != nil {
		return nil, err
	}
	if m.register != nil {
		linked, err := m.register(m.body(mem))
		if !linked {
			_ = freeMapping(mem, arena)
		}
		if err != nil {
			return nil, err
		}
		if !linked {
			return nil, ErrInvalidCodeOptions
		}
	}
	return m.code(mem, arena, nil), nil
}

// mapping is a validated mapping image waiting to be placed.
type mapping struct {
	image    []byte // the whole image, trap padding included
	offset   int    // body offset within image
	size     int    // body length
	relocate func([]byte) error
	register FrameRegistrar
	options  CodeOptions
}

// prepareCode validates options and builds the mapping image without
// allocating executable memory.
func prepareCode(code []byte, options CodeOptions, register FrameRegistrar, relocations []machine.Relocation) (mapping, error) {
	if options.External {
		return mapping{}, ErrExternalCallOnly
	}
	for _, target := range options.GoCallTargets {
		if target.Symbol != "" {
			return mapping{}, ErrInvalidCodeOptions
		}
		if err := validateGoCallTarget(target.Address); err != nil {
			return mapping{}, err
		}
	}
	limit := 640
	if strconv.IntSize == 32 {
		limit = 240
	}
	if options.Foreign {
		// Larger foreign frames need guard-page probes. Until emitted, allow
		// at most one minimum-sized stack page on the thread stack.
		limit = 4096
	}
	if options.StackBytes < 0 || options.StackBytes > limit {
		return mapping{}, fmt.Errorf("call frame %d exceeds %d-byte stack limit: %w", options.StackBytes, limit, backend.ErrBackendLimit)
	}
	if options.Arity < 0 || options.Arity > abi.MaxArgumentsFor(runtime.GOARCH) || options.WorkspaceBytes < 0 ||
		options.ArgumentWords < 0 || options.ArgumentWords > MaxArgumentWords ||
		(options.ArgumentWords != 0 && (options.ArgumentWords < options.Arity || options.ArgumentWords > options.Arity*64/strconv.IntSize)) ||
		(options.ParamBits != nil && len(options.ParamBits) != options.Arity) ||
		(options.ParameterTypes != nil && len(options.ParameterTypes) != options.Arity) ||
		(options.ABI != abi.C && options.ABI != abi.Go) ||
		(options.Foreign && options.ABI != abi.C) ||
		(options.Foreign && options.GoManaged) ||
		(options.GoManaged != (register != nil)) || options.ResultWords < 0 || options.ResultWords > 2 ||
		(options.ResultWords == 2 && (options.ABI != abi.C || options.Foreign || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" && runtime.GOARCH != "riscv64" && runtime.GOARCH != "ppc64le" && runtime.GOARCH != "loong64")) {
		return mapping{}, ErrInvalidCodeOptions
	}
	if options.Foreign && !foreignSupported() {
		return mapping{}, ErrUnsupported
	}
	image, offset, err := mappingBody(code, options.Harden, register != nil, rand.Reader)
	if err != nil {
		return mapping{}, err
	}
	var relocate func([]byte) error
	if len(relocations) != 0 {
		relocate = func(mem []byte) error {
			body := mem[offset : offset+len(code)]
			linked, err := machine.LinkRelocationsAt(body, relocations, nil, uint64(Entry(body)))
			if err != nil {
				return err
			}
			copy(body, linked)
			return nil
		}
	}
	options.ParamBits = append([]uint8(nil), options.ParamBits...)
	options.ParameterTypes = append([]reflect.Type(nil), options.ParameterTypes...)
	options.GoCallTargets = append([]GoCallTarget(nil), options.GoCallTargets...)
	return mapping{image: image, offset: offset, size: len(code), relocate: relocate, register: register, options: options}, nil
}

// body returns the function body within mem, a placed copy of the image.
func (m *mapping) body(mem []byte) []byte { return mem[m.offset : m.offset+m.size] }

// code returns the Code owning mem, a placed copy of the image.
func (m *mapping) code(mem []byte, arena *Arena, shared *sharedRun) *Code {
	return &Code{mem: mem, arena: arena, shared: shared, entry: Entry(mem[m.offset:]), options: m.options}
}

func freeMapping(mem []byte, arena *Arena) error {
	if arena != nil {
		return arena.release(mem)
	}
	return Unmap(mem)
}

// EntryAddress returns the executable entry point for trusted raw calls. It
// cannot be used as a callable CallGo target. Its owner must keep this
// Code open for the generated caller's entire lifetime. It returns ErrClosed
// after Close.
func (c *Code) EntryAddress() (uintptr, error) {
	if c == nil {
		return 0, ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entry == 0 || c.state.Load()&codeClosed != 0 {
		return 0, ErrClosed
	}
	return c.entry, nil
}

// argumentWords is the native word count Call expects.
func (c *Code) argumentWords() int {
	if c.options.ArgumentWords != 0 || c.options.Arity == 0 {
		return c.options.ArgumentWords
	}
	return c.options.Arity
}

// Call invokes any entry kind with raw native ABI argument words and returns
// its first result word. Use c.Call(Words(values...)...) to marshal logical
// arguments and keep pointer/aggregate roots alive through callbacks and stack
// growth. Pass the original Words slice: copying its words drops ownership.
// On 32-bit hosts a 64-bit result needs CallPair.
//
// Call returns ErrClosed after Close, ErrArity when len(args) is not the
// entry's argument word count, ErrResultWidth for a two-word result, and the
// error from Scratch when the entry's workspace cannot be allocated.
//
// Call does not check parameter Go types or pointer validity. Matching the
// compiled signature, aggregate layouts and memory lifetimes is the caller's
// responsibility. Hand-built uintptr addresses must refer to live memory;
// stack addresses must not reach generated code that can call back into Go.
// The uintptrescapes directive applies to direct calls, not interface method
// signatures. Use Words for pointer arguments through either kind of call.
//
//go:uintptrescapes
func (c *Code) Call(args ...uintptr) (uintptr, error) {
	lo, _, err := c.call(false, args)
	return lo, err
}

// CallPair invokes the generated function and returns both result words, low
// first. It serves C ABI functions returning two words and, on 32-bit hosts,
// any result, whose 64-bit values arrive in EAX:EDX or the Go result slot.
// On 64-bit hosts a one-word result is returned with hi zero; Wide joins the
// words portably. Like Call, this is a raw boundary for every entry kind.
// Use c.CallPair(Words(values...)...) and supply the compiled parameter types.
// Its errors are Call's, except that a two-word result is accepted for C ABI
// entries.
//
//go:uintptrescapes
func (c *Code) CallPair(args ...uintptr) (uintptr, uintptr, error) {
	return c.call(true, args)
}

func (c *Code) call(pair bool, args []uintptr) (uintptr, uintptr, error) {
	if c == nil || c.entry == 0 {
		return 0, 0, ErrClosed
	}
	if !c.beginCall() {
		return 0, 0, ErrClosed
	}
	entry := c.entry
	defer c.endCall()
	if len(args) != c.argumentWords() {
		return 0, 0, ErrArity
	}
	two := c.options.ResultWords == 2
	if two && (!pair || c.options.ABI != abi.C) {
		return 0, 0, ErrResultWidth
	}
	if c.options.Foreign {
		lo, hi := CallForeignPair(entry, args)
		runtime.KeepAlive(args)
		if !pair || strconv.IntSize == 64 {
			hi = 0
		}
		return lo, hi, nil
	}
	var workspace []byte
	if c.options.WorkspaceBytes != 0 {
		var err error
		workspace, err = Scratch(c.options.WorkspaceBytes)
		if err != nil {
			return 0, 0, err
		}
		defer FreeScratch(workspace)
	}
	var workspaceAddress uintptr
	if len(workspace) != 0 {
		workspaceAddress = uintptr(unsafe.Pointer(&workspace[0]))
	}
	slots := callArgumentSlots.Get().(*ArgumentWords)
	copy(slots[:], args)
	var lo, hi uintptr
	switch {
	case c.options.GoManaged:
		lo, hi = callManagedEntry(two, c.options.ABI, entry, slots, workspaceAddress, len(args))
	case two:
		lo, hi = callPair(entry, slots, workspaceAddress)
	default:
		lo, hi = callWithABIArity(c.options.ABI, entry, slots, workspaceAddress, len(args))
	}
	clear(slots[:])
	callArgumentSlots.Put(slots)
	// A Words result owns the Go memory its words address.
	runtime.KeepAlive(args)
	return lo, hi, nil
}

// Close releases unregistered executable mappings. Registered Go-call code
// and code exposed through Func remain mapped for the process lifetime. A call
// already in progress keeps its mapping until it returns. Close never waits
// for callbacks and is safe to call more than once.
func (c *Code) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Or(codeClosed)
	return c.releaseMapping()
}

func (c *Code) releaseMapping() error {
	if c.state.Load() != codeClosed || c.options.GoManaged || c.funcExposed || c.mem == nil {
		return nil
	}
	var err error
	if c.shared != nil {
		err = c.arena.releaseShared(c.shared, c.mem)
	} else {
		err = freeMapping(c.mem, c.arena)
	}
	if err != nil && c.arena == nil {
		return err // a later Close retries the unmap
	}
	// Arena pages are accounted for even when they could not be decommitted.
	c.mem = nil
	return err
}

// beginCall acquires the mapping without taking the close/release mutex.
func (c *Code) beginCall() bool {
	if c.state.Add(1)&codeClosed == 0 {
		return true
	}
	// An increment ordered after Close never grants entry. It may arrive
	// after reclamation; undoing that reference only touches Code itself.
	c.endCall()
	return false
}

func (c *Code) endCall() {
	if c.state.Add(^uint64(0)) == codeClosed {
		// Only the last call after Close needs the release lock. Close either
		// sees our zero count itself or leaves reclamation to this call.
		c.mu.Lock()
		_ = c.releaseMapping()
		c.mu.Unlock()
	}
}
