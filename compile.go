package nyanjit

import (
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Compile lowers program for spec and binds it into executable memory. It
// uses the shared compile.Target for spec, so repeated calls reuse the
// target's resolved state and buffers. Errors are those of compile.NewTarget,
// compile.Lower, Unit.Template and execution.Template.Bind: a program beyond
// a backend limit matches compile.ErrBackendLimit, and errors.As with a
// *compile.LimitError gives the numbers when the limit has one.
func Compile(program ir.Input, spec compile.TargetSpec, f compile.Func) (*execution.Code, error) {
	target, err := compile.NewTarget(spec)
	if err != nil {
		return nil, err
	}
	unit, err := compile.Lower(target, program, f)
	if err != nil {
		return nil, err
	}
	template, err := unit.Template()
	if err != nil {
		return nil, err
	}
	return template.Bind(nil)
}
