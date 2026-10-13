# Compilation tiers

NyanJIT provides two compilation tiers behind one Builder API. Choose `compile.TargetSpec.Tier`; its zero value is `compile.TierOptimized`.

| Tier | Use |
|------|-----|
| `compile.TierOptimized` | More compilation work to improve generated code. |
| `compile.TierBaseline` | Faster compilation with different allocation and code-selection tradeoffs. |

Both tiers support amd64, arm64, 386, riscv64, loong64 and ppc64le. They share value, scope, Var and operation semantics. Generated code and resource use can differ; a valid program can hit a tier-specific backend limit. See the [README performance tables](../README.md#performance) for measurements.

## Selecting a tier

Bind a Builder to a target and function options, then call Lower:

```go
target, err := compile.NewTarget(compile.TargetSpec{
    Tier: compile.TierBaseline,
})
if err != nil {
    return err
}
b := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.C})
x := b.Param()
b.Return(b.Add(x, b.Const(1)))
unit, err := b.Lower()
```

Targets can be shared across independent lowerings; Builders and Streams hold one function's state and are not safe for concurrent use. NewBuilderFor ends with Lower; Finish on it returns ErrInvalidFunction. An unbound NewBuilder ends with Finish; Lower on it is invalid.

To compile one function at multiple tiers or targets, use NewBuilder and Finish to obtain a sealed program, then `compile.Lower(target, program, functionOptions)` for each target. Sealed programs permit concurrent compilation; see [sealed programs](sealed-programs.md).

## Shared control-flow rules

Builder labels take no arguments. `Branch(predicate, label)` has a fallthrough path; follow it with Jump for a two-way branch. Values are usable in blocks their definitions dominate, within their lexical scope. Initialized Vars carry path-dependent values and loop state. Get returns a snapshot that later Set does not change. Declare Vars in the scope's entry sequence, before the first branch. See [values, blocks and scopes](virtual-register-design.md#values-blocks-and-scopes).

Raw IR front ends retain block parameters and edge arguments as specified in [the IR reference](ir.md). Selecting a tier does not change that contract.

## Segment front ends

Create `compile.NewStream(target, functionOptions)`, send `*ir.Segment` blocks to `Stream.Segment`, then finish with `Stream.Unit`. IDs are program-wide coordinates and the complete stream follows Program's type, scope, dominance and control-flow rules. `builder.NewSegmentBuilder(stream.Segment)` provides Builder recording; call End before requesting the Stream's Unit.

Segment storage may be reused after Segment returns. The compiler retains neither the Segment nor its borrowed buffers; the Finalizers list is shared with the resulting program's Codes. Errors are sticky. Unit consumes the Stream; a second call returns ErrInvalidFunction. Consult Godoc for the current Segment fields.

## Results and limits

Both tiers return a `compile.Unit` with Report, Listing and Template operations. Reports describe actual code; byte identity between tiers is not promised. Baseline RegisterScopes returns ErrUnsupported. Managed cross-target listings can require ListingSymbolic because concrete runtime gateways belong to the target process.

Both tiers support ContextGoManaged and the same [hardening guarantees](optional-hardening-design.md), subject to target restrictions. Baseline vectors/masks use scalar legalization. Hardened control flow is unsupported on riscv64/loong64; 386 lacks FMA and 128-bit results. A baseline Stream rejects raw vector/mask label parameters. Frame requirements differ and must satisfy execution limits at Template/Bind.

Misuse returns ErrInvalidFunction, unsupported functionality ErrUnsupported, and resource limits ErrBackendLimit with a LimitError for numeric limits. ValidationError uses program-wide instruction coordinates. Cross-block checks complete at End, Finish, Lower or Stream.Unit as appropriate.

## Recompiling at another tier

There is no automatic tier-up or execution-state transfer. Re-run a generator with a different target, or lower the same sealed program again, then bind its Template. Manage the Codes independently. `execution.Arena.BindAll` batches bindings while preserving strict writable-to-executable transitions.
