//go:build nyanjit_scratch_poison

package regalloc

// Poison builds overwrite released Residency storage to detect stale borrows.
const poison = true
