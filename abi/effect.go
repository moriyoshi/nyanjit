package abi

// Clone returns an independent resource/effect report snapshot.
func (report EmissionReport) Clone() EmissionReport {
	report.CPU = report.CPU.Clone()
	report.VectorOperations = append([]VectorOperation(nil), report.VectorOperations...)
	report.ArgumentRegisters = append([]string(nil), report.ArgumentRegisters...)
	report.ClobberedRegisters = append([]string(nil), report.ClobberedRegisters...)
	report.ClobberedFPRegisters = append([]string(nil), report.ClobberedFPRegisters...)
	report.ClobberedSpecialRegisters = append([]string(nil), report.ClobberedSpecialRegisters...)
	if report.ArgumentLocations != nil {
		locations := make([]ValueLocation, len(report.ArgumentLocations))
		for i, location := range report.ArgumentLocations {
			locations[i].Words = append([]WordLocation(nil), location.Words...)
		}
		report.ArgumentLocations = locations
	}
	report.ResultLocation.Words = append([]WordLocation(nil), report.ResultLocation.Words...)
	return report
}

// Clone makes a callee-effect snapshot independent of every variable-length
// register and ABI-location field.
func (effect CallEffect) Clone() CallEffect {
	effect.ArgumentRegisters = append([]string(nil), effect.ArgumentRegisters...)
	effect.ClobberedRegisters = append([]string(nil), effect.ClobberedRegisters...)
	effect.ClobberedFPRegisters = append([]string(nil), effect.ClobberedFPRegisters...)
	effect.ClobberedSpecialRegisters = append([]string(nil), effect.ClobberedSpecialRegisters...)
	if effect.ArgumentLocations != nil {
		locations := make([]ValueLocation, len(effect.ArgumentLocations))
		for i, location := range effect.ArgumentLocations {
			locations[i].Words = append([]WordLocation(nil), location.Words...)
		}
		effect.ArgumentLocations = locations
	}
	effect.ResultLocation.Words = append([]WordLocation(nil), effect.ResultLocation.Words...)
	return effect
}
