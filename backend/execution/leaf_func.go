package execution

import (
	"reflect"
	"runtime"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// FuncLease retains an unregistered executable mapping while its direct leaf
// entries can run. It is released by the garbage collector. Keep the returned
// lease reachable through each invocation with runtime.KeepAlive(lease).
// A lease must not be copied. Code.Close rejects new leases; existing leased
// entries remain valid until their leases are collected.
type FuncLease struct{ code *Code }

type leasedFuncval struct {
	fn    uintptr
	lease *FuncLease
}

// LeafFunc exports a trusted, no-reflection Go-ABI entry and acquires one
// mapping reference for its returned lease. Unlike Func, it does not quarantine
// the mapping permanently. The mapping can be reclaimed after Code.Close and
// collection of all leases. The function value also retains its lease.
//
// Only unmanaged Go-ABI entries with no workspace are accepted. The code must
// be a leaf: no Go callbacks, stack growth or blocking foreign calls. Raw byte
// callers are responsible for that contract, as for their other CodeOptions.
// Function signatures follow Func's integer and exact aggregate-pointer rules.
//
// This is a trusted boundary: pointer arguments must be non-nil and refer to
// live heap objects. There is no nil-check or lifetime wrapper on each call.
// Keep both the lease and every Go argument root reachable through invocation,
// for example f(args); runtime.KeepAlive(args); runtime.KeepAlive(lease). Keeping
// an owner which reaches the lease and arguments alive is equivalent. Neither
// a function value's presence on the execution stack nor raw uintptr arguments
// establish those roots. Do not publish the entry without its lifetime owner.
//
// Destination validation and failures leave fptr unchanged. The returned lease
// owns a reference rather than exposing a manual release which could race calls.
func (c *Code) LeafFunc(fptr any) (*FuncLease, error) {
	target := reflect.ValueOf(fptr)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() ||
		target.Elem().Kind() != reflect.Func || !target.Elem().CanSet() {
		return nil, ErrSignature
	}
	if c == nil {
		return nil, ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entry == 0 || c.state.Load()&codeClosed != 0 {
		return nil, ErrClosed
	}
	switch runtime.GOARCH {
	case "amd64", "arm64", "386", "riscv64", "ppc64le", "loong64":
	default:
		return nil, ErrUnsupported
	}
	if c.options.ABI != abi.Go || c.options.Foreign || c.options.GoManaged ||
		c.options.WorkspaceBytes != 0 || c.options.ResultWords > 1 || len(c.options.GoCallTargets) != 0 {
		return nil, ErrInvalidCodeOptions
	}
	typ := target.Elem().Type()
	if err := checkFuncType(typ, c.options); err != nil {
		return nil, err
	}
	// Close takes this same mutex, so the checked open mapping cannot become
	// closed between validation and acquiring this long-lived call reference.
	c.state.Add(1)
	lease := &FuncLease{code: c}
	runtime.SetFinalizer(lease, func(l *FuncLease) { l.code.endCall() })
	holder := reflect.New(typ)
	*(*unsafe.Pointer)(holder.UnsafePointer()) = unsafe.Pointer(&leasedFuncval{fn: c.entry, lease: lease})
	target.Elem().Set(holder.Elem())
	return lease, nil
}
