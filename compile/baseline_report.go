package compile

import "github.com/moriyoshi/nyanjit/backend/abi"

// baselineReport is a baseline Unit's report view. The unit builds the
// report the first time a method is called; Lower and Template need only
// its frame part.
type baselineReport struct{ u *baselineUnit }

func (r baselineReport) Architecture() string               { return r.u.reportView().Architecture() }
func (r baselineReport) ABI() abi.Flavor                    { return r.u.reportView().ABI() }
func (r baselineReport) CPU() abi.CPUProfile                { return r.u.reportView().CPU() }
func (r baselineReport) PPC64CPU() abi.PPC64CPU             { return r.u.reportView().PPC64CPU() }
func (r baselineReport) GoManaged() bool                    { return r.u.reportView().GoManaged() }
func (r baselineReport) Stack() abi.StackPractice           { return r.u.reportView().Stack() }
func (r baselineReport) StackPointer() abi.StackPointerRole { return r.u.reportView().StackPointer() }
func (r baselineReport) ArgumentRegisters() []string        { return r.u.reportView().ArgumentRegisters() }
func (r baselineReport) ResultRegister() string             { return r.u.reportView().ResultRegister() }
func (r baselineReport) ArgumentLocations() []abi.ValueLocation {
	return r.u.reportView().ArgumentLocations()
}
func (r baselineReport) ResultLocation() abi.ValueLocation { return r.u.reportView().ResultLocation() }
func (r baselineReport) ResultWords() int                  { return r.u.reportView().ResultWords() }
func (r baselineReport) WorkspaceRegister() string         { return r.u.reportView().WorkspaceRegister() }
func (r baselineReport) ClobberedRegisters() []string      { return r.u.reportView().ClobberedRegisters() }
func (r baselineReport) ClobberedFPRegisters() []string {
	return r.u.reportView().ClobberedFPRegisters()
}
func (r baselineReport) ClobberedSpecialRegisters() []string {
	return r.u.reportView().ClobberedSpecialRegisters()
}
func (r baselineReport) FlagsClobbered() bool             { return r.u.reportView().FlagsClobbered() }
func (r baselineReport) FPStatusModified() bool           { return r.u.reportView().FPStatusModified() }
func (r baselineReport) ExplicitMemoryReads() bool        { return r.u.reportView().ExplicitMemoryReads() }
func (r baselineReport) ExplicitMemoryWrites() bool       { return r.u.reportView().ExplicitMemoryWrites() }
func (r baselineReport) MayReadMemory() bool              { return r.u.reportView().MayReadMemory() }
func (r baselineReport) MayWriteMemory() bool             { return r.u.reportView().MayWriteMemory() }
func (r baselineReport) HasSystemCalls() bool             { return r.u.reportView().HasSystemCalls() }
func (r baselineReport) HasFunctionCalls() bool           { return r.u.reportView().HasFunctionCalls() }
func (r baselineReport) CodeBytes() int                   { return r.u.reportView().CodeBytes() }
func (r baselineReport) WorkspaceBytes() int              { return r.u.reportView().WorkspaceBytes() }
func (r baselineReport) WorkspaceAlignment() int          { return r.u.reportView().WorkspaceAlignment() }
func (r baselineReport) StackBytes() int                  { return r.u.reportView().StackBytes() }
func (r baselineReport) CallStackBytes() int              { return r.u.reportView().CallStackBytes() }
func (r baselineReport) SpillStorage() abi.SpillStorage   { return r.u.reportView().SpillStorage() }
func (r baselineReport) LoweringStats() abi.LoweringStats { return r.u.reportView().LoweringStats() }
func (r baselineReport) VectorOperations() []abi.VectorOperation {
	return r.u.reportView().VectorOperations()
}
func (r baselineReport) AssumedVectorBytes() int      { return r.u.reportView().AssumedVectorBytes() }
func (r baselineReport) Snapshot() abi.EmissionReport { return r.u.reportView().Snapshot() }
