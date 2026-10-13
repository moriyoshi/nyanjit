// Package nyanjit holds the Builder, which records target-independent
// values and control flow, and Compile, which lowers a finished program and
// binds it into executable memory. ABI and stack types live in package abi,
// IR types and branch conditions in package ir, and the compiler in package
// compile.
package nyanjit

import "github.com/moriyoshi/nyanjit/builder"

// Value and Predicate are the Builder's value handle types.
type (
	Value     = builder.Value
	Predicate = builder.Predicate
)

// Label is a control-flow destination of one Builder. The zero Label is
// invalid; a Label of another Builder is rejected.
type Label = builder.Label
