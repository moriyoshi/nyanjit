package execution

import (
	"errors"
	"runtime"
	"strconv"
	"sync"
	"unsafe"

	"github.com/moriyoshi/nyanjit/abi"
)

// ErrClosed is returned when a closed code object is invoked.
var ErrClosed = errors.New("nyanjit: code is closed")

// ErrArity is returned when Call receives the wrong number of arguments.
var ErrArity = errors.New("nyanjit: argument count does not match function")

// ErrInvalidCodeOptions indicates an inconsistent execution boundary.
var ErrInvalidCodeOptions = errors.New("execution: invalid code options")

// ErrEmptyOutput means CallInto received no addressable output bytes.
var ErrEmptyOutput = errors.New("execution: output buffer is empty")

// ErrResultWidth means the chosen call method cannot represent the function result.
var ErrResultWidth = errors.New("execution: result width does not match call method")

// CodeOptions describes a mapped function's execution boundary.
type CodeOptions struct {
	Arity          int
	WorkspaceBytes int
	ABI            abi.Flavor
	PPC64CABI      abi.PPC64CABI
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
}

// Code owns a generated function and its executable mapping. Close waits for
// an in-progress Call. Registered Go-call mappings remain allocated because
// the Go runtime cannot remove their function metadata.
type Code struct {
	mu      sync.RWMutex
	mem     []byte
	entry   uintptr
	options CodeOptions
}

// The assembly trampoline retains the argument-array address across CALL, so
// a local array would escape on every invocation.
var callArgumentSlots = sync.Pool{New: func() any { return new(ArgumentWords) }}

// FrameRegistrar registers runtime metadata before the entry point is exposed.
// linked reports whether the runtime already retains the mapping if registration
// fails, in which case the mapping cannot be released.
type FrameRegistrar func(mem []byte) (linked bool, err error)

// NewCode maps machine bytes and optionally registers their runtime frame.
func NewCode(code []byte, options CodeOptions, register FrameRegistrar) (*Code, error) {
	if options.PPC64CABI != abi.PPC64CABIUnspecified {
		if options.ABI != abi.C {
			return nil, ErrInvalidCodeOptions
		}
		if _, err := abi.NewPPC64Profile(runtime.GOARCH, options.ABI, options.PPC64CABI); err != nil {
			return nil, ErrInvalidCodeOptions
		}
	}
	if options.Arity < 0 || options.Arity > 8 || options.WorkspaceBytes < 0 ||
		options.ArgumentWords < 0 || options.ArgumentWords > MaxArgumentWords ||
		(options.ArgumentWords != 0 && (options.ArgumentWords < options.Arity || options.ArgumentWords > options.Arity*64/strconv.IntSize)) ||
		(options.ParamBits != nil && len(options.ParamBits) != options.Arity) ||
		(options.ABI != abi.C && options.ABI != abi.Go) ||
		(options.Foreign && options.ABI != abi.C) ||
		(options.Foreign && options.GoManaged) ||
		(options.GoManaged != (register != nil)) || options.ResultWords < 0 || options.ResultWords > 2 ||
		(options.ResultWords == 2 && (options.ABI != abi.C || options.Foreign || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" && runtime.GOARCH != "riscv64" && runtime.GOARCH != "ppc64" && runtime.GOARCH != "ppc64le")) {
		return nil, ErrInvalidCodeOptions
	}
	if options.Foreign && !foreignSupported() {
		return nil, ErrUnsupported
	}
	mappingCode := code
	if register != nil && len(code) != 0 {
		// The runtime's plugin registrar compares text endpoints inclusively.
		// Reserve a byte beyond the registered text so page-sized functions
		// cannot acquire adjacent text ranges. The byte is not registered code.
		mappingCode = make([]byte, len(code)+1)
		copy(mappingCode, code)
	}
	mem, err := Map(mappingCode)
	if err != nil {
		return nil, err
	}
	if register != nil {
		linked, err := register(mem[:len(code)])
		if err != nil {
			if !linked {
				_ = Unmap(mem)
			}
			return nil, err
		}
		if !linked {
			_ = Unmap(mem)
			return nil, ErrInvalidCodeOptions
		}
	}
	options.ParamBits = append([]uint8(nil), options.ParamBits...)
	return &Code{mem: mem, entry: Entry(mem), options: options}, nil
}

// EntryAddress returns the executable entry point. Its owner must keep this
// Code open for the generated caller's entire lifetime. On PPC64 this is a raw
// code address; an ELFv1 C callable pointer needs a separate descriptor.
func (c *Code) EntryAddress() (uintptr, error) {
	if c == nil {
		return 0, ErrClosed
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.entry == 0 {
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

// Call invokes the generated function with native ABI argument words and
// returns its first result word. Use Words to build portable arguments. On
// 32-bit hosts a 64-bit result needs CallPair.
func (c *Code) Call(args ...uintptr) (uintptr, error) {
	lo, _, err := c.call(false, args)
	return lo, err
}

// CallPair invokes the generated function and returns both result words, low
// first. It serves C ABI functions returning two words and, on 32-bit hosts,
// any result, whose 64-bit values arrive in EAX:EDX or the Go result slot.
// On 64-bit hosts a one-word result is returned with hi zero; Wide joins the
// words portably.
func (c *Code) CallPair(args ...uintptr) (uintptr, uintptr, error) {
	return c.call(true, args)
}

func (c *Code) call(pair bool, args []uintptr) (uintptr, uintptr, error) {
	if c == nil {
		return 0, 0, ErrClosed
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.entry == 0 {
		return 0, 0, ErrClosed
	}
	if len(args) != c.argumentWords() {
		return 0, 0, ErrArity
	}
	two := c.options.ResultWords == 2
	if two && (!pair || c.options.ABI != abi.C) {
		return 0, 0, ErrResultWidth
	}
	if c.options.Foreign {
		lo, hi := callForeignGenerated(c.entry, args, c.options.PPC64CABI)
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
	if two {
		lo, hi = callPair(c.entry, slots, workspaceAddress)
	} else {
		lo, hi = callWithABIArity(c.options.ABI, c.entry, slots, workspaceAddress, len(args))
	}
	clear(slots[:])
	callArgumentSlots.Put(slots)
	return lo, hi, nil
}

// CallInto passes a pinned output buffer's address as the first native ABI
// word, followed by args. The generated function may write to the buffer
// only for the duration of this call. Its first result word is returned.
// This entry point supports C ABI code; registered Go frames remain subject
// to their pointer-free stack-map contract.
func (c *Code) CallInto(output []byte, args ...uintptr) (uintptr, error) {
	if c == nil {
		return 0, ErrClosed
	}
	if c.options.ABI != abi.C {
		return 0, ErrInvalidCodeOptions
	}
	if len(output) == 0 {
		return 0, ErrEmptyOutput
	}
	var pinner runtime.Pinner
	pinner.Pin(&output[0])
	defer pinner.Unpin()
	callArgs := make([]uintptr, 1, len(args)+1)
	callArgs[0] = uintptr(unsafe.Pointer(&output[0]))
	callArgs = append(callArgs, args...)
	result, err := c.Call(callArgs...)
	return result, err
}

// Close releases unregistered executable mappings. Registered Go-call code
// remains mapped for the process lifetime. It is safe to call more than once.
func (c *Code) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entry == 0 {
		return nil
	}
	if !c.options.GoManaged {
		if err := Unmap(c.mem); err != nil {
			return err
		}
	}
	c.mem = nil
	c.entry = 0
	return nil
}
