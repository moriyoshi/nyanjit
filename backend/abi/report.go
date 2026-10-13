package abi

// Report is a read-only view of an emission report: the resources and
// effects of one lowered function. Implementations are immutable and safe for
// concurrent use. Getters that return slices or other reference values return
// the caller's own copy; Snapshot returns every field at once.
type Report interface {
	Architecture() string
	ABI() Flavor
	// CPU is the resolved CPU profile the code requires.
	CPU() CPUProfile
	PPC64CPU() PPC64CPU
	GoManaged() bool
	Stack() StackPractice
	StackPointer() StackPointerRole
	ArgumentRegisters() []string
	ResultRegister() string
	ArgumentLocations() []ValueLocation
	ResultLocation() ValueLocation
	// ResultWords counts logical uint64 words; a scalar uint64 on 386 still
	// occupies two physical 32-bit registers.
	ResultWords() int
	WorkspaceRegister() string
	// ClobberedRegisters excludes trampoline writes.
	ClobberedRegisters() []string
	ClobberedFPRegisters() []string
	// ClobberedSpecialRegisters names modified resources such as LR, CTR,
	// condition fields, and status registers independently of general registers.
	ClobberedSpecialRegisters() []string
	FlagsClobbered() bool
	// FPStatusModified reports arithmetic that may change FP exception flags
	// or a call whose FP status effects are not known at this boundary.
	FPStatusModified() bool
	ExplicitMemoryReads() bool
	ExplicitMemoryWrites() bool
	MayReadMemory() bool
	MayWriteMemory() bool
	HasSystemCalls() bool
	HasFunctionCalls() bool
	CodeBytes() int
	WorkspaceBytes() int
	WorkspaceAlignment() int
	StackBytes() int
	CallStackBytes() int
	SpillStorage() SpillStorage
	LoweringStats() LoweringStats
	VectorOperations() []VectorOperation
	// AssumedVectorBytes records a scalable vector length; phase 1 leaves it zero.
	AssumedVectorBytes() int
	// Snapshot returns an independent copy of every field.
	Snapshot() EmissionReport
}

// NewReport returns a view of an independent copy of r.
func NewReport(r EmissionReport) Report { return ReportOf(r.Clone()) }

// ReportOf wraps r as a Report without copying it, transferring ownership of
// r's slices to the view. It is for producers that build r for the view: the
// caller may keep reading r but must never modify it, or any slice it refers
// to, afterwards. Use NewReport otherwise.
func ReportOf(r EmissionReport) Report { return &reportView{r: r} }

type reportView struct{ r EmissionReport }

func (v *reportView) Architecture() string           { return v.r.Architecture }
func (v *reportView) ABI() Flavor                    { return v.r.ABI }
func (v *reportView) CPU() CPUProfile                { return v.r.CPU.Clone() }
func (v *reportView) PPC64CPU() PPC64CPU             { return v.r.PPC64CPU }
func (v *reportView) GoManaged() bool                { return v.r.GoManaged }
func (v *reportView) Stack() StackPractice           { return v.r.Stack }
func (v *reportView) StackPointer() StackPointerRole { return v.r.StackPointer }
func (v *reportView) ArgumentRegisters() []string    { return cloneStrings(v.r.ArgumentRegisters) }
func (v *reportView) ResultRegister() string         { return v.r.ResultRegister }
func (v *reportView) ArgumentLocations() []ValueLocation {
	locations, _ := cloneLocations(v.r.ArgumentLocations, ValueLocation{})
	return locations
}
func (v *reportView) ResultLocation() ValueLocation {
	_, result := cloneLocations(nil, v.r.ResultLocation)
	return result
}
func (v *reportView) ResultWords() int               { return v.r.ResultWords }
func (v *reportView) WorkspaceRegister() string      { return v.r.WorkspaceRegister }
func (v *reportView) ClobberedRegisters() []string   { return cloneStrings(v.r.ClobberedRegisters) }
func (v *reportView) ClobberedFPRegisters() []string { return cloneStrings(v.r.ClobberedFPRegisters) }
func (v *reportView) ClobberedSpecialRegisters() []string {
	return cloneStrings(v.r.ClobberedSpecialRegisters)
}
func (v *reportView) FlagsClobbered() bool         { return v.r.FlagsClobbered }
func (v *reportView) FPStatusModified() bool       { return v.r.FPStatusModified }
func (v *reportView) ExplicitMemoryReads() bool    { return v.r.ExplicitMemoryReads }
func (v *reportView) ExplicitMemoryWrites() bool   { return v.r.ExplicitMemoryWrites }
func (v *reportView) MayReadMemory() bool          { return v.r.MayReadMemory }
func (v *reportView) MayWriteMemory() bool         { return v.r.MayWriteMemory }
func (v *reportView) HasSystemCalls() bool         { return v.r.HasSystemCalls }
func (v *reportView) HasFunctionCalls() bool       { return v.r.HasFunctionCalls }
func (v *reportView) CodeBytes() int               { return v.r.CodeBytes }
func (v *reportView) WorkspaceBytes() int          { return v.r.WorkspaceBytes }
func (v *reportView) WorkspaceAlignment() int      { return v.r.WorkspaceAlignment }
func (v *reportView) StackBytes() int              { return v.r.StackBytes }
func (v *reportView) CallStackBytes() int          { return v.r.CallStackBytes }
func (v *reportView) SpillStorage() SpillStorage   { return v.r.SpillStorage }
func (v *reportView) LoweringStats() LoweringStats { return v.r.LoweringStats }
func (v *reportView) VectorOperations() []VectorOperation {
	if v.r.VectorOperations == nil {
		return nil
	}
	return append([]VectorOperation{}, v.r.VectorOperations...)
}
func (v *reportView) AssumedVectorBytes() int  { return v.r.AssumedVectorBytes }
func (v *reportView) Snapshot() EmissionReport { return v.r.Clone() }

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string{}, s...)
}
