package execution

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"testing"
)

func TestTemplateOwnsFrameSP(t *testing.T) {
	frame := goregister.Frame{SP: make([]goregister.SPPoint, 1)}
	code := []byte{1, 2, 3}
	options := CodeOptions{ParamBits: []uint8{64}}
	report := abi.EmissionReport{CPU: abi.CPUProfile{Extensions: []string{"fp"}}}
	template := NewTemplate(code, abi.NewReport(report), nil, options, frame)
	code[0] = 99
	frame.SP[0] = goregister.SPPoint{PC: 99}
	options.ParamBits[0] = 8
	report.CPU.Extensions[0] = "changed"
	if template.code[0] != 1 || template.frame.SP[0].PC != 0 || template.options.ParamBits[0] != 64 || template.report.CPU().Extensions[0] != "fp" {
		t.Fatal("template aliases constructor input")
	}
}

func TestTemplateReportsRemainIndependent(t *testing.T) {
	report := abi.EmissionReport{ArgumentRegisters: []string{"X0"}, ArgumentLocations: []abi.ValueLocation{{Words: []abi.WordLocation{{Register: "X0"}}}}}
	template := NewTemplate([]byte{1}, abi.NewReport(report), nil, CodeOptions{}, goregister.Frame{})
	first := template.Report()
	first.ArgumentRegisters()[0] = "changed"
	first.ArgumentLocations()[0].Words[0].Register = "changed"
	second := template.Report()
	if second.ArgumentRegisters()[0] != "X0" || second.ArgumentLocations()[0].Words[0].Register != "X0" {
		t.Fatal("public report mutates the template")
	}
}
