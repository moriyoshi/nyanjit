//go:build linux && ppc64le

package abicheck

import "github.com/moriyoshi/nyanjit/backend/abi"

// ELFv2 preserves R14–R31 and F14–F31 and keeps the TOC pointer R2 and
// thread pointer R13; ABIInternal fixes a zero R0, R2, R13 and g (R30). R29
// carries the workspace.
var registers = [...]register{
	{name: "R0", zero: true, goABI: true},
	{name: "R14", c: true},
	{name: "R15", c: true},
	{name: "R16", c: true},
	{name: "R17", c: true},
	{name: "R18", c: true},
	{name: "R19", c: true},
	{name: "R20", c: true},
	{name: "R21", c: true},
	{name: "R22", c: true},
	{name: "R23", c: true},
	{name: "R24", c: true},
	{name: "R25", c: true},
	{name: "R26", c: true},
	{name: "R27", c: true},
	{name: "R28", c: true},
	{name: "R29", c: true},
	{name: "R31", c: true},
	{name: "F14", c: true},
	{name: "F15", c: true},
	{name: "F16", c: true},
	{name: "F17", c: true},
	{name: "F18", c: true},
	{name: "F19", c: true},
	{name: "F20", c: true},
	{name: "F21", c: true},
	{name: "F22", c: true},
	{name: "F23", c: true},
	{name: "F24", c: true},
	{name: "F25", c: true},
	{name: "F26", c: true},
	{name: "F27", c: true},
	{name: "F28", c: true},
	{name: "F29", c: true},
	{name: "F30", c: true},
	{name: "F31", c: true},
	{name: "SP", recorded: true, c: true, goABI: true},
	{name: "R2", recorded: true, c: true, goABI: true},
	{name: "R13", recorded: true, c: true, goABI: true},
	{name: "g", recorded: true, c: true, goABI: true},
}

const registerSlots = 44

// The first eight integer registers coincide for C and ABIInternal.
func argumentWord(_ abi.Flavor, i int) (abi.WordLocation, bool) {
	return registerWord([]string{"R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10"}, i)
}

func resultWord(abi.Flavor, int) abi.WordLocation {
	return abi.WordLocation{Storage: abi.Register, Register: "R3"}
}
