package compile

import (
	"reflect"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// emissionTemplate is a unit's encoded body: bytes, relocations, the report
// and the Go unwind table for managed frames. Callers must not mutate it.
type emissionTemplate struct {
	Bytes       []byte
	Report      abi.EmissionReport
	Relocations []machine.Relocation
	goUnwind    goUnwindInfo
}

func newExecutableTemplate(emission emissionTemplate, report abi.Report, arity int, paramBits []uint8, parameterTypes []reflect.Type, flavor abi.Flavor, foreign, goManaged, external bool, targets []execution.GoCallTarget, harden bool) *execution.Template {
	info := emission.goUnwind
	words := entryArgumentWords(emission.Report, arity)
	argBytes := 0
	if emission.Report.Architecture == "386" && flavor == abi.Go {
		argBytes = 4*words + 8 // Arguments plus the two-word result slot.
	}
	return execution.NewTemplateWithResolver(emission.Bytes, report, emission.Relocations, execution.CodeOptions{
		Harden: harden, GoCallTargets: targets, Arity: arity, WorkspaceBytes: emission.Report.WorkspaceBytes,
		ABI: flavor, Foreign: foreign, GoManaged: goManaged, External: external,
		ResultWords: emission.Report.ResultWords, ArgumentWords: words,
		ParamBits: paramBits, ParameterTypes: parameterTypes, StackBytes: emission.Report.StackBytes + emission.Report.CallStackBytes,
	}, goregister.Frame{
		Name: "nyanjit.generated", CodeBytes: info.CodeBytes,
		FrameBytes: info.FrameBytes, ArgBytes: argBytes, SP: info.SP, PointerFree: goManaged,
	}, resolveMathBindings)
}

// entryArgumentWords counts the native words of the entry parameters. Only
// 386 splits a parameter across words; the reported layout is authoritative.
// A missing layout yields -1, which NewCode rejects at Bind.
func entryArgumentWords(report abi.EmissionReport, arity int) int {
	if report.Architecture != "386" {
		return arity
	}
	if len(report.ArgumentLocations) != arity {
		return -1
	}
	words := 0
	for _, location := range report.ArgumentLocations {
		words += len(location.Words)
	}
	return words
}
