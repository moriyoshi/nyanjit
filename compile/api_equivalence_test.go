package compile_test

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// recordedOptions decodes the option records of the frozen corpus and of the
// golden API record, which were captured before TargetSpec and Func existed.
type recordedOptions struct {
	Architecture        string
	CPU                 abi.CPUProfile
	AMD64FMA            bool
	Features            []string
	ABI                 abi.Flavor
	PPC64CPU            abi.PPC64CPU
	ManagedCalls        bool
	Stack               abi.StackPractice
	StackPointer        abi.StackPointerRole
	StackBytesAvailable int
	AssumePointerFree   bool
	PreferCompactLoops  bool
	Harden              bool
}

// split maps recorded options onto a target and a function.
func (o recordedOptions) split() (compile.TargetSpec, compile.Func, error) {
	spec := compile.TargetSpec{Architecture: o.Architecture, CPU: o.CPU.Clone()}
	if o.AMD64FMA && !spec.CPU.Has("fma") {
		spec.CPU.Extensions = append(spec.CPU.Extensions, "fma")
	}
	if o.PPC64CPU != 0 {
		level, err := o.PPC64CPU.Resolve()
		if err != nil {
			return spec, compile.Func{}, err
		}
		spec.CPU.Level = level.String()
	}
	switch {
	case o.Features == nil:
		spec.Vectors = compile.VectorsAuto()
	case len(o.Features) == 0:
		spec.Vectors = compile.VectorsOff()
	default:
		spec.Vectors = compile.VectorsRequire(o.Features...)
	}
	f := compile.Func{
		ABI:               o.ABI,
		Stack:             compile.StackPolicy{Practice: o.Stack, Pointer: o.StackPointer, BytesAvailable: o.StackBytesAvailable},
		AssumePointerFree: o.AssumePointerFree, PreferCompactLoops: o.PreferCompactLoops, Harden: o.Harden,
	}
	return spec, f, nil
}

type apiGolden struct {
	Fixture string            `json:"f"`
	Variant string            `json:"v"`
	Options recordedOptions   `json:"o"`
	Entry   string            `json:"e"`
	Out     map[string]string `json:"out,omitempty"`
	Err     string            `json:"err,omitempty"`
	Is      []string          `json:"is,omitempty"`
}

var apiSentinels = []struct {
	name string
	err  error
}{
	{"InvalidFunction", compile.ErrInvalidFunction}, {"BackendLimit", compile.ErrBackendLimit},
	{"Unsupported", compile.ErrUnsupported}, {"UnboundSymbol", compile.ErrUnboundSymbol},
	{"CodeBaseRequired", compile.ErrCodeBaseRequired}, {"UnsupportedABI", compile.ErrUnsupportedABI},
	{"UnsupportedStackRole", compile.ErrUnsupportedStackRole}, {"StackRequired", compile.ErrStackRequired},
	{"ExternalCallOnly", compile.ErrExternalCallOnly}, {"PointerMapsRequired", compile.ErrPointerMapsRequired},
	{"CPUUnsupported", compile.ErrCPUUnsupported}, {"InvalidSpec", abi.ErrInvalidSpec},
	{"InvalidProgram", ir.ErrInvalidProgram},
}

func apiHash(v any) string {
	var b []byte
	switch x := v.(type) {
	case string:
		b = []byte(x)
	case []byte:
		b = x
	default:
		var err error
		if b, err = json.Marshal(v); err != nil {
			panic(err)
		}
	}
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:12])
}

func apiSentinelNames(err error) []string {
	var names []string
	for _, s := range apiSentinels {
		if err != nil && errors.Is(err, s.err) {
			names = append(names, s.name)
		}
	}
	return names
}

func apiSymbols(p *ir.Program) map[string]uint64 {
	var names []string
	seen := map[string]bool{}
	for _, ins := range p.Instructions {
		if ins.Symbol != "" && ins.Symbol != ir.MathPi4Symbol && !seen[ins.Symbol] {
			seen[ins.Symbol] = true
			names = append(names, ins.Symbol)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	m := map[string]uint64{}
	for i, n := range names {
		m[n] = 0x1234 + uint64(i)*0x40
	}
	return m
}

func hasFunctionCalls(p *ir.Program) bool {
	for _, ins := range p.Instructions {
		if ins.Op == ir.OpCallC || ins.Op == ir.OpCallGo {
			return true
		}
	}
	return false
}

// replay runs one golden entry through the new API.
func replayAPIEntry(r apiGolden, p *ir.Program, keepFlagWrites bool) (map[string]string, error) {
	spec, f, err := r.Options.split()
	if err != nil {
		return nil, err
	}
	newTarget := compile.NewTarget
	if keepFlagWrites {
		newTarget = compile.KeepFlagWrites
	}
	target, err := newTarget(spec)
	if err != nil {
		return nil, err
	}
	// Old report and listing entry points lowered without callable checks.
	f.Context = compile.ContextExternal
	if r.Options.ManagedCalls {
		f.Context = compile.ContextGoManaged
	}
	lower := func() (*compile.Unit, error) { return compile.Lower(target, p, f) }
	template := func() (*execution.Template, error) {
		u, err := lower()
		if err != nil {
			return nil, err
		}
		return u.Template()
	}
	switch r.Entry {
	case "assembly", "assembly.constants":
		if r.Entry == "assembly.constants" {
			f.Constants = apiSymbols(p)
		}
		u, err := lower()
		if err != nil {
			return nil, err
		}
		listing, err := u.ListingSymbolic()
		return map[string]string{"listing": apiHash(listing)}, err
	case "lower":
		u, err := lower()
		if err != nil {
			return nil, err
		}
		return map[string]string{"report": apiHash(u.Report().Snapshot())}, nil
	case "lower.listing":
		u, err := lower()
		if err != nil {
			return nil, err
		}
		listing, err := u.Listing()
		return map[string]string{"listing": apiHash(listing)}, err
	case "lower.template", "prepare", "prepareforeign", "preparego":
		switch r.Entry {
		case "lower.template":
			// Lower chose the boundary from the program.
			f.Context = compile.ContextNative
			if r.Options.ManagedCalls {
				f.Context = compile.ContextGoManaged
			} else if hasFunctionCalls(p) && r.Options.ABI == abi.C {
				f.Context = compile.ContextForeign
			}
		case "prepare":
			f.Context = compile.ContextNative
		case "prepareforeign":
			f.Context = compile.ContextForeign
		case "preparego":
			f.Context = compile.ContextGoManaged
		}
		t, err := template()
		if err != nil {
			return map[string]string{}, err
		}
		return map[string]string{"report": apiHash(t.Report().Snapshot()), "relocations": apiHash(t.Relocations())}, nil
	case "scopes":
		u, err := lower()
		if err != nil {
			return nil, err
		}
		scopes, err := u.RegisterScopes()
		return map[string]string{"scopes": apiHash(scopes)}, err
	case "vectors":
		v := target.Vectors()
		return map[string]string{"bits": apiHash(v.PreferredBits), "features": apiHash(v.Features)}, nil
	case "analyze", "analyze.constants":
		if r.Entry == "analyze.constants" {
			f.Constants = apiSymbols(p)
		}
		u, err := lower()
		if err != nil {
			return nil, err
		}
		return map[string]string{"report": apiHash(u.Report().Snapshot())}, nil
	case "emittemplate":
		t, err := template()
		if err != nil {
			return nil, err
		}
		linked, lerr := t.LinkAt(apiSymbols(p), 0x100000)
		out := map[string]string{"report": apiHash(t.Report().Snapshot()), "relocations": apiHash(t.Relocations()), "linkat": apiHash(linked)}
		markHostAddresses(out, t)
		if lerr != nil {
			out["linkat"] = "error: " + lerr.Error()
		}
		return out, nil
	case "emit":
		t, err := template()
		if err != nil {
			return nil, err
		}
		code, err := t.Bytes(nil)
		if err != nil {
			return nil, err
		}
		out := map[string]string{"bytes": apiHash(code), "report": apiHash(t.Report().Snapshot())}
		markHostAddresses(out, t)
		return out, nil
	}
	return nil, fmt.Errorf("unknown golden entry %q", r.Entry)
}

// intendedDifference explains a golden entry whose new-API result differs by
// design. The test fails on any difference this does not explain.
func intendedDifference(r apiGolden, err error) (string, bool) {
	cross := r.Options.Architecture != "" && r.Options.Architecture != runtime.GOARCH
	switch {
	case (r.Entry == "emit" || r.Entry == "emittemplate") && r.Options.ManagedCalls && r.Err == "" &&
		errors.Is(err, compile.ErrBackendLimit) && strings.Contains(err.Error(), "managed frame"):
		// EmitTemplate skipped the managed-frame limit that every Go-managed
		// template now checks.
		return "go-managed templates check the managed frame limit", true
	case r.Options.ManagedCalls && !r.Options.AssumePointerFree && errors.Is(err, compile.ErrPointerMapsRequired):
		// Lower checks the call context; report and listing entry points did not.
		return "Lower checks a Go-managed function for pointer-free values", true
	case r.Entry == "lower" && cross && r.Options.ManagedCalls && slices.Contains(r.Is, "Unsupported"):
		// Lower rejected cross-target Go-managed programs with C calls before
		// lowering them; now only Listing does, and errors found while
		// lowering are reported instead.
		return "cross-target Go-managed C calls lower; only Listing needs the host", true
	case r.Entry == "scopes" && r.Err == "" && errors.Is(err, compile.ErrStackRequired) &&
		r.Fixture == "f1f3adf1f3b81fde0e27b730d2d387b47f9340a3b90fd469036990b6b6d41edb.json.gz" && r.Variant == "scalar":
		// RegisterScopes now lowers the Unit first, and this program's calls
		// need a stack frame its recorded options do not provide.
		return "RegisterScopes needs a lowered Unit", true
	}
	return "", false
}

// reviewedOutput names one recorded output of one golden entry.
type reviewedOutput struct{ fixture, variant, entry, key string }

// reviewedChange is an output change that was reviewed by hand: the entry's
// error state must be unchanged and the output must change exactly from
// before to after. Every other output of the entry is still compared.
type reviewedChange struct{ reason, before, after string }

// resultRegisterLast explains listings in which the variable home planner
// offers the result register only after every other register and gives it
// to a variable a straight-line program returns.
// deadFlagLEA explains amd64 outputs that the record reproduces once dead-flag
// elision is off: additions whose flags no path reads select LEA.
const deadFlagLEA = "amd64 additions with dead flags select LEA"

// deadFlagSBit explains arm64 outputs that the record reproduces once
// dead-flag elision is off: ADD, SUB and AND whose flags no path reads drop
// the S bit.
const deadFlagSBit = "arm64 arithmetic with dead flags drops the S bit"

// deadFlagReasons names, per architecture, the outputs that dead-flag
// elision changes.
var deadFlagReasons = map[string]string{"amd64": deadFlagLEA, "arm64": deadFlagSBit}

const resultRegisterLast = "variable homes take the result register last, or for the returned variable"

var reviewedChanges = map[reviewedOutput]reviewedChange{
	{"040b8cc1175cf493e3ff2195584936b0ca5d3ea01d7d95069fb4641ffbb04f94.json.gz", "arch:386", "assembly", "listing"}:          {"vector spills reuse freed slots, renumbering a 386 GP pair spill", "1eae7c59419434fa1771a150", "9a1444517e51f6579ef9ec46"},
	{"040b8cc1175cf493e3ff2195584936b0ca5d3ea01d7d95069fb4641ffbb04f94.json.gz", "arch:386", "lower.listing", "listing"}:     {"vector spills reuse freed slots, renumbering a 386 GP pair spill", "1eae7c59419434fa1771a150", "9a1444517e51f6579ef9ec46"},
	{"426bd72da5b5ce408cfee5da190a1e9d910667b8a163b14dddf3e2b59ccaa088.json.gz", "arch:ppc64le", "assembly", "listing"}:      {"ppc64le zero-extends 32-bit high products (clrldi after mulhwu)", "786fed5b7fe2a46239ebb1d5", "fd6e3f7cacc7a0715e25704f"},
	{"426bd72da5b5ce408cfee5da190a1e9d910667b8a163b14dddf3e2b59ccaa088.json.gz", "arch:ppc64le", "lower", "report"}:          {"ppc64le zero-extends 32-bit high products (clrldi after mulhwu)", "e51d85dd72b1225a7be63569", "2dba31560fb0abd0f2cef335"},
	{"426bd72da5b5ce408cfee5da190a1e9d910667b8a163b14dddf3e2b59ccaa088.json.gz", "arch:ppc64le", "lower.listing", "listing"}: {"ppc64le zero-extends 32-bit high products (clrldi after mulhwu)", "786fed5b7fe2a46239ebb1d5", "fd6e3f7cacc7a0715e25704f"},
	{"9334e86e9272d558fb82e9aefea9c7d9389d663c42cf8982bbc0d59657c14e42.json.gz", "arch:ppc64le", "assembly", "listing"}:      {"ppc64le zero-extends 32-bit high products (clrldi after mulhwu)", "44b8c38bc84eda4b52c606dc", "bff5b059379dbb8ce1b3a283"},
	{"9334e86e9272d558fb82e9aefea9c7d9389d663c42cf8982bbc0d59657c14e42.json.gz", "arch:ppc64le", "lower", "report"}:          {"ppc64le zero-extends 32-bit high products (clrldi after mulhwu)", "6d5ac066aad99ae2e3d6a8c0", "21e15b27d10895555cf2fd46"},
	{"9334e86e9272d558fb82e9aefea9c7d9389d663c42cf8982bbc0d59657c14e42.json.gz", "arch:ppc64le", "lower.listing", "listing"}: {"ppc64le zero-extends 32-bit high products (clrldi after mulhwu)", "44b8c38bc84eda4b52c606dc", "bff5b059379dbb8ce1b3a283"},
	{"04068d9192ee05477f84de533f69c22fbb7cc34e3b235bfbb3db75f945063164.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "c255efc7e3be6c50cda376d2", "a9a9273d11dd003ef6f42379"},
	{"04068d9192ee05477f84de533f69c22fbb7cc34e3b235bfbb3db75f945063164.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "7bb33bb55c2c305ff26b7211", "15b0d7972b84c1974efb7540"},
	{"04068d9192ee05477f84de533f69c22fbb7cc34e3b235bfbb3db75f945063164.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "7bb33bb55c2c305ff26b7211", "15b0d7972b84c1974efb7540"},
	{"04068d9192ee05477f84de533f69c22fbb7cc34e3b235bfbb3db75f945063164.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "c255efc7e3be6c50cda376d2", "a9a9273d11dd003ef6f42379"},
	{"18ca1822d96ea8237f3c211649efeb328ef0ef4e3c38348988ee675ccbbd56e9.json.gz", "as-is", "analyze", "report"}:               {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"18ca1822d96ea8237f3c211649efeb328ef0ef4e3c38348988ee675ccbbd56e9.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "1492ff8d61ea0241315a335f", "098a6c399f242f24fe56d196"},
	{"18ca1822d96ea8237f3c211649efeb328ef0ef4e3c38348988ee675ccbbd56e9.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "e858f8e8baa6d9f8005fa2d5", "cf61a5b610cca0cf854b734d"},
	{"18ca1822d96ea8237f3c211649efeb328ef0ef4e3c38348988ee675ccbbd56e9.json.gz", "as-is", "emit", "report"}:                  {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"18ca1822d96ea8237f3c211649efeb328ef0ef4e3c38348988ee675ccbbd56e9.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "e858f8e8baa6d9f8005fa2d5", "cf61a5b610cca0cf854b734d"},
	{"18ca1822d96ea8237f3c211649efeb328ef0ef4e3c38348988ee675ccbbd56e9.json.gz", "as-is", "emittemplate", "report"}:          {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"18ca1822d96ea8237f3c211649efeb328ef0ef4e3c38348988ee675ccbbd56e9.json.gz", "as-is", "lower", "report"}:                 {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"18ca1822d96ea8237f3c211649efeb328ef0ef4e3c38348988ee675ccbbd56e9.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "1492ff8d61ea0241315a335f", "098a6c399f242f24fe56d196"},
	{"18ca1822d96ea8237f3c211649efeb328ef0ef4e3c38348988ee675ccbbd56e9.json.gz", "as-is", "lower.template", "report"}:        {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"18ca1822d96ea8237f3c211649efeb328ef0ef4e3c38348988ee675ccbbd56e9.json.gz", "as-is", "prepare", "report"}:               {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "as-is", "analyze", "report"}:               {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "19aec262a0a08bba22201a4e", "00e65f67d1d5b5ed9814ac55"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "af5d1d974369fe7e03b2c606", "5b6d1243d4e3ef29dd922252"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "as-is", "emit", "report"}:                  {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "af5d1d974369fe7e03b2c606", "5b6d1243d4e3ef29dd922252"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "as-is", "emittemplate", "report"}:          {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "as-is", "lower", "report"}:                 {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "19aec262a0a08bba22201a4e", "00e65f67d1d5b5ed9814ac55"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "as-is", "lower.template", "report"}:        {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "as-is", "prepare", "report"}:               {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "analyze", "report"}:                  {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "assembly", "listing"}:                {resultRegisterLast, "19aec262a0a08bba22201a4e", "00e65f67d1d5b5ed9814ac55"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "emit", "bytes"}:                      {resultRegisterLast, "af5d1d974369fe7e03b2c606", "5b6d1243d4e3ef29dd922252"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "emit", "report"}:                     {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "emittemplate", "linkat"}:             {resultRegisterLast, "af5d1d974369fe7e03b2c606", "5b6d1243d4e3ef29dd922252"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "emittemplate", "report"}:             {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "lower", "report"}:                    {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "lower.listing", "listing"}:           {resultRegisterLast, "19aec262a0a08bba22201a4e", "00e65f67d1d5b5ed9814ac55"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "lower.template", "report"}:           {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "prepare", "report"}:                  {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "go", "preparego", "report"}:                {resultRegisterLast, "5a5edde246d8136daba7a843", "d79316656cba02aa2e6f48c8"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "analyze", "report"}:             {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "assembly", "listing"}:           {resultRegisterLast, "deb09941da1d3771ea191663", "d6d70bcc39206c1d3704d131"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "emit", "bytes"}:                 {resultRegisterLast, "b008ba97ce49d0da22cd6a72", "1ab901bea6e3320dfa9ef748"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "emit", "report"}:                {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "emittemplate", "linkat"}:        {resultRegisterLast, "b008ba97ce49d0da22cd6a72", "1ab901bea6e3320dfa9ef748"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "emittemplate", "report"}:        {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "lower", "report"}:               {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "lower.listing", "listing"}:      {resultRegisterLast, "deb09941da1d3771ea191663", "d6d70bcc39206c1d3704d131"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "lower.template", "report"}:      {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "prepare", "report"}:             {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "prepareforeign", "report"}:      {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"1e09e432ae3b0f3ccdf65d51a94fc380bddafff305326e9d929ea80015527277.json.gz", "managed", "preparego", "report"}:           {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"4641dc2585eb7d52d0c26f4f921b70f62737689a474e6f14963761bc9fcffd2e.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "4d58c62b1d0c38ac7f7f869b", "41e08a26ea7fab014521e1b9"},
	{"4641dc2585eb7d52d0c26f4f921b70f62737689a474e6f14963761bc9fcffd2e.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "3727daf35957061565262f4c", "08c62363c9b58d9ba07b000c"},
	{"4641dc2585eb7d52d0c26f4f921b70f62737689a474e6f14963761bc9fcffd2e.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "3727daf35957061565262f4c", "08c62363c9b58d9ba07b000c"},
	{"4641dc2585eb7d52d0c26f4f921b70f62737689a474e6f14963761bc9fcffd2e.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "4d58c62b1d0c38ac7f7f869b", "41e08a26ea7fab014521e1b9"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "analyze", "report"}:               {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "202a0872e12ca777e1744241", "b959e3a52b05cfd64e26cec8"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "6619876bb9d24b2944822a7d", "b86f2ccc9233845cdd933bfc"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "emit", "report"}:                  {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "6619876bb9d24b2944822a7d", "b86f2ccc9233845cdd933bfc"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "emittemplate", "report"}:          {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "lower", "report"}:                 {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "202a0872e12ca777e1744241", "b959e3a52b05cfd64e26cec8"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "lower.template", "report"}:        {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "prepare", "report"}:               {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "as-is", "prepareforeign", "report"}:        {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "analyze", "report"}:                  {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "assembly", "listing"}:                {resultRegisterLast, "202a0872e12ca777e1744241", "b959e3a52b05cfd64e26cec8"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "emit", "bytes"}:                      {resultRegisterLast, "6619876bb9d24b2944822a7d", "b86f2ccc9233845cdd933bfc"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "emit", "report"}:                     {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "emittemplate", "linkat"}:             {resultRegisterLast, "6619876bb9d24b2944822a7d", "b86f2ccc9233845cdd933bfc"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "emittemplate", "report"}:             {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "lower", "report"}:                    {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "lower.listing", "listing"}:           {resultRegisterLast, "202a0872e12ca777e1744241", "b959e3a52b05cfd64e26cec8"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "lower.template", "report"}:           {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "prepare", "report"}:                  {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "go", "preparego", "report"}:                {resultRegisterLast, "5a5edde246d8136daba7a843", "d79316656cba02aa2e6f48c8"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "analyze", "report"}:             {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "assembly", "listing"}:           {resultRegisterLast, "d864ecdad348397fd6e7026d", "33275c222ec390bc7ede8ada"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "emit", "bytes"}:                 {resultRegisterLast, "fbfcdb82f2a8f5f41bebebba", "6873edc93ceff134fb0780af"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "emit", "report"}:                {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "emittemplate", "linkat"}:        {resultRegisterLast, "fbfcdb82f2a8f5f41bebebba", "6873edc93ceff134fb0780af"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "emittemplate", "report"}:        {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "lower", "report"}:               {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "lower.listing", "listing"}:      {resultRegisterLast, "d864ecdad348397fd6e7026d", "33275c222ec390bc7ede8ada"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "lower.template", "report"}:      {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "prepare", "report"}:             {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "prepareforeign", "report"}:      {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"4f0f253f1d1b00417e83a74b1829f3191464caa7e88fc71e635c8eb3a83a44f2.json.gz", "managed", "preparego", "report"}:           {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "as-is", "analyze", "report"}:               {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "202a0872e12ca777e1744241", "b959e3a52b05cfd64e26cec8"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "6619876bb9d24b2944822a7d", "b86f2ccc9233845cdd933bfc"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "as-is", "emit", "report"}:                  {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "6619876bb9d24b2944822a7d", "b86f2ccc9233845cdd933bfc"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "as-is", "emittemplate", "report"}:          {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "as-is", "lower", "report"}:                 {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "202a0872e12ca777e1744241", "b959e3a52b05cfd64e26cec8"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "as-is", "lower.template", "report"}:        {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "as-is", "prepare", "report"}:               {resultRegisterLast, "120360c10bdaceeb2abc293e", "b2b6d26462dd547130371b10"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "analyze", "report"}:                  {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "assembly", "listing"}:                {resultRegisterLast, "202a0872e12ca777e1744241", "b959e3a52b05cfd64e26cec8"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "emit", "bytes"}:                      {resultRegisterLast, "6619876bb9d24b2944822a7d", "b86f2ccc9233845cdd933bfc"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "emit", "report"}:                     {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "emittemplate", "linkat"}:             {resultRegisterLast, "6619876bb9d24b2944822a7d", "b86f2ccc9233845cdd933bfc"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "emittemplate", "report"}:             {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "lower", "report"}:                    {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "lower.listing", "listing"}:           {resultRegisterLast, "202a0872e12ca777e1744241", "b959e3a52b05cfd64e26cec8"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "lower.template", "report"}:           {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "prepare", "report"}:                  {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "go", "preparego", "report"}:                {resultRegisterLast, "5a5edde246d8136daba7a843", "d79316656cba02aa2e6f48c8"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "analyze", "report"}:             {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "assembly", "listing"}:           {resultRegisterLast, "d864ecdad348397fd6e7026d", "33275c222ec390bc7ede8ada"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "emit", "bytes"}:                 {resultRegisterLast, "fbfcdb82f2a8f5f41bebebba", "6873edc93ceff134fb0780af"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "emit", "report"}:                {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "emittemplate", "linkat"}:        {resultRegisterLast, "fbfcdb82f2a8f5f41bebebba", "6873edc93ceff134fb0780af"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "emittemplate", "report"}:        {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "lower", "report"}:               {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "lower.listing", "listing"}:      {resultRegisterLast, "d864ecdad348397fd6e7026d", "33275c222ec390bc7ede8ada"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "lower.template", "report"}:      {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "prepare", "report"}:             {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "prepareforeign", "report"}:      {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"7e95f0467b4d6c0732a57965fed82206fd73d3461872f1601872d16a11b8132e.json.gz", "managed", "preparego", "report"}:           {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "as-is", "analyze", "report"}:               {resultRegisterLast, "63c8447560677889c5dcf7b6", "cbe638c319c44a762a83b132"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "7f8892c9d7666c7d62cef46c", "5aac0fa999dd5655682b3337"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "2f8a46b57a099f10919e8d64", "a38fe89011af4cc87e4b681b"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "as-is", "emit", "report"}:                  {resultRegisterLast, "63c8447560677889c5dcf7b6", "cbe638c319c44a762a83b132"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "2f8a46b57a099f10919e8d64", "a38fe89011af4cc87e4b681b"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "as-is", "emittemplate", "report"}:          {resultRegisterLast, "63c8447560677889c5dcf7b6", "cbe638c319c44a762a83b132"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "as-is", "lower", "report"}:                 {resultRegisterLast, "63c8447560677889c5dcf7b6", "cbe638c319c44a762a83b132"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "7f8892c9d7666c7d62cef46c", "5aac0fa999dd5655682b3337"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "as-is", "lower.template", "report"}:        {resultRegisterLast, "63c8447560677889c5dcf7b6", "cbe638c319c44a762a83b132"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "as-is", "prepare", "report"}:               {resultRegisterLast, "63c8447560677889c5dcf7b6", "cbe638c319c44a762a83b132"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "analyze", "report"}:                  {resultRegisterLast, "d787239188d7b4866cb7957f", "44511c9a6dc0ce2e70c15894"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "assembly", "listing"}:                {resultRegisterLast, "7f8892c9d7666c7d62cef46c", "5aac0fa999dd5655682b3337"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "emit", "bytes"}:                      {resultRegisterLast, "2f8a46b57a099f10919e8d64", "a38fe89011af4cc87e4b681b"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "emit", "report"}:                     {resultRegisterLast, "d787239188d7b4866cb7957f", "44511c9a6dc0ce2e70c15894"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "emittemplate", "linkat"}:             {resultRegisterLast, "2f8a46b57a099f10919e8d64", "a38fe89011af4cc87e4b681b"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "emittemplate", "report"}:             {resultRegisterLast, "d787239188d7b4866cb7957f", "44511c9a6dc0ce2e70c15894"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "lower", "report"}:                    {resultRegisterLast, "d787239188d7b4866cb7957f", "44511c9a6dc0ce2e70c15894"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "lower.listing", "listing"}:           {resultRegisterLast, "7f8892c9d7666c7d62cef46c", "5aac0fa999dd5655682b3337"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "lower.template", "report"}:           {resultRegisterLast, "d787239188d7b4866cb7957f", "44511c9a6dc0ce2e70c15894"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "prepare", "report"}:                  {resultRegisterLast, "d787239188d7b4866cb7957f", "44511c9a6dc0ce2e70c15894"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "go", "preparego", "report"}:                {resultRegisterLast, "9a3cfc4d8a9128dc7785e2b2", "a81def44413e89af58ff6d38"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "analyze", "report"}:             {resultRegisterLast, "0448c37aa1dc5600c22dcea2", "52e22523191663ac4e1de931"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "assembly", "listing"}:           {resultRegisterLast, "51813a46c24f3d0326ec4f90", "c998eb99b7b4911011e6e761"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "emit", "bytes"}:                 {resultRegisterLast, "19a79f50b8c0c34a68f9a192", "3c570c42ea141a16a5809659"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "emit", "report"}:                {resultRegisterLast, "0448c37aa1dc5600c22dcea2", "52e22523191663ac4e1de931"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "emittemplate", "linkat"}:        {resultRegisterLast, "19a79f50b8c0c34a68f9a192", "3c570c42ea141a16a5809659"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "emittemplate", "report"}:        {resultRegisterLast, "0448c37aa1dc5600c22dcea2", "52e22523191663ac4e1de931"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "lower", "report"}:               {resultRegisterLast, "0448c37aa1dc5600c22dcea2", "52e22523191663ac4e1de931"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "lower.listing", "listing"}:      {resultRegisterLast, "51813a46c24f3d0326ec4f90", "c998eb99b7b4911011e6e761"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "lower.template", "report"}:      {resultRegisterLast, "0448c37aa1dc5600c22dcea2", "52e22523191663ac4e1de931"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "prepare", "report"}:             {resultRegisterLast, "ab41ee20374f169a7400120d", "c2f6bca8451dc578fcf141f9"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "prepareforeign", "report"}:      {resultRegisterLast, "ab41ee20374f169a7400120d", "c2f6bca8451dc578fcf141f9"},
	{"9b0c9bd3a19fdbe6b7a958514a831b352dfb7c2928604aca4e1ad1d3c8e809bd.json.gz", "managed", "preparego", "report"}:           {resultRegisterLast, "0448c37aa1dc5600c22dcea2", "52e22523191663ac4e1de931"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "arch:loong64", "assembly", "listing"}:      {resultRegisterLast, "b96129ab07616a571e5a9369", "0a3510a3cb850b1d24b0a2c8"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "arch:loong64", "lower.listing", "listing"}: {resultRegisterLast, "b96129ab07616a571e5a9369", "0a3510a3cb850b1d24b0a2c8"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "arch:ppc64le", "assembly", "listing"}:      {resultRegisterLast, "07bc5e40d9cc8cb676b75ec5", "e7d753a29af6879d5ee37ca8"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "arch:ppc64le", "lower.listing", "listing"}: {resultRegisterLast, "07bc5e40d9cc8cb676b75ec5", "e7d753a29af6879d5ee37ca8"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "arch:riscv64", "assembly", "listing"}:      {resultRegisterLast, "e901b86ea851893c2ae10a2d", "d48bb5c657aff51f587010ff"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "arch:riscv64", "lower.listing", "listing"}: {resultRegisterLast, "e901b86ea851893c2ae10a2d", "d48bb5c657aff51f587010ff"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "c255efc7e3be6c50cda376d2", "a9a9273d11dd003ef6f42379"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "7bb33bb55c2c305ff26b7211", "15b0d7972b84c1974efb7540"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "7bb33bb55c2c305ff26b7211", "15b0d7972b84c1974efb7540"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "c255efc7e3be6c50cda376d2", "a9a9273d11dd003ef6f42379"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "go", "assembly", "listing"}:                {resultRegisterLast, "c255efc7e3be6c50cda376d2", "a9a9273d11dd003ef6f42379"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "go", "emit", "bytes"}:                      {resultRegisterLast, "7bb33bb55c2c305ff26b7211", "15b0d7972b84c1974efb7540"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "go", "emittemplate", "linkat"}:             {resultRegisterLast, "7bb33bb55c2c305ff26b7211", "15b0d7972b84c1974efb7540"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "go", "lower.listing", "listing"}:           {resultRegisterLast, "c255efc7e3be6c50cda376d2", "a9a9273d11dd003ef6f42379"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "managed", "assembly", "listing"}:           {resultRegisterLast, "741a4bb3c81d34130201affa", "34b99d603ea1d27486c46158"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "managed", "emit", "bytes"}:                 {resultRegisterLast, "a08537b437be88fe80b50ade", "a3ea6aeb256ec36f4d28e3aa"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "managed", "emittemplate", "linkat"}:        {resultRegisterLast, "a08537b437be88fe80b50ade", "a3ea6aeb256ec36f4d28e3aa"},
	{"9f179c620c69908d36aee2e84a4dcc125607653d1ae23f94bf4ab14ec0629af5.json.gz", "managed", "lower.listing", "listing"}:      {resultRegisterLast, "741a4bb3c81d34130201affa", "34b99d603ea1d27486c46158"},
	{"b4edd49b2a8f2cbdcbb74c2941fa1fcfbf1c09fc4e990bb6e9b80f3e9e5988b5.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "4d58c62b1d0c38ac7f7f869b", "41e08a26ea7fab014521e1b9"},
	{"b4edd49b2a8f2cbdcbb74c2941fa1fcfbf1c09fc4e990bb6e9b80f3e9e5988b5.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "3727daf35957061565262f4c", "08c62363c9b58d9ba07b000c"},
	{"b4edd49b2a8f2cbdcbb74c2941fa1fcfbf1c09fc4e990bb6e9b80f3e9e5988b5.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "3727daf35957061565262f4c", "08c62363c9b58d9ba07b000c"},
	{"b4edd49b2a8f2cbdcbb74c2941fa1fcfbf1c09fc4e990bb6e9b80f3e9e5988b5.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "4d58c62b1d0c38ac7f7f869b", "41e08a26ea7fab014521e1b9"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "analyze", "report"}:               {resultRegisterLast, "e8a56cf110106ddcc5b43902", "3fd151a44843344e05fbb9ff"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "5cd08732abf08f4a4865c8bc", "b0e444bab72ddcb0da1bdb5d"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "e75844e6ac9a2ad7f8dc20f7", "317dca1131466ea135ce69d5"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "emit", "report"}:                  {resultRegisterLast, "e8a56cf110106ddcc5b43902", "3fd151a44843344e05fbb9ff"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "e75844e6ac9a2ad7f8dc20f7", "317dca1131466ea135ce69d5"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "emittemplate", "report"}:          {resultRegisterLast, "e8a56cf110106ddcc5b43902", "3fd151a44843344e05fbb9ff"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "lower", "report"}:                 {resultRegisterLast, "e8a56cf110106ddcc5b43902", "3fd151a44843344e05fbb9ff"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "5cd08732abf08f4a4865c8bc", "b0e444bab72ddcb0da1bdb5d"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "lower.template", "report"}:        {resultRegisterLast, "e8a56cf110106ddcc5b43902", "3fd151a44843344e05fbb9ff"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "prepare", "report"}:               {resultRegisterLast, "e8a56cf110106ddcc5b43902", "3fd151a44843344e05fbb9ff"},
	{"c1e97a88e754c67bf60a2c7a3815dd21dccab7dd08a22d7378f457be280b00ad.json.gz", "as-is", "prepareforeign", "report"}:        {resultRegisterLast, "e8a56cf110106ddcc5b43902", "3fd151a44843344e05fbb9ff"},
	{"c5dc5c91fb804b4487b1a1bedcefd09b2075846ebc9ca58720ec04bca5bf810e.json.gz", "as-is", "analyze", "report"}:               {resultRegisterLast, "1a67e8f258e6dbb4913ce91f", "4dde6cfe29008c00ce112cca"},
	{"c5dc5c91fb804b4487b1a1bedcefd09b2075846ebc9ca58720ec04bca5bf810e.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "5cd08732abf08f4a4865c8bc", "b0e444bab72ddcb0da1bdb5d"},
	{"c5dc5c91fb804b4487b1a1bedcefd09b2075846ebc9ca58720ec04bca5bf810e.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "e75844e6ac9a2ad7f8dc20f7", "317dca1131466ea135ce69d5"},
	{"c5dc5c91fb804b4487b1a1bedcefd09b2075846ebc9ca58720ec04bca5bf810e.json.gz", "as-is", "emit", "report"}:                  {resultRegisterLast, "1a67e8f258e6dbb4913ce91f", "4dde6cfe29008c00ce112cca"},
	{"c5dc5c91fb804b4487b1a1bedcefd09b2075846ebc9ca58720ec04bca5bf810e.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "e75844e6ac9a2ad7f8dc20f7", "317dca1131466ea135ce69d5"},
	{"c5dc5c91fb804b4487b1a1bedcefd09b2075846ebc9ca58720ec04bca5bf810e.json.gz", "as-is", "emittemplate", "report"}:          {resultRegisterLast, "1a67e8f258e6dbb4913ce91f", "4dde6cfe29008c00ce112cca"},
	{"c5dc5c91fb804b4487b1a1bedcefd09b2075846ebc9ca58720ec04bca5bf810e.json.gz", "as-is", "lower", "report"}:                 {resultRegisterLast, "1a67e8f258e6dbb4913ce91f", "4dde6cfe29008c00ce112cca"},
	{"c5dc5c91fb804b4487b1a1bedcefd09b2075846ebc9ca58720ec04bca5bf810e.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "5cd08732abf08f4a4865c8bc", "b0e444bab72ddcb0da1bdb5d"},
	{"c5dc5c91fb804b4487b1a1bedcefd09b2075846ebc9ca58720ec04bca5bf810e.json.gz", "as-is", "lower.template", "report"}:        {resultRegisterLast, "1a67e8f258e6dbb4913ce91f", "4dde6cfe29008c00ce112cca"},
	{"c5dc5c91fb804b4487b1a1bedcefd09b2075846ebc9ca58720ec04bca5bf810e.json.gz", "as-is", "prepare", "report"}:               {resultRegisterLast, "1a67e8f258e6dbb4913ce91f", "4dde6cfe29008c00ce112cca"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "analyze", "report"}:               {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "1492ff8d61ea0241315a335f", "098a6c399f242f24fe56d196"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "e858f8e8baa6d9f8005fa2d5", "cf61a5b610cca0cf854b734d"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "emit", "report"}:                  {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "e858f8e8baa6d9f8005fa2d5", "cf61a5b610cca0cf854b734d"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "emittemplate", "report"}:          {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "lower", "report"}:                 {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "1492ff8d61ea0241315a335f", "098a6c399f242f24fe56d196"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "lower.template", "report"}:        {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "prepare", "report"}:               {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "as-is", "prepareforeign", "report"}:        {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "analyze", "report"}:             {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "assembly", "listing"}:           {resultRegisterLast, "1492ff8d61ea0241315a335f", "098a6c399f242f24fe56d196"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "emit", "bytes"}:                 {resultRegisterLast, "e858f8e8baa6d9f8005fa2d5", "cf61a5b610cca0cf854b734d"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "emit", "report"}:                {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "emittemplate", "linkat"}:        {resultRegisterLast, "e858f8e8baa6d9f8005fa2d5", "cf61a5b610cca0cf854b734d"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "emittemplate", "report"}:        {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "lower", "report"}:               {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "lower.listing", "listing"}:      {resultRegisterLast, "1492ff8d61ea0241315a335f", "098a6c399f242f24fe56d196"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "lower.template", "report"}:      {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "prepare", "report"}:             {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "compact", "prepareforeign", "report"}:      {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "lsx", "assembly", "listing"}:               {resultRegisterLast, "46f7465c2ea86a9e0f5804d3", "593a2d29e90e7344ea8fd5aa"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "lsx", "lower", "report"}:                   {resultRegisterLast, "1d0452791385ed35a10dbe9e", "9a2c007de4a37938b196a79b"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "lsx", "lower.listing", "listing"}:          {resultRegisterLast, "46f7465c2ea86a9e0f5804d3", "593a2d29e90e7344ea8fd5aa"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "power10", "assembly", "listing"}:           {resultRegisterLast, "c50b43a7cd21b4f16e46c81b", "e21d9a1923e17ab6d9ba8fc0"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "power10", "lower", "report"}:               {resultRegisterLast, "caf6d2f33626096ee05ea9c4", "29af2ed217de62a1de39a1a2"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "power10", "lower.listing", "listing"}:      {resultRegisterLast, "c50b43a7cd21b4f16e46c81b", "e21d9a1923e17ab6d9ba8fc0"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "ppc64cpu9", "assembly", "listing"}:         {resultRegisterLast, "c50b43a7cd21b4f16e46c81b", "e21d9a1923e17ab6d9ba8fc0"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "ppc64cpu9", "lower", "report"}:             {resultRegisterLast, "fb35c173a46ccb09546fa0ff", "b252404a4a10996af5e9d1ab"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "ppc64cpu9", "lower.listing", "listing"}:    {resultRegisterLast, "c50b43a7cd21b4f16e46c81b", "e21d9a1923e17ab6d9ba8fc0"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "analyze", "report"}:              {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "assembly", "listing"}:            {resultRegisterLast, "1492ff8d61ea0241315a335f", "098a6c399f242f24fe56d196"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "emit", "bytes"}:                  {resultRegisterLast, "e858f8e8baa6d9f8005fa2d5", "cf61a5b610cca0cf854b734d"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "emit", "report"}:                 {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "emittemplate", "linkat"}:         {resultRegisterLast, "e858f8e8baa6d9f8005fa2d5", "cf61a5b610cca0cf854b734d"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "emittemplate", "report"}:         {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "lower", "report"}:                {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "lower.listing", "listing"}:       {resultRegisterLast, "1492ff8d61ea0241315a335f", "098a6c399f242f24fe56d196"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "lower.template", "report"}:       {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "prepare", "report"}:              {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e0397f7b995b32812bbb34880e88ee3d388817dde279c91998064a57878b4a67.json.gz", "scalar", "prepareforeign", "report"}:       {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "as-is", "analyze", "report"}:               {resultRegisterLast, "d961ca1981b2c3f4b3b7b33d", "4bf8b80a830b798bf2b9870e"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "as-is", "analyze.constants", "report"}:     {resultRegisterLast, "06e0ef8a703b3b6ec2c85cde", "104f0dc3dcfeff34e9823993"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "b5396f1dd19dc857bc023fb0", "3c77d824a6de6bad9d156091"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "as-is", "assembly.constants", "listing"}:   {resultRegisterLast, "658f165dddeba0e3e3621f72", "73a678f83efecbb5d251bf9d"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "c9bb8f7b0bf73018d86e7633", "8680790ca0a0a9eac97d5e99"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "as-is", "emittemplate", "report"}:          {resultRegisterLast, "d961ca1981b2c3f4b3b7b33d", "4bf8b80a830b798bf2b9870e"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "as-is", "lower", "report"}:                 {resultRegisterLast, "d961ca1981b2c3f4b3b7b33d", "4bf8b80a830b798bf2b9870e"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "b5396f1dd19dc857bc023fb0", "3c77d824a6de6bad9d156091"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "as-is", "lower.template", "report"}:        {resultRegisterLast, "d961ca1981b2c3f4b3b7b33d", "4bf8b80a830b798bf2b9870e"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "as-is", "prepare", "report"}:               {resultRegisterLast, "d961ca1981b2c3f4b3b7b33d", "4bf8b80a830b798bf2b9870e"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "analyze", "report"}:                  {resultRegisterLast, "593dd9650582bd0c7e8d00b6", "7bc2b8bb42af113993bbdedf"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "analyze.constants", "report"}:        {resultRegisterLast, "6a80770164091cd55035f365", "68078ba894b3d3eae91a0a85"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "assembly", "listing"}:                {resultRegisterLast, "b5396f1dd19dc857bc023fb0", "3c77d824a6de6bad9d156091"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "assembly.constants", "listing"}:      {resultRegisterLast, "658f165dddeba0e3e3621f72", "73a678f83efecbb5d251bf9d"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "emittemplate", "linkat"}:             {resultRegisterLast, "c9bb8f7b0bf73018d86e7633", "8680790ca0a0a9eac97d5e99"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "emittemplate", "report"}:             {resultRegisterLast, "593dd9650582bd0c7e8d00b6", "7bc2b8bb42af113993bbdedf"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "lower", "report"}:                    {resultRegisterLast, "593dd9650582bd0c7e8d00b6", "7bc2b8bb42af113993bbdedf"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "lower.listing", "listing"}:           {resultRegisterLast, "b5396f1dd19dc857bc023fb0", "3c77d824a6de6bad9d156091"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "lower.template", "report"}:           {resultRegisterLast, "593dd9650582bd0c7e8d00b6", "7bc2b8bb42af113993bbdedf"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "prepare", "report"}:                  {resultRegisterLast, "593dd9650582bd0c7e8d00b6", "7bc2b8bb42af113993bbdedf"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "go", "preparego", "report"}:                {resultRegisterLast, "7684293c7d65cad32b8c38e9", "25f5ef71149999c977387815"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "analyze", "report"}:             {resultRegisterLast, "6d776b0fad646c1abdafd1dc", "791e50fde3008dde32e3f5c2"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "analyze.constants", "report"}:   {resultRegisterLast, "54742bab3676fa05786e7843", "5e09e2ffddb26d5cd65b9d90"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "assembly", "listing"}:           {resultRegisterLast, "efba5ad057d6671044da4643", "f0023f0339b9528c54ac5fee"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "assembly.constants", "listing"}: {resultRegisterLast, "92dedd5a20c1000c1862873a", "3861db356d7f583ddb6fc886"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "emittemplate", "linkat"}:        {resultRegisterLast, "dc2b7b08a721393fc4e419a7", "38d9936a1cce9b85a3dea33a"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "emittemplate", "report"}:        {resultRegisterLast, "6d776b0fad646c1abdafd1dc", "791e50fde3008dde32e3f5c2"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "lower", "report"}:               {resultRegisterLast, "6d776b0fad646c1abdafd1dc", "791e50fde3008dde32e3f5c2"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "lower.listing", "listing"}:      {resultRegisterLast, "efba5ad057d6671044da4643", "f0023f0339b9528c54ac5fee"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "lower.template", "report"}:      {resultRegisterLast, "6d776b0fad646c1abdafd1dc", "791e50fde3008dde32e3f5c2"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "prepare", "report"}:             {resultRegisterLast, "7666abbae4a9532afd83af33", "9aaed51469045c0f68f05a09"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "prepareforeign", "report"}:      {resultRegisterLast, "7666abbae4a9532afd83af33", "9aaed51469045c0f68f05a09"},
	{"e6333376885f729cf70956772cc382d970366b197a4f49858921ca1670879543.json.gz", "managed", "preparego", "report"}:           {resultRegisterLast, "6d776b0fad646c1abdafd1dc", "791e50fde3008dde32e3f5c2"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "analyze", "report"}:               {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "assembly", "listing"}:             {resultRegisterLast, "19aec262a0a08bba22201a4e", "00e65f67d1d5b5ed9814ac55"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "emit", "bytes"}:                   {resultRegisterLast, "af5d1d974369fe7e03b2c606", "5b6d1243d4e3ef29dd922252"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "emit", "report"}:                  {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "emittemplate", "linkat"}:          {resultRegisterLast, "af5d1d974369fe7e03b2c606", "5b6d1243d4e3ef29dd922252"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "emittemplate", "report"}:          {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "lower", "report"}:                 {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "lower.listing", "listing"}:        {resultRegisterLast, "19aec262a0a08bba22201a4e", "00e65f67d1d5b5ed9814ac55"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "lower.template", "report"}:        {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "prepare", "report"}:               {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "as-is", "prepareforeign", "report"}:        {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "analyze", "report"}:                  {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "assembly", "listing"}:                {resultRegisterLast, "19aec262a0a08bba22201a4e", "00e65f67d1d5b5ed9814ac55"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "emit", "bytes"}:                      {resultRegisterLast, "af5d1d974369fe7e03b2c606", "5b6d1243d4e3ef29dd922252"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "emit", "report"}:                     {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "emittemplate", "linkat"}:             {resultRegisterLast, "af5d1d974369fe7e03b2c606", "5b6d1243d4e3ef29dd922252"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "emittemplate", "report"}:             {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "lower", "report"}:                    {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "lower.listing", "listing"}:           {resultRegisterLast, "19aec262a0a08bba22201a4e", "00e65f67d1d5b5ed9814ac55"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "lower.template", "report"}:           {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "prepare", "report"}:                  {resultRegisterLast, "bc82ed7e4afb246cd2933f58", "01ec12a9bc533f319c87d4ad"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "go", "preparego", "report"}:                {resultRegisterLast, "5a5edde246d8136daba7a843", "d79316656cba02aa2e6f48c8"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "analyze", "report"}:             {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "assembly", "listing"}:           {resultRegisterLast, "deb09941da1d3771ea191663", "d6d70bcc39206c1d3704d131"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "emit", "bytes"}:                 {resultRegisterLast, "b008ba97ce49d0da22cd6a72", "1ab901bea6e3320dfa9ef748"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "emit", "report"}:                {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "emittemplate", "linkat"}:        {resultRegisterLast, "b008ba97ce49d0da22cd6a72", "1ab901bea6e3320dfa9ef748"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "emittemplate", "report"}:        {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "lower", "report"}:               {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "lower.listing", "listing"}:      {resultRegisterLast, "deb09941da1d3771ea191663", "d6d70bcc39206c1d3704d131"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "lower.template", "report"}:      {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "prepare", "report"}:             {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "prepareforeign", "report"}:      {resultRegisterLast, "614f775059cb4bf18230f98d", "af18c2182635ee60ad31b455"},
	{"e657675c7ce841b17e9dda1b815635f72e89fe961b80570304bf043947f95cef.json.gz", "managed", "preparego", "report"}:           {resultRegisterLast, "b9f5ccb62cde7fa1972bb96e", "203271af0ef8b12331973cd6"},
}

// reviewedDifference reports whether key of entry r changed exactly as reviewed.
func reviewedDifference(r apiGolden, key, got string) (string, bool) {
	change, ok := reviewedChanges[reviewedOutput{r.Fixture, r.Variant, r.Entry, key}]
	if !ok || change.before != r.Out[key] || change.after != got {
		return "", false
	}
	return change.reason, true
}

// goldenSummary is the outcome of replaying a golden record.
type goldenSummary struct {
	total, differing int
	mismatches       []string
	byEntry          map[string]int
	intended         map[string]int
	reviewed         map[string]int
	seen             map[reviewedOutput]bool
}

// replayGolden replays every entry of the golden record against the corpus
// programs in directory and classifies each difference.
func replayGolden(directory, golden string) (goldenSummary, error) {
	summary := goldenSummary{byEntry: map[string]int{}, intended: map[string]int{}, reviewed: map[string]int{}, seen: map[reviewedOutput]bool{}}
	in, err := os.Open(golden)
	if err != nil {
		return summary, err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return summary, err
	}
	var current string
	var program *ir.Program
	scanner := bufio.NewScanner(zr)
	scanner.Buffer(make([]byte, 1<<20), 1<<26)
	for scanner.Scan() {
		var r apiGolden
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			return summary, err
		}
		if r.Fixture != current {
			program, err = loadCorpusProgram(filepath.Join(directory, r.Fixture))
			if err != nil {
				return summary, err
			}
			current = r.Fixture
		}
		summary.total++
		out, err := replayAPIEntry(r, program, false)
		problems := goldenProblems(r, program, out, err, &summary)
		architecture := r.Options.Architecture
		if architecture == "" {
			architecture = runtime.GOARCH
		}
		if reason, ok := deadFlagReasons[architecture]; ok && len(problems) != 0 {
			// The record predates dead-flag elision. Lowering without it must
			// reproduce the record, or its hand-reviewed changes, exactly.
			out, err := replayAPIEntry(r, program, true)
			retry := goldenSummary{reviewed: map[string]int{}, seen: map[reviewedOutput]bool{}}
			if len(goldenProblems(r, program, out, err, &retry)) == 0 {
				summary.reviewed[reason]++
				for k, n := range retry.reviewed {
					summary.reviewed[k] += n
				}
				for k := range retry.seen {
					summary.seen[k] = true
				}
				continue
			}
		}
		if len(problems) == 0 {
			continue
		}
		if reason, ok := intendedDifference(r, err); ok {
			summary.intended[reason]++
			continue
		}
		summary.differing++
		summary.byEntry[r.Entry+"|"+r.Variant]++
		summary.mismatches = append(summary.mismatches, fmt.Sprintf("%s %s %s: %s", r.Fixture, r.Variant, r.Entry, strings.Join(problems, "; ")))
	}
	return summary, scanner.Err()
}

// goldenProblems compares one replayed entry with its record. With a
// summary, hand-reviewed output changes are accepted and counted.
func goldenProblems(r apiGolden, program *ir.Program, out map[string]string, err error, summary *goldenSummary) []string {
	got := strings.Join(apiSentinelNames(err), ",")
	want := strings.Join(r.Is, ",")
	var problems []string
	if (err == nil) != (r.Err == "") || got != want {
		problems = append(problems, fmt.Sprintf("error %q [%s], want %q [%s]", errString(err), got, r.Err, want))
	} else if err == nil {
		keys := make([]string, 0, len(r.Out))
		for k := range r.Out {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "bytes" && r.Entry == "emittemplate" {
				continue // unlinked bytes are no longer exposed; linkat covers them
			}
			// Calls embed host gateway and callee addresses, and built-in
			// tables link to this binary's addresses; both move between test
			// binaries, so compare their errors and relocations only.
			if (k == "listing" && r.Entry == "lower.listing" || k == "bytes" || k == "linkat") && (hasFunctionCalls(program) || out["host"] != "") {
				continue
			}
			if out[k] == r.Out[k] {
				continue
			}
			if reason, ok := reviewedDifference(r, k, out[k]); ok && summary != nil {
				summary.reviewed[reason]++
				summary.seen[reviewedOutput{r.Fixture, r.Variant, r.Entry, k}] = true
				continue
			}
			problems = append(problems, fmt.Sprintf("%s differs (%s -> %s)", k, r.Out[k], out[k]))
		}
	}
	return problems
}

// TestAPIEquivalence replays outputs recorded from the entry points that
// TargetSpec, Func and Unit replaced, over the frozen listing corpus.
func TestAPIEquivalence(t *testing.T) {
	directory, golden := os.Getenv("NYANJIT_LISTING_CORPUS"), os.Getenv("NYANJIT_API_GOLDEN")
	if directory == "" || golden == "" {
		t.Skip("set NYANJIT_LISTING_CORPUS and NYANJIT_API_GOLDEN to replay the API golden record")
	}
	summary, err := replayGolden(directory, golden)
	if err != nil {
		t.Fatal(err)
	}
	if report := os.Getenv("NYANJIT_API_GOLDEN_REPORT"); report != "" {
		if err := os.WriteFile(report, []byte(strings.Join(summary.mismatches, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keys := make([]string, 0, len(summary.byEntry))
	for k := range summary.byEntry {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		t.Logf("%s: %d differing", k, summary.byEntry[k])
	}
	for reason, n := range summary.intended {
		t.Logf("intended: %s: %d", reason, n)
	}
	for reason, n := range summary.reviewed {
		t.Logf("reviewed: %s: %d", reason, n)
	}
	for output := range reviewedChanges {
		if !summary.seen[output] {
			t.Errorf("reviewed change %+v was not observed; remove it or update its hashes", output)
		}
	}
	t.Logf("replayed %d golden entries, %d differ", summary.total, summary.differing)
	if summary.differing != 0 {
		t.Errorf("%d golden entries differ; first: %s", summary.differing, summary.mismatches[0])
	}
}

// markHostAddresses flags templates whose linking resolves built-in tables
// (such as the math constants) to this binary's addresses, which move when
// the test binary changes.
func markHostAddresses(out map[string]string, t *execution.Template) {
	for _, r := range t.Relocations() {
		if strings.HasPrefix(r.Symbol, "__nyanjit_") {
			out["host"] = "1"
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func loadCorpusProgram(name string) (*ir.Program, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	var fixture struct{ Program *ir.Program }
	if err := json.NewDecoder(gz).Decode(&fixture); err != nil {
		return nil, err
	}
	return fixture.Program, nil
}
