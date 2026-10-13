package execution

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"runtime"
	"strings"
)

// GoCallTarget is a fixed CallGo destination. Symbol is resolved at Bind; an
// empty Symbol uses Address. Hosts must supply ordinary splittable Go functions,
// never NOSPLIT forwarders into generated code. Generated entries and interior PCs are forbidden.
// Raw-code constructors must describe their outbound Go calls accurately.
type GoCallTarget struct {
	Address uint64
	Symbol  string
}

func validateGoCallTarget(address uint64) error {
	pc := uintptr(address)
	f := runtime.FuncForPC(pc)
	if uint64(pc) != address || pc == 0 || f == nil || f.Entry() != pc || goregister.IsRegisteredPC(pc) || goCallTrampoline(f.Name()) {
		return fmt.Errorf("CallGo target %#x must be an ordinary Go function entry; generated calls are forbidden: %w", address, backend.ErrInvalidFunction)
	}
	return nil
}

// Entry identity cannot prove that arbitrary host code is splittable. Reject
// known ABI/runtime bridges, and leave ordinary host targets under the explicit
// splittable-function contract. Names here come from runtime function metadata,
// not caller-provided symbols or generated frame names.
func goCallTrampoline(name string) bool {
	for _, prefix := range []string{
		"runtime.morestack", "runtime.asmcgocall", "runtime.cgocallback", "runtime.systemstack", "runtime.reflectcall",
		"reflect.makeFuncStub", "reflect.methodValueCall", "callbackasm", "github.com/ebitengine/purego.",
		"github.com/moriyoshi/nyanjit/backend/execution.call",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	// reflect's fixed-size assembly call helpers also invoke arbitrary code
	// without adding a Go stack-growth boundary.
	if suffix, ok := strings.CutPrefix(name, "runtime.call"); ok {
		suffix = strings.TrimSuffix(suffix, ".abi0")
		if suffix == "Ret" {
			return true
		}
		if len(suffix) > 0 && suffix[0] >= '0' && suffix[0] <= '9' {
			return true
		}
	}
	return name == "runtime.mcall" || name == "runtime.gogo"
}
