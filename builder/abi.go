package builder

import "github.com/moriyoshi/nyanjit/abi"

// Public aliases retain the original API while sharing ABI contracts with other packages.
type ABIFlavor = abi.Flavor
type StackPractice = abi.StackPractice
type StackPointerRole = abi.StackPointerRole
type CompileOptions = abi.CompileOptions
type SpillStorage = abi.SpillStorage
type ABIStorage = abi.Storage
type ABIWordLocation = abi.WordLocation
type ABIValueLocation = abi.ValueLocation
type EmissionReport = abi.EmissionReport
type CallKind = abi.CallKind
type CallSpec = abi.CallSpec
type EffectPrecision = abi.EffectPrecision
type CallEffect = abi.CallEffect

const (
	ABIC                     = abi.C
	ABIGo                    = abi.Go
	StackNoUse               = abi.StackNoUse
	StackPlatform            = abi.StackPlatform
	StackPointerReserved     = abi.StackPointerReserved
	StackPointerOrdinary     = abi.StackPointerOrdinary
	SpillNone                = abi.SpillNone
	SpillWorkspace           = abi.SpillWorkspace
	SpillStack               = abi.SpillStack
	ABIRegister              = abi.Register
	ABIStack                 = abi.Stack
	FunctionCall             = abi.FunctionCall
	SystemCall               = abi.SystemCall
	EffectConservative       = abi.EffectConservative
	EffectPlatformGuaranteed = abi.EffectPlatformGuaranteed
)
