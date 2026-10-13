// Package op is the operation vocabulary shared by nyanjit's IR and its
// backend layers: opcodes, integer branch conditions and memory byte orders.
// It has no dependencies, so encoders, the machine stream and register
// allocation can name operations without importing the IR.
//
// The opcode documentation gives each operation's meaning in terms of the
// fields of an ir.Instruction (A, B, C, Args, Imm, Bits, Type), because that
// is where the operations are defined. Backend layers use the same opcodes
// for the arithmetic a machine instruction performs, for example
// machine.Instruction.Arithmetic. Package ir re-exports every name here, so
// IR code can keep writing ir.OpAdd.
//
// Value widths and types are abi.ValueType and abi.ValueKind.
package op
