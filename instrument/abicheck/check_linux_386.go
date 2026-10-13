//go:build linux && 386

package abicheck

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
)

// cdecl preserves EBX, ESI, EDI and EBP; the stack-based Go ABI fixes only
// SP and the TLS slot holding g. EBP carries the workspace.
var registers = [...]register{
	{name: "EBX", c: true},
	{name: "ESI", c: true},
	{name: "EDI", c: true},
	{name: "EBP", c: true},
	{name: "SP", recorded: true, c: true, goABI: true},
	{name: "g", recorded: true, c: true, goABI: true},
}

const registerSlots = 8

// Both conventions pass every word on the stack, above the return address.
func argumentWord(_ abi.Flavor, i int) (abi.WordLocation, bool) {
	if i >= execution.MaxArgumentWords {
		return abi.WordLocation{}, false
	}
	return abi.WordLocation{Storage: abi.Stack, StackOffset: 4 + 4*i}, true
}

// The Go result slot directly follows the argument words.
func resultWord(flavor abi.Flavor, words int) abi.WordLocation {
	if flavor == abi.Go {
		return abi.WordLocation{Storage: abi.Stack, StackOffset: 4 + 4*words}
	}
	return abi.WordLocation{Storage: abi.Register, Register: "EAX"}
}
