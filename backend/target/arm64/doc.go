// Package arm64 defines AArch64 register policy, instruction sizes and
// validation, and the byte and text assemblers for selected instructions.
//
// The generic encoder path: build machine.Instruction values and visit them
// with machine.Visit and a ByteAssembler or TextAssembler. FORMS.md lists the
// accepted instruction forms, their operand constraints and sizes
// (PhysicalSize); a table test checks every row. GoFrame describes a laid-out
// function to the Go unwinder for goregister.
//
// Construct this package's exported structs with keyed fields. Field order is
// not part of the API: fields may be reordered or packed, though removing a
// field or changing its meaning is a breaking change.
package arm64
