# Sealed programs

`Builder.Finish` returns `*ir.SealedProgram`. It owns an immutable, validated IR snapshot and architecture-independent analyses: instruction regions, scope bounds/local declarations, loops, use positions, floating homes/label parameters, and entry parameter widths. Repeated compilation reuses this storage without cloning or validating it again: the compiler reads it through the sealed program's accessors. Target legalization creates its own validated representation when needed; selection, allocation, physical layout and output caches belong to each `compile.Unit`.

`ir.Program` remains the mutable construction and inspection form. `compile.Lower` accepts it through `ir.Input`; the adapter calls `ir.Seal`, detaching and validating a fresh snapshot on each call. To reuse hand-built IR, seal it explicitly:

```go
sealed, err := ir.Seal(raw)
if err != nil { return err }
u, err := compile.Lower(target, sealed, compile.Func{ABI: abi.C})
```

A sealed program exposes read-only accessors that borrow its storage (`Len`, `Arity`, `Result`, `Instructions`, `Variables`, `ScopeExits`, `Control`, `ResultParts`, `ParameterTypes`), its `Analysis()`, and `Clone()`. Never modify borrowed storage: compilers trust the validation done at sealing. Clone returns fully independent mutable IR, including nested argument/effect slices, literal data and control predicates. Changes to the raw input or a clone cannot change an existing sealed program. To change a function, edit a clone and call Seal again. Zero-value and nil sealed inputs fail compilation safely.

Code that needs raw fields, or wants to edit a program, works on an independent `Clone()` and seals the result again. A function that only forwards programs to the compiler takes `ir.Input`, which accepts both `*ir.SealedProgram` and a raw `*ir.Program`. Examples use sealed inputs; validation and selection tests request editable clones explicitly.

## Ownership choice

The handle is opaque rather than a validated flag on `ir.Program`. Public slices, nested effects and predicate pointers permit edits that Go cannot automatically invalidate. Trusting a flag would risk stale validation or stale analyses. A detached immutable snapshot gives a precise ownership boundary and allows safe concurrent compilation with different options.

`SealedProgram` is a struct defined in `ir`, so its public identity, reflection name and documentation belong to that package. Every package, the compiler included, reaches sealed storage through the same public read-only accessors; there is no private bridge. `ir.SealOwned` is the only way to seal without copying, and it always validates. `ir.Analyze` analyzes a program its caller owns and keeps valid; the compiler uses it for programs it builds during legalization. There is no global program identity/hash cache. A shared `compile.Target` reuses
private scratch buffers through a pool; each lowering leases its own buffers,
and each Unit owns the storage needed by its lazy outputs.

Finish rewrites scope entries first, then seals the result with `ir.SealOwned`, so every program is validated exactly once. The rewrite runs before validation; a program malformed enough to break its indexing is reported as an invalid program, never a panic. Public Seal always clones and validates its untrusted raw input. Compile-time constants (`compile.Func.Constants`) edit one owned copy, which is sealed and validated again.

`compile.Lower` can borrow a sealed graph and its facts. Each compilation owns target-specific home plans, selected values, allocation state and physical operations. `Unit.Report`, `Listing` and `Template` do not modify the sealed source; outputs and separate compilations can be requested concurrently.

Successful cross-target `Lower` units have exact reports and listings. Go-managed outbound C calls need concrete gateways from the target process, so cross-target `Listing` returns `ErrUnsupported` for them; `ListingSymbolic` renders symbolic runtime gateways on every host, and equals `Listing` for programs without managed C calls.
