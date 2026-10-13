package abi

import "slices"

// Clone returns an independent resource/effect report snapshot.
// Nil and non-nil empty slices retain their shape, including nested locations.
func (report EmissionReport) Clone() EmissionReport {
	strings := len(report.CPU.Extensions) + len(report.ArgumentRegisters) + len(report.ClobberedRegisters) + len(report.ClobberedFPRegisters) + len(report.ClobberedSpecialRegisters)
	words := len(report.ResultLocation.Words)
	for _, location := range report.ArgumentLocations {
		words += len(location.Words)
	}
	// Empty payloads need no storage. Detach even empty slices from their
	// backing arrays, preserving shape without retaining caller capacity.
	if strings == 0 && words == 0 && len(report.ArgumentLocations) == 0 && len(report.VectorOperations) == 0 {
		for _, field := range []*[]string{&report.CPU.Extensions, &report.ArgumentRegisters, &report.ClobberedRegisters, &report.ClobberedFPRegisters, &report.ClobberedSpecialRegisters} {
			if *field != nil {
				*field = []string{}
			}
		}
		if report.ArgumentLocations != nil {
			report.ArgumentLocations = []ValueLocation{}
		}
		if report.ResultLocation.Words != nil {
			report.ResultLocation.Words = []WordLocation{}
		}
		if report.VectorOperations != nil {
			report.VectorOperations = []VectorOperation{}
		}
		return report
	}
	// Pack ordinary scalar reports in one typed allocation. Large reports use
	// the general slabs below; each slice is clipped to protect its siblings.
	if strings <= 8 && words <= 4 && len(report.ArgumentLocations) <= 2 && len(report.VectorOperations) == 0 {
		storage := new(struct {
			strings   [8]string
			arguments [2]ValueLocation
			words     [4]WordLocation
		})
		data := storage.strings[:]
		for _, field := range []*[]string{&report.CPU.Extensions, &report.ArgumentRegisters, &report.ClobberedRegisters, &report.ClobberedFPRegisters, &report.ClobberedSpecialRegisters} {
			if *field != nil {
				n := copy(data, *field)
				*field, data = data[:n:n], data[n:]
			}
		}
		if report.ArgumentLocations != nil {
			n := copy(storage.arguments[:], report.ArgumentLocations)
			report.ArgumentLocations = storage.arguments[:n:n]
		}
		dataWords := storage.words[:]
		copyWords := func(location *ValueLocation) {
			if location.Words != nil {
				n := copy(dataWords, location.Words)
				location.Words, dataWords = dataWords[:n:n], dataWords[n:]
			}
		}
		for i := range report.ArgumentLocations {
			copyWords(&report.ArgumentLocations[i])
		}
		copyWords(&report.ResultLocation)
		report.VectorOperations = slices.Clone(report.VectorOperations)
		return report
	}
	packStrings(&report.CPU.Extensions, &report.ArgumentRegisters, &report.ClobberedRegisters, &report.ClobberedFPRegisters, &report.ClobberedSpecialRegisters)
	report.VectorOperations = slices.Clone(report.VectorOperations)
	report.ArgumentLocations, report.ResultLocation = cloneLocations(report.ArgumentLocations, report.ResultLocation)

	return report
}

// Clone makes a callee-effect snapshot independent of every variable-length
// register and ABI-location field, preserving nil versus non-nil empty slices.
func (effect CallEffect) Clone() CallEffect {
	packStrings(&effect.ArgumentRegisters, &effect.ClobberedRegisters, &effect.ClobberedFPRegisters, &effect.ClobberedSpecialRegisters)
	effect.ArgumentLocations, effect.ResultLocation = cloneLocations(effect.ArgumentLocations, effect.ResultLocation)

	return effect
}

// Clip every packed slice so appending cannot overwrite a sibling field.
func packStrings(fields ...*[]string) {
	count := 0
	for _, f := range fields {
		count += len(*f)
	}
	data := make([]string, count)
	for _, f := range fields {
		if *f == nil {
			continue
		}
		n := copy(data, *f)
		*f = data[:n:n]
		data = data[n:]
	}
}

func cloneLocations(arguments []ValueLocation, result ValueLocation) ([]ValueLocation, ValueLocation) {
	count := len(result.Words)
	for _, location := range arguments {
		count += len(location.Words)
	}
	words := make([]WordLocation, count)
	arguments = slices.Clone(arguments)
	for i := range arguments {
		if arguments[i].Words == nil {
			continue
		}
		n := copy(words, arguments[i].Words)
		arguments[i].Words = words[:n:n]
		words = words[n:]
	}
	if result.Words != nil {
		n := copy(words, result.Words)
		result.Words = words[:n:n]
	}
	return arguments, result
}
