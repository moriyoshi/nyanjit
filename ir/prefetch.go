package ir

import "github.com/moriyoshi/nyanjit/backend/op"

// PrefetchKind says which access OpPrefetch anticipates; see
// op.PrefetchKind.
type PrefetchKind = op.PrefetchKind

const (
	PrefetchRead  = op.PrefetchRead
	PrefetchWrite = op.PrefetchWrite
)
