package compile

import (
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/goregister"
	"github.com/moriyoshi/nyanjit/machine"
)

type Code = execution.Code
type CodeTemplate = execution.Template

// EmissionTemplate is the relocatable output of native lowering.
type EmissionTemplate struct {
	Bytes       []byte
	Report      EmissionReport
	Relocations []Relocation
	goUnwind    goUnwindInfo
}

// Link copies and patches every symbol load in the emitted bytes.
func (t EmissionTemplate) Link(bindings map[string]uint64) ([]byte, error) {
	var err error
	bindings, err = resolveMathBindings(t.Relocations, bindings)
	if err != nil {
		return nil, err
	}
	return machine.LinkRelocations(t.Bytes, t.Relocations, bindings)
}

func newCodeTemplate(emission EmissionTemplate, arity int, paramBits []uint8, flavor ABIFlavor, foreign, goManaged bool) *CodeTemplate {
	info := emission.goUnwind
	words := entryArgumentWords(emission.Report, arity)
	argBytes := 0
	if emission.Report.Architecture == "386" && flavor == ABIGo {
		argBytes = 4*words + 8 // Arguments plus the two-word result slot.
	}
	return execution.NewTemplateWithResolver(emission.Bytes, emission.Report, emission.Relocations,
		execution.CodeOptions{
			Arity: arity, WorkspaceBytes: emission.Report.WorkspaceBytes,
			ABI: flavor, PPC64CABI: emission.Report.PPC64CABI, Foreign: foreign, GoManaged: goManaged,
			ResultWords: emission.Report.ResultWords, ArgumentWords: words,
			ParamBits: append([]uint8(nil), paramBits...),
		}, goregister.Frame{
			Name: "nyanjit.generated", CodeBytes: info.CodeBytes,
			FrameBytes: info.FrameBytes, ArgBytes: argBytes, SP: info.SP, PointerFree: goManaged,
		}, resolveMathBindings)
}

// entryArgumentWords counts the native words of the entry parameters. Only
// 386 splits a parameter across words; the reported layout is authoritative.
// A missing layout yields -1, which NewCode rejects at Bind.
func entryArgumentWords(report EmissionReport, arity int) int {
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
