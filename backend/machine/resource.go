package machine

import "github.com/moriyoshi/nyanjit/backend"

// ResourceClass distinguishes general registers from independently modified
// architectural state. A Resource is an effect identity, not an allocator slot.
type ResourceClass uint8

const (
	ResourceNone ResourceClass = iota
	ResourceGeneralRegister
	ResourceLinkRegister
	ResourceCountRegister
	ResourceConditionField
	ResourceIntegerStatus
	ResourceFloatingStatus
)

// Resource names one target-specific register or status field. Number is the
// ordinal within Class; LR and CTR normally use ordinal zero.
type Resource struct {
	Class  ResourceClass
	Number uint8
}

// ReturnAddress describes where a call leaves its return address. None means
// the architecture places it on the stack rather than in a register resource.
type ReturnAddress struct {
	Resource Resource
}

// InRegister reports whether the return address occupies an architectural
// register, including a special register such as PPC64 LR.
func (address ReturnAddress) InRegister() bool { return address.Resource.Class != ResourceNone }

// GeneralRegister reports the ordinal only when the return-address resource
// aliases an ordinary general register.
func (address ReturnAddress) GeneralRegister() (backend.Register, bool) {
	return backend.Register(address.Resource.Number), address.Resource.Class == ResourceGeneralRegister
}
