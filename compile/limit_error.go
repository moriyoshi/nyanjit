package compile

import (
	"strconv"
	"strings"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// LimitKind names the backend limit a LimitError reports.
type LimitKind uint8

const (
	// LimitSpillWords is the spill storage a function needs, in words,
	// including the homes reserved for variables and parameters.
	LimitSpillWords LimitKind = iota + 1
	// LimitFrameBytes is a stack frame's size in bytes, such as a Go-managed
	// frame above the target's managed-frame limit.
	LimitFrameBytes
	// LimitPredicateNodes is a branch predicate's expanded size in nodes,
	// counting each use of a shared subtree. The limit is ir.MaxPredicateNodes.
	LimitPredicateNodes
	// LimitBranchRange is a branch or literal-pool displacement in bytes
	// beyond what the target's instruction can reach.
	LimitBranchRange
	// LimitArguments is an argument or parameter count: an entry's arity, a
	// label's parameters, or an outbound call's arguments.
	LimitArguments
)

var limitKindNames = [...]string{
	LimitSpillWords:     "spill words",
	LimitFrameBytes:     "frame bytes",
	LimitPredicateNodes: "predicate nodes",
	LimitBranchRange:    "branch range",
	LimitArguments:      "arguments",
}

// String returns the kind's name, for example "spill words".
func (k LimitKind) String() string {
	if int(k) < len(limitKindNames) && limitKindNames[k] != "" {
		return limitKindNames[k]
	}
	return "LimitKind(" + strconv.Itoa(int(k)) + ")"
}

// LimitError reports a function that exceeds a numeric backend limit. It
// wraps ErrBackendLimit, so errors.Is(err, ErrBackendLimit) holds; use
// errors.As to read the numbers, for example to split a function or move
// values out of registers before compiling it again.
//
// Backend limits without a number, such as an operation the target cannot
// lower, return ErrBackendLimit itself.
type LimitError struct {
	// Kind says which limit was exceeded and the unit of Needed and Limit.
	Kind LimitKind
	// Needed is the amount the function needs. Counting can stop once it
	// passes the limit, so Needed may be a lower bound; it is still above
	// Limit. Zero means the amount is unknown.
	Needed int
	// Limit is the largest amount the target accepts. Zero means unknown.
	Limit int
	// Arch is the target architecture when the message names it. It may be
	// empty even for a limit that varies by target.
	Arch string
	// Detail says where the limit was reached, for people; its wording is not
	// part of the API.
	Detail string
}

// Error describes the limit, for example "spill storage needs 4100 words
// (reserved homes/parameters 12), limit 4096: nyanjit: function exceeds
// backend limits".
func (e *LimitError) Error() string {
	var b strings.Builder
	if e.Arch != "" {
		b.WriteString(e.Arch)
		b.WriteByte(' ')
	}
	needed := strconv.Itoa(e.Needed)
	switch {
	case e.Needed == 0:
		b.WriteString(e.Kind.String() + " over the limit")
	case e.Kind == LimitSpillWords:
		b.WriteString("spill storage needs " + needed + " words")
	case e.Kind == LimitFrameBytes:
		b.WriteString("frame needs " + needed + " bytes")
	case e.Kind == LimitPredicateNodes:
		b.WriteString("predicate needs at least " + needed + " expanded nodes")
	case e.Kind == LimitBranchRange:
		b.WriteString("displacement of " + needed + " bytes is out of range")
	case e.Kind == LimitArguments:
		b.WriteString(needed + " arguments")
	default:
		b.WriteString(e.Kind.String() + " needs " + needed)
	}
	if e.Detail != "" {
		b.WriteString(" (" + e.Detail + ")")
	}
	if e.Limit != 0 {
		b.WriteString(", limit " + strconv.Itoa(e.Limit))
	}
	b.WriteString(": ")
	b.WriteString(ErrBackendLimit.Error())
	return b.String()
}

// Unwrap returns ErrBackendLimit.
func (e *LimitError) Unwrap() error { return ErrBackendLimit }

func entryArityError(arity int, architecture string) error {
	return &LimitError{Kind: LimitArguments, Needed: arity, Limit: abi.MaxArgumentsFor(architecture), Arch: architecture, Detail: "entry parameters"}
}

// predicateLimitError reports a predicate whose count stopped above the limit.
func predicateLimitError(detail string) error {
	return &LimitError{Kind: LimitPredicateNodes, Needed: ir.MaxPredicateNodes + 1, Limit: ir.MaxPredicateNodes, Detail: detail}
}

// distance is the byte distance between two PCs.
func distance(from, to int) int {
	if to < from {
		return from - to
	}
	return to - from
}

// callArgumentLimit mirrors abi.PreflightCall's argument-count limit.
func callArgumentLimit(spec abi.CallSpec) (int, string) {
	limit := abi.MaxArgumentsFor(spec.Architecture)
	if spec.Architecture == "" {
		limit = abi.MaxArguments()
	}
	switch {
	case spec.Kind == abi.SystemCall:
		return min(limit, 6), "system call"
	case spec.ABI == abi.Go && spec.Architecture == "":
		return abi.MaxGoCallArguments(), "Go call"
	case spec.ABI == abi.Go:
		return abi.MaxGoCallArgumentsFor(spec.Architecture), "Go call"
	}
	return limit, "C call"
}
