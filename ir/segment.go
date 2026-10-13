package ir

import "reflect"

// Segment is one terminated block of a function, in layout order, as a
// streaming front end hands it to a consumer such as compile.Stream.
// Concatenating a function's segments gives a Program: Code makes up
// Instructions, Variables and ScopeExits append to those tables, and the label
// steps for Labels followed by Step make up Control.Steps. Every Program rule
// applies, including dominance (docs/ir.md §6); a consumer checks rules that
// span segments when the function ends.
//
// IDs are program-wide: Code[i] has ID Base+i, and Step's Start, End,
// LeaveStart and LeaveEnd are program coordinates. An immutable Variable that
// a later segment declares is a capture: it adds its Initial values to the
// Args of its scope's OpScopeEnter. A consumer must not retain a Segment or
// anything it refers to after it returns, because the front end reuses the
// storage.
type Segment struct {
	// Labels are bound at the start of the segment, in binding order. The
	// first segment is the function entry and binds none.
	Labels []Label
	// Base is the ID of Code[0].
	Base int
	// Code holds the segment's instructions. OpParam appears only in the
	// first segment.
	Code []Instruction
	// Variables declared in this segment. Their indices continue the
	// table of earlier segments.
	Variables []Variable
	// ScopeExits whose Leave lies in this segment, in order.
	ScopeExits []ScopeExit
	// Step ends the segment with TermJump, TermBranch or TermReturn. It is
	// never a label step.
	Step ControlStep
	// ParameterTypes is set on the first segment only, as for
	// Program.ParameterTypes.
	ParameterTypes []reflect.Type
}
