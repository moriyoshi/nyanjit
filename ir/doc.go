// Package ir defines NyanJIT's target-independent intermediate
// representation: Program, its Instructions and labeled Control flow,
// scoped Variables, validation, SealedProgram and Analysis.
//
// A front end builds a Program, either with the root package's Builder or
// directly, seals it with Seal or SealOwned, and passes the SealedProgram to
// package compile. Values are defined once and are usable in every block
// their definition dominates; label parameters are SSA block parameters.
// The Builder never produces label parameters: it merges values that differ
// by path through Variables. Direct front ends such as ProgramBuilder use them.
// Validation failures are *ValidationError values wrapping
// ErrInvalidProgram.
//
// The full specification of every opcode and of the value, block, scope,
// variable and predicate rules is docs/ir.md in the repository.
package ir
