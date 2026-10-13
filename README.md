# NyanJIT

[![CI](https://github.com/moriyoshi/nyanjit/actions/workflows/ci.yml/badge.svg)](https://github.com/moriyoshi/nyanjit/actions/workflows/ci.yml)
[![Linux E2E](https://github.com/moriyoshi/nyanjit/actions/workflows/e2e-linux.yml/badge.svg)](https://github.com/moriyoshi/nyanjit/actions/workflows/e2e-linux.yml)

<img src="assets/nyanjit-logo-29x23-large.png" width="324" />

## What is NyanJIT

NyanJIT builds, compiles and runs native integer, floating point and SIMD code from Go without cgo. You describe a function with the `nyanjit.Builder`, or emit NyanJIT IR directly from your own front end. NyanJIT selects instructions, allocates registers, lays out and encodes the code, and gives you a callable function, an assembly listing or relocatable bytes.

Linux/amd64 and Linux/arm64 are the primary targets. Linux/386 (i686 + SSE2), Linux/riscv64, Linux/ppc64le (POWER8+, ELFv2) and Linux/loong64 (LA64 v1.0, optional LSX) have experimental backends with floating point arithmetic; see [loong64 status](docs/loong64-completion.json). The modules target Go 1.26 and support Go 1.27 on the managed-call path. Big-endian Linux/ppc64 is not supported: it builds, but reports the unsupported error like other unsupported platforms.

## Design

NyanJIT is meant to be embedded: as a JIT in an interpreter, query engine, emulator or codec, and as the native back end of other IR-to-native projects. The public API follows from that.

- **Pure Go, explicit effects.** There is no cgo and no patched toolchain. Generated code calls C, Go and system calls directly, and every call carries an explicit description of the registers, memory and stack it touches. Managed frames are registered with the Go runtime, so Go callees can grow the stack, run the GC and unwind panics.
- **An immutable program is the unit of work.** `Finish` (or `ir.Seal`) validates a program once and returns an immutable `*ir.SealedProgram` together with its analyses. Every later step can trust it without copying or validating it again, and the same program can be compiled concurrently for different targets.
- **Lower once, take what you need.** `compile.Lower` selects, allocates and lays out a function once. The report, the listing and the encoded template are all lazy views of that one result. A listing describes exactly the bytes the template encodes, and asking for a report never encodes anything.
- **A shared, reentrant `Target`.** `compile.NewTarget` resolves the target once (architecture, CPU features, vector policy). A `compile.Target` holds the state that makes every lowering cheaper, such as a pool of reusable compilation buffers, and any number of goroutines share one. Without `Harden`, the same program and function settings give identical output regardless of concurrency or compilation history. `Harden` deliberately randomizes each lowering.
- **No SSA construction for the client, SSA-friendly inside.** Values are defined once and are usable in every block their definition dominates. In the Builder, values that differ by path merge through scoped `Var`s, so front ends never write phis. The optimized tier promotes most Vars to SSA itself; the baseline tier (`compile.TierBaseline`) keeps them in homes and builds no SSA at all. Raw IR (`ir.ProgramBuilder`) keeps SSA block parameters, so an SSA front end can map phis directly onto them.
- **Layered, public building blocks.** Everything shared between packages is a designed public API: there are no `internal/` packages. The lower layers, from ABIs and CPU profiles to encoders, the machine instruction stream, register allocation and executable memory, form a separate module, `nyanjit/backend`, that does not depend on NyanJIT's IR. Measurement and checking tools live under `instrument/`.
- **One name, one home.** Each type and constant is defined in exactly one package. The root package holds the Builder, its `Data` views and a one-shot `Compile`.

## Getting started

```sh
go get github.com/moriyoshi/nyanjit
```

```go
package main

import (
    "fmt"
    "log"

    "github.com/moriyoshi/nyanjit"
    "github.com/moriyoshi/nyanjit/backend/abi"
    "github.com/moriyoshi/nyanjit/compile"
)

func main() {
    b := nyanjit.NewBuilder()
    b.Return(b.Add(b.ParamNative(), b.ParamNative()))
    program, err := b.Finish()
    if err != nil { log.Fatal(err) }

    code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
    if err != nil { log.Fatal(err) }
    defer code.Close()

    result, err := code.Call(20, 22)
    if err != nil { log.Fatal(err) }
    fmt.Println(result) // 42
}
```

`nyanjit.Compile(program, spec, f)` lowers the program and binds it into executable memory. It uses the shared `Target` for the spec, so repeated calls benefit from the cached target state too.

### Examples

| Example | Run | Shows |
| --- | --- | --- |
| [add](examples/add/main.go) | `go run ./examples/add` | Builder, compilation, and `Code.Call`. |
| [sha256](examples/sha256/main.go) | `go run ./examples/sha256` | Four independent SHA-256 messages in portable SIMD lanes; native SSE2/NEON/LSX shifts and rotations. |
| [scopes](examples/scopes/main.go) | `go run ./examples/scopes` | `Enter`/`Leave`, temporary lifetimes, and exporting a value. |
| [aggregates](examples/aggregates) | `go run ./examples/aggregates` | Struct, array and slice parameters through `Data` views. |
| [irfrontend](examples/irfrontend/main.go) | `go run ./examples/irfrontend` | A bytecode front end emitting SSA-style IR with `ir.ProgramBuilder`. |
| [dctlogo](examples/dctlogo/main.go) | `go run ./examples/dctlogo` | Generated FP64 inverse DCT with square root, cosine, and nested loops. |
| [elfwrite](examples/elfwrite/main.go) | `go run ./examples/elfwrite /tmp/elfwrite` | A standalone Linux ELF and assembly listing. |
| [snake](examples/snake/main.go) | `go run ./examples/snake` | Game rules built with labels and predicates. |
| [snake_termbox](examples/snake_termbox/main.go) | `go -C examples/snake_termbox run .` | Interactive rendering and managed Go ABI calls (Linux amd64, arm64 and riscv64). |
| [stackvm](examples/stackvm/main.go) | `go -C examples/stackvm run .` | A stack bytecode compiled with only the backend module; `-S -arch riscv64` prints a listing for any target. |

`snake_termbox` and `stackvm` are separate modules: `termbox-go` stays out of the library module, and `stackvm` requires only the backend module.

## Comparison with other projects

| Project | Main focus | Difference here |
| --- | --- | --- |
| [gojit](https://github.com/nelhage/gojit) | Hack-day amd64 JIT toolkit: RWX memory, a minimal assembler, and a Brainfuck JIT | Callers write amd64 instructions themselves; there is no IR, instruction selection, or register allocation. Code goes into a read-write-execute `mmap` region. A Go assembly trampoline jumps into it on the goroutine stack, or a cgo path calls it with the C ABI. `BuildTo` hands the code a pointer to the Go argument frame. It is tested only on amd64. NyanJIT builds target-independent IR, allocates registers, emits for six architectures, and seals code read-execute before running it. |
| [aabalke/gojit](https://github.com/aabalke/gojit) | Revival of nelhage/gojit for Go 1.17+ on amd64; used by the [guac](https://github.com/aabalke/guac) GB/GBA/NDS emulator, whose author reports a 2–4× CPU speedup | Callers still emit amd64 machine code themselves. Code runs on the goroutine stack behind a Go assembly trampoline. `CallFunc` reaches Go by jumping to a `gocall` label inside that trampoline, so the garbage collector and stack growth see a valid Go frame. NyanJIT emits Go calls itself from target-independent IR on six architectures. Go-managed frames are registered with the runtime, so callees may grow the stack and run the GC. |
| [go-jit](https://github.com/goccy/go-jit) | cgo bindings to a vendored GNU libjit (archived, work in progress) | Typed builder calls map one-to-one onto libjit. libjit performs its own optimization, register allocation, and code generation for x86-64, x86, and ARM, with an interpreter fallback. Compiled functions run through cgo; Go callbacks re-enter Go through cgo `crosscall2`. NyanJIT is pure Go with no C toolchain. Its own selection and allocation passes take an inspectable IR snapshot, and calls carry explicit clobber and effect descriptions. |
| [jit-compiler](https://github.com/bspaans/jit-compiler) | Experimental compiler from a small Go/C-like language to x86-64 | Parsed source (integers, `float64`, arrays, structs, `if`/`while`, calls, syscalls) is lowered through its own IR and x86-64 encoder with the System V ABI. Its register allocator "works until you run out of registers", and there is no stack allocation. NyanJIT has no surface language. It spills under pressure and lowers one IR to six architectures. |
| [cel-go `jit`](https://pkg.go.dev/github.com/taichimaeda/cel-go/jit) | Native evaluation of boolean CEL expressions (a fork of cel-go) | A checked CEL AST is translated to three-address IR, given linear-scan register allocation, and rewritten with spill loads and stores. It is then emitted as amd64 code and called as `func(unsafe.Pointer) bool` on a typed activation struct. NyanJIT is a general-purpose builder with labeled control flow, scopes and variables, calls, and SIMD, and it targets six architectures. |
| [sonic](https://github.com/bytedance/sonic) | JSON encoder and decoder specialized per Go type | Codec programs are assembled for amd64 with [golang-asm](https://github.com/twitchyliquid64/golang-asm), the Go toolchain's assembler as a library. They are installed through sonic's `loader`, which registers code with function metadata and stack maps for arguments and locals in Go's module data. `Pretouch` hides first-use compilation latency. NyanJIT's `goregister` also registers frames with the runtime, but requires pointer-free values. Its code comes from instruction selection and register allocation over a general IR rather than a fixed codec template. |
| [memcp](https://github.com/launix-de/memcp) | In-memory SQL database whose Scheme procedures and query plans can be compiled to native code (experimental) | A generator (`jitgen`) produces shared emitters. Each amd64, arm64, or riscv64 backend performs instruction selection and register assignment, keeping persistent register homes across control-flow paths and Go-call boundaries. Code is written straight into executable memory. The JIT needs a patched Go toolchain (`GOEXPERIMENT=jit` with a `runtime/jit` fork for foreign frames); otherwise memcp interprets. NyanJIT builds with the stock toolchain. Its variable homes follow lexical scopes, and it compiles a caller-built IR rather than a Scheme dialect. |
| [musql](https://github.com/samyfodil/musql) | Pure-Go SQLite-compatible database with columnar storage and a JIT | `internal/jit` compiles SQLite-style VDBE bytecode and columnar scan filters to amd64 and arm64, with SSE/AVX2/NEON variants and its own register allocator. Execution enters native code at any program counter and falls back to the interpreter for instructions it cannot compile. NyanJIT is not tied to one bytecode. It compiles a whole program or rejects it with an error, and has no interpreter fallback. |
| [riscv-emu-golang](https://github.com/glycerine/riscv-emu-golang) | Pure-Go RISC-V (RV64GC) emulator with a dynamic binary translator | Guest RISC-V code is translated to amd64 or arm64 at runtime; the author reports about an 8× speedup over its interpreter. Its `abjit` trampoline follows aabalke/gojit to call Go without cgo. NyanJIT is a general code generator rather than a guest-to-host translator, and it lowers one IR to six host architectures. |
| [wazero](https://github.com/wazero/wazero) | WebAssembly runtime; its optimizing engine (wazevo) compiles whole modules before execution | wazevo translates Wasm into an SSA IR with block parameters and runs CFG passes. It lowers to amd64/arm64 and allocates registers with function-wide liveness analysis, placing spills at the common ancestor of reloads. Code runs on a wazero-managed stack and leaves through exit codes for host calls, memory growth, and stack growth. NyanJIT never builds SSA itself, but its IR is SSA-friendly: values are usable wherever their definition dominates, and raw IR merges through block parameters as in wazevo (Builder programs merge through Vars). It allocates per block, with edge arguments and scope-lifetime variable homes instead of global liveness. Go-ABI code runs on the goroutine stack, and managed frames are registered with the runtime. |
| [wago](https://github.com/wago-org/wago) | Pure-Go WebAssembly runtime with a single-pass compiler | Code generation and register allocation are fused into one forward pass over a symbolic value stack, following WARP's Valent-Block technique. Control-flow joins agree on frame slots, and there is no IR on the production path. Code runs on an off-heap execution stack, and synchronous host calls yield back to Go. NyanJIT keeps a reusable IR snapshot and separate selection, allocation, and emission passes across six architectures. Its code calls C, Go, and syscalls directly with explicit call effects. |
| [go-xla](https://github.com/gomlx/go-xla) | Go bindings for OpenXLA StableHLO and PJRT | Programs are tensor dataflow graphs built as StableHLO. A dynamically loaded PJRT plugin compiles them with XLA for a CPU, GPU, or TPU, and data moves through host and device buffers. NyanJIT compiles scalar and SIMD instruction streams to host machine code that runs in-process on Go memory. |

Related projects outside the table: [frugal](https://github.com/cloudwego/frugal) had a Thrift codec JIT, disabled by default in v0.2.4 and removed in v0.3.0. [goloader](https://github.com/eh-steve/goloader) compiles Go source with a patched compiler and links the objects into the running process; this is runtime loading, not instruction-level code generation. [wasmtime-go](https://github.com/bytecodealliance/wasmtime-go) and [wasmer-go](https://github.com/wasmerio/wasmer-go) reach Rust JIT backends (Cranelift; Singlepass, Cranelift, or LLVM) through cgo.

[purego](https://github.com/ebitengine/purego) is an FFI library that NyanJIT uses for its optional foreign-call path. The pinned revision contains PPC64 FFI fixes needed by the target matrix; it will move to a tagged release once those fixes are included.

## JIT concepts

The Builder describes values and control flow independently of the target machine. `Finish` validates the program and returns an immutable sealed program. Lowering follows **instruction selection → register allocation and spill scheduling → layout → byte or text assembly**. Selection may fold arithmetic into memory addresses, use immediates, or reuse arithmetic flags for a following branch. [The design document](docs/virtual-register-design.md) describes these stages and the ABI constraints.

Variable homes follow scope lifetimes; selected virtual values receive registers, spill slots or rematerialized constants. The spill scheduler preserves live values around fixed-register instructions and calls using their declared effects. Layout then resolves branches, pools and relocations before an assembler renders the instructions.

Native loads and stores require an address valid for the entire access. `Store8` accepts 8-, 16-, 32- or 64-bit scalar values, `Store16` accepts 16- or 64-bit values, and `Store32` accepts 32- or 64-bit values; each stores only the low bytes. `Store64` accepts only 64-bit values. Cast other widths explicitly before a store; the same width rules apply to raw IR. `Load64` and `Store64` use the target's native word representation, while `LoadBits` and `StoreBits` are little-endian at every width, including 128 bits. IR memory operations record byte order explicitly, so addressing selection cannot change it. Use `code.Call(execution.Words(&buffer[0])...)` for an output buffer, after ensuring it is nonempty: the argument frame keeps its memory alive, and heap objects do not move, so pinning is unnecessary.

### Lowering examples

Each row starts with `b := nyanjit.NewBuilder()` and uses the default `abi.StackNoUse` policy. The listings were generated on a 64-bit host with explicit `amd64` and `arm64` targets and byte offsets omitted. Both use destination-first syntax; amd64 uses Intel syntax, and ARM64 prefixes immediates with `#`. The C ABI supplies integer arguments in `RDI`, `RSI` and `RDX` on amd64 and `X0`, `X1` and `X2` on ARM64; results return in `RAX` and `X0`, respectively. Variables can occupy registers or spill slots; these examples fit in registers. Register choices can change as the allocator evolves.

| Example | Builder snippet | amd64 assembly | ARM64 assembly | Lowering |
| --- | --- | --- | --- | --- |
| Constant | `b.Return(b.Const(42))` | `mov EAX, 0x2a`<br>`ret` | `movz X0, #0x2a`<br>`ret` | amd64 uses a 32-bit move that zero-extends into RAX; ARM64 uses movz to place a 16-bit immediate in X0 and zero the other bits. |
| Integer addition | `x, y := b.Param(), b.Param()`<br>`b.Return(b.Add(x, y))` | `mov RAX, RDI`<br>`add RAX, RSI`<br>`ret` | `add X0, X0, X1`<br>`ret` | amd64 copies the first argument into RAX before a two-operand add. ARM64 uses a three-operand add and returns directly in X0. |
| Immediate addition | `x := b.Param()`<br>`b.Return(b.Add(x, b.Const(7)))` | `mov RAX, RDI`<br>`add RAX, 7`<br>`ret` | `add X0, X0, #7`<br>`ret` | The constant becomes an immediate operand on both targets. ARM64 can update the argument in its return register directly. |
| Mutable variable | `x := b.ParamNative()`<br>`v := b.Var(x)`<br>`b.Set(v, b.Add(b.Get(v), b.ConstNative(7)))`<br>`b.Return(b.Get(v))` | `add RDI, 7`<br>`mov RAX, RDI`<br>`ret` | `add X0, X0, #7`<br>`ret` | The variable stays in RDI on amd64 and X0 on ARM64. Get and Set need no separate copies for this update; ARM64 returns directly from its register home. |
| Snapshot before Set | `x := b.ParamNative()`<br>`v := b.Var(x)`<br>`before := b.Get(v)`<br>`b.Set(v, b.Add(before, b.ConstNative(7)))`<br>`b.Return(b.Add(before, b.Get(v)))` | `mov RAX, RDI`<br>`add RAX, 7`<br>`mov RCX, RDI`<br>`mov RDI, RAX`<br>`mov RAX, RCX`<br>`add RAX, RDI`<br>`ret` | `add X1, X0, #7`<br>`mov X2, X0`<br>`mov X0, X1`<br>`add X2, X2, X0`<br>`mov X0, X2`<br>`ret` | The old snapshot is preserved in RCX on amd64 and X2 on ARM64 before Set replaces the variable's home; the result is `2*x + 7`. |
| Increment | `x := b.Param()`<br>`b.Return(b.Add(x, b.Const(1)))` | `mov RAX, RDI`<br>`inc RAX`<br>`ret` | `add X0, X0, #1`<br>`ret` | amd64 selects inc. ARM64 uses add with immediate 1; it has no dedicated increment instruction. |
| Scaled expression | `base, index := b.Param(), b.Param()`<br>`b.Return(base.Add(index.MulConst(8)).Add(b.Literal(16)))` | `lea RAX, [RDI + RSI*8 + 16]`<br>`ret` | `add X0, X0, X1, lsl #3`<br>`add X0, X0, #16`<br>`ret` | amd64 combines the whole expression into lea. ARM64 folds the scale into a shifted-register add, then adds the displacement separately. |
| Folded memory address | `base, index := b.Param(), b.Param()`<br>`b.Return(b.Load64(base.Add(index.MulConst(8)).Add(b.Literal(16))))` | `mov RAX, qword ptr [RDI + RSI*8 + 16]`<br>`ret` | `add X0, X0, X1, lsl #3`<br>`ldr X0, [X0, #16]`<br>`ret` | amd64 folds base, scaled index and displacement into one load. ARM64 computes base + (index << 3) first, then uses ldr with displacement 16. |
| Conditional selection | `condition, x, y := b.Param(), b.Param(), b.Param()`<br>`b.Return(b.Select(condition, x, y))` | `mov RAX, RDX`<br>`test RDI, RDI`<br>`cmovne RAX, RSI`<br>`ret` | `cmp X0, #0`<br>`csel X0, X1, X2, ne`<br>`ret` | A nonzero condition selects x: test/cmovne on amd64, cmp/csel on ARM64. Both avoid a branch. |
| Scope export | `x := b.ParamNative()`<br>`b.Enter()`<br>`shifted := b.Add(x, b.ConstNative(2))`<br>`scaled := b.Mul(shifted, b.ConstNative(3))`<br>`b.Leave(scaled)`<br>`b.Return(b.Add(scaled, x))` | `mov RAX, RDI`<br>`add RAX, 2`<br>`mov ECX, 0x3`<br>`imul RAX, RCX`<br>`add RAX, RDI`<br>`ret` | `add X1, X0, #2`<br>`movz X2, #0x3`<br>`mul X1, X1, X2`<br>`add X1, X1, X0`<br>`mov X0, X1`<br>`ret` | Enter and Leave control value lifetimes without adding scope instructions. ARM64 uses separate destination and source operands for arithmetic. |
| FP64 addition | `x, y := b.ParamBits(64), b.ParamBits(64)`<br>`b.Return(b.FAdd(x, y))` | `movq xmm3, RDI`<br>`movq xmm4, RSI`<br>`addsd xmm3, xmm4`<br>`movq RAX, xmm3`<br>`ret` | `fmov D0, X0`<br>`fmov D1, X1`<br>`fadd D0, D0, D1`<br>`fmov X0, D0`<br>`ret` | Integer-register bit patterns move through XMM registers on amd64 and D registers on ARM64 for arithmetic, then return through an integer register. |

See [Advanced usage](#advanced-usage) for generating these listings and inspecting reports.

### Values

Values are typed: integers of 8–128 bits, IEEE 754 bit patterns, vectors and masks. Values from different builders cannot mix. An arithmetic expression such as `base.Add(index.MulConst(8))` is materialized at its use, so the target can choose instructions and addressing modes.

`ParamNative` declares a native-width entry parameter; `ParamBits` chooses an explicit scalar width. Loads, stores and calls are ordered operations. Struct, array and slice parameters have typed `Data` views; see [Struct, array and slice parameters](#struct-array-and-slice-parameters).

### Labels and blocks

Labels and blocks define control flow. The function starts in an implicit entry block. `NewLabel` may be called before or after entry instructions. `Bind(label)` starts a block; if the current block is still open and nonempty, it supplies an implicit jump to the label. `Jump(label)` ends the current block. `Branch(p, label)` continues at the label when `p` holds; otherwise execution continues after the call, in a new block. A two-way branch is `Branch(p, a)` followed by `Jump(c)`, which lowers to one conditional branch. An empty predicate makes `Branch` an unconditional jump. Labels take no parameters.

A Label belongs to the Builder that created it and to the scope that was current at `NewLabel`; edges may target only labels of the current scope. The zero `Label` is invalid, and a Label of another Builder, including one from an earlier run of a generator, is rejected with an error wrapping `compile.ErrInvalidFunction`.

A value is usable in every block its definition dominates, so it needs no plumbing through the blocks between. Values that differ by path merge through Vars (below). `Finish` checks dominance and returns an `*ir.ValidationError` for a use its definition does not dominate.

Signed `max(x, y)` at native integer width, with a Var carrying the result. `x` and `y` are defined in the entry block, which dominates both arms:

```go
b := nyanjit.NewBuilder()
x, y := b.ParamNative(), b.ParamNative()
r := b.Var(y)
pickX, done := b.NewLabel(), b.NewLabel()
b.Branch(b.Compare(x, ir.BranchGreater, y), pickX)
b.Jump(done)
b.Bind(pickX)
b.Set(r, x) // x dominates this block
b.Bind(done) // implicit edge from the open block
b.Return(b.Get(r))
program, err := b.Finish()
```

### Variables

Variables from `Var(initial)` hold mutable state for the lifetime of their scope. `Get(v)` reads a snapshot, and `Set(v, value)` replaces the stored value without changing earlier snapshots; the value must have the variable's type. A variable behaves as one home for its whole scope; the optimized tier may promote it to SSA values, and the baseline tier keeps it in a register or spill slot. Declare variables in their scope's entry sequence, before its first `Jump`, `Branch` or `Return`.

A loop with variables, summing 1 through `n` for nonnegative `n`:

```go
b := nyanjit.NewBuilder()
n := b.ParamNative()
acc, i := b.Var(b.ConstNative(0)), b.Var(n)
loop, done := b.NewLabel(), b.NewLabel()
b.Bind(loop)
b.Branch(b.IsZero(b.Get(i)), done) // false falls through to the body
b.Set(acc, b.Add(b.Get(acc), b.Get(i)))
b.Set(i, b.Sub(b.Get(i), b.ConstNative(1)))
b.Jump(loop)
b.Bind(done)
b.Return(b.Get(acc))
program, err := b.Finish()
```

`Bind(loop)` supplies an implicit edge from the entry code to the loop header, so no initial `Jump(loop)` is needed. Vars are the Builder's only way to carry loop state and to merge values that differ by path. A front end that already has SSA form with phis can emit raw IR with `ir.ProgramBuilder` instead, whose blocks take parameters (see [Emitting IR directly](#emitting-ir-directly)). See [examples/add](examples/add/main.go) for a runnable Builder example.

### Scopes

Scopes from `Enter` and `Leave` bound the lifetimes of variables and values. `Enter` opens a nested scope; `Leave(v...)` exports the listed values to the parent scope. Other values created inside the scope expire at `Leave`. Variables from outer scopes remain accessible inside nested scopes; inner variables expire at `Leave`. Branches cannot skip a scope boundary. Scopes are optional: root-scope labels describe any control-flow graph.

```go
b := nyanjit.NewBuilder()
x := b.ParamNative()
b.Enter()
shifted := b.Add(x, b.ConstNative(2))
scaled := b.Mul(shifted, b.ConstNative(3))
b.Leave(scaled) // export scaled; shifted and the constants expire
b.Return(b.Add(scaled, x)) // (x + 2) * 3 + x
program, err := b.Finish()
```

Using `shifted` after `Leave` makes `Finish` return an `*ir.ValidationError`. Call `Leave()` without arguments when no local values need to survive. For nested scopes, export a value at each boundary it must cross. When the entered scope contains a jump or branch, `Enter` also terminates the preceding block; a scope containing only straight-line code stays in the same block. Run `go run ./examples/scopes` for the complete example.

### Predicates

Predicates combine comparisons with `And`, `Or` and `Not` with no depth limit. `Branch` rejects predicates above `ir.MaxPredicateNodes` (16,384) expanded nodes, counting each use of a shared subtree; `Finish` then returns a `LimitError` with `Kind` `LimitPredicateNodes`.

`Compare`, `CompareImmediate` and `IsZero` build conditions for `Branch`. Predicate methods `And`, `Or` and `Not` compose them. `Branch` continues after the call when the predicate is false; follow it with `Jump` to select one of two labels.

### Arithmetic

Integer and floating point operations share `Value` handles. The operation determines how their bits are interpreted.

#### Integer widths and shifts

Integer values carry a width of 8, 16, 32, 64 or 128 bits; `ParamNative` and `ConstNative` use the pointer width of the process building the IR. Arithmetic wraps at the value width. Logical shifts return zero for counts of at least the width, and arithmetic right shifts fill with the sign bit. Rotations reduce the count modulo the width. The `*By` methods take a `Value` count.

`Mul` keeps the low half of a product. `MulHigh` and `MulHighSigned` return the high half of the full unsigned or signed double-width product of two integers of 8, 16, 32 or 64 bits; together with `Mul` they give the whole product.

#### Integer division

`Div` and `Rem` divide unsigned integers of 8, 16, 32 or 64 bits toward zero, and `DivSigned` and `RemSigned` signed ones; a signed remainder has the dividend's sign. No division traps by default: dividing by zero gives a quotient of 0 and a remainder equal to the dividend, and the signed `MIN / -1` gives `MIN` with remainder 0. `SetDivisionSemantics` changes that for later divisions, and `DivWith` and the other `...With` forms for one operation: `ir.DivisionAllOnes` gives an all-ones quotient for a zero divisor, as RISC-V does, and `ir.DivisionUnchecked` emits the bare instruction for front ends that have checked the divisor themselves; it may trap otherwise. 128-bit values have no division.

#### Floating point arithmetic

Floating point values are IEEE 754 bit patterns in ordinary `Value`s. `Float32` and `Float64` create constants. `FAdd`, `FSub`, `FMul` and `FDiv` operate on two matching 32- or 64-bit values. Use `ParamBits(32)` or `ParamBits(64)` for inputs, pass their bits to `Code.Call`, and reinterpret the returned bits with `math.Float32frombits` or `math.Float64frombits`. Arguments and results travel in integer registers, including at calls; native float ABI parameters and results are not implemented.

For example, with `math` and `github.com/moriyoshi/nyanjit/backend/execution` added to the imports from the getting-started example:

```go
b := nyanjit.NewBuilder()
x, y := b.ParamBits(64), b.ParamBits(64)
b.Return(b.FAdd(x, y))
program, err := b.Finish()
if err != nil { log.Fatal(err) }

code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
if err != nil { log.Fatal(err) }
defer code.Close()

// Words splits each 64-bit argument (here float64) into two words on 32-bit hosts;
// CallPair and Wide return the 64-bit result portably.
lo, hi, err := code.CallPair(execution.Words(1.5, 2.25)...)
if err != nil { log.Fatal(err) }
fmt.Println(math.Float64frombits(execution.Wide(lo, hi))) // 3.75
```

`FMA(x, y, z)` requests a hardware-fused `x*y+z` with one rounding step. A deferred expression such as `x.FMul(y).FAdd(z)` is contracted to one FMA instruction when its multiply has one use and the CPU supports FMA; otherwise it remains a multiply followed by an add. Explicitly emitted `FMul` and `FAdd` keep separate rounding. Arithmetic follows the current CPU floating point environment.

`FCompare(x, condition, y)` compares two floats of the same width and returns 1 or 0 at that width; `FComparePredicate` gives the same comparison as a `Branch` predicate. The conditions are IEEE 754 relations: `ir.FloatEqual`, `ir.FloatLess` and the others are false when an operand is NaN, `ir.FloatNotEqual` is true then as Go's `!=` is, and each condition has its negation (`ir.FloatUnorderedGreaterEqual` for `FloatLess`) and `ir.FloatOrdered`/`ir.FloatUnordered` test for NaN.

`FSqrt` uses hardware square root, and `FConvert(x, bits)` converts between binary32 and binary64. `FFromSigned(x, bits)` and `FFromUnsigned(x, bits)` convert a 32- or 64-bit integer to a float of `bits`, and `FToSigned(x, bits)` and `FToUnsigned(x, bits)` truncate a float toward zero to an integer of `bits`. A NaN or out-of-range value saturates by default (NaN gives 0); `SetConversionSemantics` or the `...With` forms choose the target's native result instead. The math API (`FSin`, `FCos`, `FTan`, `FAsin`, `FAcos`, `FAtan`, `FAtan2`, `FSinh`, `FCosh`, `FTanh` and their inverses, `FExp`, `FExp2`, `FExpm1`, `FLog`, `FLog2`, `FLog10`, `FLog1p`, `FPow`, `FCbrt`, `FHypot` and rounding functions) expands inline into portable algorithms and also composes deferred `Value` expressions. The expansions include full-range trigonometric argument reduction and special-value handling, and make no helper calls; their stack/workspace needs depend on register pressure. Binary32 inputs are widened for evaluation and rounded back to binary32.

Floating point arithmetic is emitted on all six targets:

- **386:** requires SSE2. Values stay in XMM registers, with integer words or pairs at ABI boundaries, and FP variables and label parameters can use XMM homes. Constants use a shared read-only pool whose absolute loads are rebased for each binding. Explicit FMA is unsupported.
- **amd64:** FMA requires FMA3. An explicit amd64 target uses baseline instructions; set `CPU: abi.CPUProfile{Extensions: []string{"fma"}}` to permit FMA3/AVX and multiply-add contraction.
- **ppc64le:** uses POWER8 instructions for FP32 and FP64, including single-precision FMA.
- **loong64:** uses baseline scalar FP and FMA, keeps values and variables in f8–f23, and loads shared literal pools with PCADDU12I + FLD.

`Report.FPStatusModified()` reports possible floating point status changes separately from integer flags, and `ClobberedFPRegisters()` lists the floating point registers the code modifies.

### Calling Go code from within

`CallGo(target, args...)` emits a call from generated code to a compatible Go function with scalar arguments and one integer result. Compile the caller with `Context: compile.ContextGoManaged`, `AssumePointerFree: true` and a platform stack policy. Binding registers the generated frame so Go callees can grow the stack, run GC and unwind panics.

Callable targets must be fixed, closure-free Go function entries supplied as constants or symbol bindings. Closures and interface methods are outside this scalar contract. See [Safety / trust model](#safety--trust-model) for target and lifetime requirements, and [snake_termbox](examples/snake_termbox/main.go) for a complete managed-call example.

### FFI / system calls

`CallC(target, args...)` emits a call through the platform C ABI. Use `ContextForeign` for generated C entries running in a foreign thread context, or `ContextGoManaged` to reach C through the runtime gateway from a registered Go frame. The `foreign` package describes native function pointers and registers Go callbacks with C signatures.

`CallC` arguments follow the target psABI for their IR widths. Values of 8 or 16 bits are zero-extended; use `SignExtend` first for a signed `char` or `short` parameter. On loong64 and riscv64 a 32-bit argument is sign-extended to 64 bits, including `uint32` bit patterns. Pass a 64-bit IR value (for example `b.CastUnsigned(x, 64)`) for a 64-bit C parameter.

`Syscall(number, args...)` emits a raw Linux system call with up to six arguments and returns its raw result, including negative error values. Use `ContextExternal` for code written to an executable or otherwise run outside this process; link its template with `Bytes` or `LinkAt`. `Code.Call` does not provide the runtime transition for raw syscalls. [examples/elfwrite](examples/elfwrite/main.go) shows a standalone Linux executable.

`CallCWithEffect` and `CallGoWithEffect` accept an explicit `abi.CallEffect` instead of conservative ABI clobbers. The effect must cover every transitive callee. Calls and syscalls require `abi.StackPlatform`.

### ABI / architecture dependent configuration

`Func.ABI` selects the generated function's entry convention; each `CallC`, `CallGo` and `Syscall` selects its callee convention independently. Managed frames require pointer-free generated values and a supported Go runtime. `compile.PreflightCall` reports call locations and conservative clobbers, and `Unit.Report()` reports the generated body's register, memory, stack and workspace effects.

Supported targets are `amd64`, `arm64`, `386`, `riscv64`, `loong64` and `ppc64le` (big-endian `ppc64` is rejected). An explicit architecture defaults to its baseline CPU, including on the host. Leaving the architecture empty detects host CPU features. `NativeBits` and `ParamNative` follow the process building the IR, so choose explicit widths for cross-target programs. Callable code and native bytes require the host target.

The stack policy chooses where spills live. `abi.StackNoUse` leaves the stack pointer unchanged and uses the workspace that `Code.Call` supplies. `abi.StackPlatform` permits a bounded stack frame of up to `StackPolicy.BytesAvailable` bytes. The budget includes ABI frame overhead as well as spill slots; arm64 reserves both a saved link-register word and a saved frame-pointer word (with 16-byte alignment), so 128 bytes permits fewer spills than 128 bytes of value storage. Outbound calls and system calls require `StackPlatform`.

See [CPU profiles](docs/cpu-profiles.md) for ISA selection and [backend parity](docs/backend-parity.md) for native integer operations, flag reuse and validation.

#### Call contexts

`Func.Context` says how the generated function is entered and what it may call. `Lower` checks the program against it:

| Context | Entered from | Outbound calls |
| --- | --- | --- |
| `compile.ContextNative` (default) | anywhere, through `Code.Call` | none |
| `compile.ContextForeign` | a C call in a foreign (non-Go-managed) thread context | C functions |
| `compile.ContextGoManaged` | a Go-managed call chain (a Go call, or a C entry reached through a Go runtime callback) | Go functions directly, C functions through the runtime gateway. The frame is registered with the Go runtime when bound; generated values must be pointer-free (`AssumePointerFree`). |
| `compile.ContextExternal` | outside this process, for example from an executable file | system calls and C calls to symbols resolved when linking. `Bind` returns `ErrExternalCallOnly`; use `Bytes` or `LinkAt`. |

#### Argument and result words

How `Code.Call` and its variants pass words:

- `Code.Call` takes native ABI words (`uintptr`) and returns the first result word. Pointer and `ParamNative` arguments are one word everywhere.
- On 386, 8-, 16- and 32-bit parameters take one stack word, and 64-bit parameters take two consecutive words, low word first (cdecl).
- `CallPair` returns a full 64-bit result: from EAX:EDX for C entries, or the two-word result slot for Go entries.
- `execution.Words` builds the argument words from each value's type: `int64`, `uint64` and `float64` are 64-bit values, while `int`, `uint`, `uintptr`, `bool`, `float32` and narrower integers take one word. Give 64-bit constants an explicit type, as in `uint64(35)`; an untyped constant becomes a native `int`. On 64-bit hosts every scalar is one word.
- Struct and array values become the addresses of read-only boxed copies; pointers to them become writable addresses. A slice becomes the address of a boxed header, with writable elements. These aggregates may recursively contain integers, bools, floats, structs, arrays and slices. Pointers inside aggregates, `unsafe.Pointer`, strings, maps, interfaces, chans, funcs and complex numbers are rejected with a field path. Typed root pointers to transferable scalars or aggregates are accepted. A result holding aggregate addresses keeps the roots and their reachable slice backing arrays alive.
- For every entry kind, pass the original `Words` result directly to `Call` or `CallPair`; copying its words with `append` drops ownership.

#### PPC64LE specifics

- **ABI.** C entries and C callees use ELFv2, and C function pointers are global code entries. `Code.EntryAddress` returns raw generated code.
- **CPU level.** Set `TargetSpec.CPU.Level` to `"power8"` (the default), `"power9"` or `"power10"`. POWER9 and POWER10 contract a single-use integer multiply followed by an add into `MADDLD`, for widths of 8, 16, 32 or 64 bits; narrower results keep their modular semantics. Native compilation checks the kernel's CPU capabilities and rejects an unavailable level; cross-target assembly text can use any supported level. `Report.CPU()` records the resolved level and extensions.
- **Wide arithmetic.** Native register-count shifts and rotations, carry chains for wide addition and subtraction, and high-product instructions for wide multiplication.
- **Flag reuse.** Arithmetic predicates reuse CR0 sign/zero and XER carry/borrow when their value and width still match, so a decrement followed by a nonzero branch can emit `addic.` and `bne`. POWER9/10 can also reuse 32-bit carry through XER.CA32.
- **Compact loops.** `PreferCompactLoops` selects `mtctr` / `bdnz` for eligible countdown loops. Counter uses in the body, live decrement flags, calls, side entries and competing CTR loops keep ordinary arithmetic branches. `Unit.RegisterScopes()` reports the opportunities, and emitted reports list actual CTR clobbers.
- **Callbacks.** When real cgo is linked, C-created threads can enter through the callback trampoline; the runtime attaches them through its TLS path. Registered callback mappings stay alive for the process lifetime.
- **Managed frames.** They reserve all twelve Go argument homes, preserve the C nonvolatile GPR, FP, vector and condition resources for C entries, and are limited to 640 bytes. Supply at least that stack budget for a managed C entry.

### SIMD

`ir.Type` distinguishes scalars, vectors and boolean masks. `Value.Bits()` returns the total width as `uint16`; scalar-width APIs use `value.Type().LaneBits`.

```go
b := nyanjit.NewBuilder()
dst, a, c := b.ParamNative(), b.ParamNative(), b.ParamNative()
t := ir.Vec(4, 32)
b.VStore(dst, b.Add(b.VLoad(t, a), b.VLoad(t, c)))
b.Return(b.Literal(0))
program, err := b.Finish()
```

Matching vectors use the ordinary integer arithmetic, bitwise, shift and rotation methods, plus `VLoad`, `VStore`, `Splat`, `ConstVector`, lane extraction and insertion, integer comparisons and `MaskBits`. SSE2 on amd64/386, NEON on arm64 and LSX on loong64 execute supported operations in vector registers. Wider vectors are split, and other operations or targets use scalar lanes. Loads and stores allow unaligned addresses and place lane zero at the lowest address.

`TargetSpec.Vectors` selects the vector policy:

| Policy | Meaning |
| --- | --- |
| `compile.VectorsAuto()` (the zero value) | Use the baseline SIMD the host or explicit target supports. |
| `compile.VectorsOff()` | Scalar lanes only; useful for testing. |
| `compile.VectorsRequire("pclmulqdq")`, `VectorsRequire("lsx")`, … | Require the extensions, which become part of the CPU contract. `NewTarget` fails with `ErrCPUUnsupported` when an explicit CPU profile lacks them, and native templates fail when the host does. |

Each vector operation appears in `Report.VectorOperations()` as Native, Expanded, Split or Scalarized.

Target details:

- **386:** integer SIMD and scalar FP share five XMM homes (XMM3–7), and vector spills occupy 16 bytes.
- **x86:** `pclmulqdq` enables carryless multiplication on both amd64 and 386.
- **loong64:** LSX supports 128-bit integer lanes, full-count shifts and rotates, and byte masks. It shares sixteen f8–f23 homes with scalar FP, and spills complete vectors across C and Go calls. An explicit CPU profile must include LSX. LASX profiles imply LSX, but 256-bit LASX emission is not implemented.

Use `Var` to carry vectors and masks across blocks. A mask is stored with `b.Var(m.Value())` and read with `b.Get(v).Mask()`; scoped vector and mask homes are scalarized. See the [SIMD design](docs/simd-design.md) for semantics, examples and later phases.

## Calling generated code from Go

Using the addition program from [Getting started](#getting-started), compile a Go ABI entry and expose it as a typed Go function:

```go
code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{
    ABI:   abi.Go,
    Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240},
})
if err != nil { log.Fatal(err) }
var add func(uintptr, uintptr) uintptr // matches the ParamNative inputs above
if err := code.Func(&add); err != nil { log.Fatal(err) }
fmt.Println(add(20, 22)) // 42
```

`Code.Func(&f)` writes a checked function value into the caller's function variable. It returns `ErrSignature` for a nil or invalid destination, and leaves the variable unchanged on error.

- It accepts integer parameters matching the compiled widths, and `*T` parameters for transferable structs or arrays matching the exact type recorded by `PointerParam`. It rejects by-value structs, arrays and slices, because Go splits those across ABI registers instead of using the `Words` address convention. It permits at most one integer result. Floating point values still travel as bit patterns.
- The code must not need a workspace, so compile it with `StackPlatform`.
- The function value retains its `Code` and permanently quarantines the mapping, so existing function values remain callable after `Close`. New calls through the closed `Code` fail.
- Pointer signatures use a Go wrapper that rejects nil arguments and keeps the typed aggregate targets reachable for the entire native call, including across callbacks. Generated frames have no pointer maps, so callbacks must not replace reachable slices whose elements generated code is still using. Other memory can be passed as a pinned `uintptr`.

### Struct, array and slice parameters

`ValueParam[T](b)` takes a struct or array by value, `PointerParam[T](b)` takes `*T`, and `SliceParam[E](b)` takes `[]E`. Each consumes one native-width entry parameter and returns a `Data` view. Layouts, offsets, alignment and element strides come from the host Go type, consistent with `NativeBits`; they are not target-independent wire formats.

```go
type Item struct { ID uint32; Score float64 }
type Args struct { Count uint32; Items []Item; Grid [4][4]uint8 }

b := nyanjit.NewBuilder()
args := nyanjit.ValueParam[Args](b)
out := nyanjit.PointerParam[[2]uint64](b)
ids := nyanjit.SliceParam[uint32](b)
out.At("[0]").Store(b.CastUnsigned(args.Field("Count"), 64))
out.At("[1]").Store(b.CastUnsigned(ids.Len(), 64))
b.Return(b.CastUnsigned(args.Field("Grid[1][2]"), 64))
// Compile as usual, then call code.Call(execution.Words(argsValue, &outputArray, idSlice)...).
```

`At(path)` returns another view; `Field(path)` is `At(path).Load()`. Paths use declared field names, dots and constant array indexes, including `"[2].ID"` for an array root. A path stops at a slice header: use `args.At("Items").Index(i, outOfBounds)` for slice elements. `Load` is for scalar leaves; integers are zero-extended at their width (use `SignExtend` for signed arithmetic), bools have width 8, and floats use the bit-pattern Values expected by `FAdd` and the other FP operations. `Store(value)` requires the matching scalar width. Aggregate values and slice headers cannot be stored, so this API never stores Go pointers or needs a write barrier.

| Region | Writable through `Store` |
|---|---|
| Struct/array copy supplied to `ValueParam` | No |
| Target of `PointerParam` | Yes |
| Backing array of a slice, wherever its header lives | Yes |
| Fields/elements within a region | Inherit that region's rule |

`Len()` returns a native-width array length or freshly reads slice length. `Index(i, outOfBounds)` branches to the supplied label on unsigned bounds failure, then resumes in a fresh in-bounds block with an element view. The branch is mandatory; there is no bounds predicate to discard. Values defined before the bounds branch remain usable by dominance; use Vars for mutable loop state:

```go
done := b.NewLabel()
elem := args.At("Items").Index(b.Get(index), done)
score := elem.Field("Score")
elem.At("Score").Store(b.FAdd(score, b.Float64(1)))
// ... finish this block, then Bind(done) to handle bounds failure.
```

`IndexedAddress(base, index, count, scale, outOfBounds)` applies the same branch rule to raw addresses. Its three scalar operands must have the same width. The raw address still requires a valid backing allocation and the caller's lifetime discipline.

Root views and their `At` descendants keep their base in an entry `Var`, so they work after `Bind` and inside loops. Indexed views and indexed addresses cannot cross block boundaries, including by dominance, through arithmetic aliases or through Vars, and expire at every call or syscall. Take a fresh `Index` in the destination block, including the other arm of a branch. A raw `Store64`/`Load64` address round-trip bypasses borrow tracking and belongs to the trusted raw-memory contract. `Rebase` preserves the original indexed block and cannot revive an expired view; obtain a fresh `Index` after a callback. `Base` excludes the view's static offset and `Rebase` preserves it; `Addr()` includes the offset. Raw stores through an address bypass the checks: writing into a read-only region is undefined, and Go pointer words and slice headers must not be stored. Reads, slice lengths and slice data pointers are not cached; keep a length or an address in your own `Var` if you need a snapshot. Nil and empty slices have no valid element index.

`Call`/`CallPair` keep the original `Words` frame and its pointer/aggregate roots alive until the call returns. Callers must match the compiled logical types; `Words` checks transferable memory shapes, and `Func` additionally checks its signature. Callbacks must not reallocate or replace borrowed slice backing arrays while generated code holds their addresses (including any growing `append`). Indexed Data operations require a fresh `Index` after the callback; raw saved addresses and rebased roots remain the host's responsibility. See [examples/aggregates](examples/aggregates) for a complete loop using all three parameter forms. The lower-level `builder` package exposes the same API with explicit errors.

## Compiler details

Use the compilation stages directly when you need repeated bindings, cross-target inspection, symbol linking or a front end that emits IR.

- A **program** is validated IR,
- A **target** resolves architecture and CPU policy,
- A **unit** owns one lowering, and
- A **template** owns its encoded relocatable body.
- Executable **code** is created by binding a template.

### Targets, units and templates

- **`compile.TargetSpec`** says what to generate for:
  - `Architecture` (empty means the host);
  - `CPU` (an `abi.CPUProfile`; zero means the detected host CPU, or the target's baseline when cross-compiling);
  - `Vectors` ([SIMD](#simd));
  - `Tier`. `compile.TierOptimized`, the zero value, builds SSA IR, promotes Vars and allocates registers for the whole function. `compile.TierBaseline` builds no SSA: it records the function in a compact form, computes liveness once, and allocates and encodes in one forward pass. It compiles 4 to 13 times faster (see [Performance](#performance)), and its code is somewhat larger and can be slower. Both tiers accept the same programs and compute the same results; backend limits (spill words, frame size, branch range) can differ, and a baseline `*compile.LimitError` means the function should be compiled at `TierOptimized`. Baseline engines exist for amd64, arm64, 386, riscv64, ppc64le and loong64; `NewTarget` returns `compile.ErrUnsupported` elsewhere, and a baseline Stream returns it for `Func.Harden` and `ContextGoManaged`.
- **`compile.NewTarget(spec)`** validates the spec and returns a `compile.Target`. A Target is safe for concurrent use and holds what every lowering for it shares: the resolved CPU profile and vector policy, and a pool of compilation buffers. Equal specs return the same shared Target. Without `Harden`, the same program and function settings give identical code from a fresh Target, a warm one, or one under concurrent load. `target.Vectors()` describes the SIMD baseline it will use.
- **`compile.Func`** describes one function:
  - `ABI` (`abi.C` or `abi.Go`) and `Context` ([call contexts](#call-contexts));
  - `Stack`, a `compile.StackPolicy{Practice, Pointer, BytesAvailable}`;
  - `AssumePointerFree`, `PreferCompactLoops`, `Harden`;
  - `Constants`, compile-time values for [symbols](#symbols).
- **`nyanjit.NewBuilderFor(target, f)`** returns a Builder bound to a Target and function settings; `b.Lower()` returns its `*compile.Unit`. The same generator code runs against either tier: at `TierBaseline` each block is lowered as the Builder finishes it and no `ir.Program` is ever built, and at `TierOptimized` the function is lowered as `compile.Lower` would. `Finish` on a bound Builder returns an error; use an unbound `NewBuilder()` when you need an `*ir.SealedProgram`. To tier up, call the generator again with a Builder for the other Target.
- **`compile.NewStream(target, f)`** is the engine behind a bound Builder, open to raw-IR front ends too: `Segment(seg)` takes each `ir.Segment` (one terminated block, in layout order), and `Unit()` ends the function.
- **`compile.Lower(target, program, f)`** selects instructions, allocates registers and fixes offsets once. A `Unit`'s outputs are lazy and safe to request concurrently; a Unit never owns executable memory. `Listing` shows concrete runtime gateways so its offsets match the template; `ListingSymbolic` names them instead, so it reads the same on every host. `RegisterScopes` reports register use per value scope, including optional CTR loop opportunities on ppc64le.
- **Reports** are `abi.Report` values: read-only views with one getter per field (`CodeBytes()`, `ClobberedRegisters()`, …). Getters return the caller's own copies, and `Snapshot()` returns every field as an `abi.EmissionReport`. A unit and its template share one report; nothing copies it until you ask.
- **`execution.Template`** holds the encoded bytes, relocations and report. `Bind(symbols)` creates an independent executable mapping; `Bytes(symbols)` returns linked bytes when no mapping address is needed, and `LinkAt(symbols, base)` links for a known address.
- **`execution.Arena`** keeps related functions in one address range: `arena, err := execution.NewArena(size)` reserves it, and `template.BindIn(arena, symbols)` binds into whole pages of it instead of a new mapping. `codes, err := arena.BindAll(templates, symbols)` packs several templates into one run of pages with a single write-to-execute transition; neighbouring `Code`s share boundary pages, and a page is returned when the last `Code` on it is closed. No page is ever writable and executable at once. Free pages are inaccessible and commit no memory. Closing a `Code` returns its pages; `arena.Close()` stops new bindings and releases the range once the last `Code` in it is closed. Go-managed code keeps its pages for the process lifetime. A full arena returns `execution.ErrArenaFull`.

### Programs

`Finish` returns an immutable `*ir.SealedProgram` containing validated IR and reusable source analyses. The root Builder defers its first construction error until `Finish`; the lower-level `builder.Builder` returns errors at each operation.

- **Accessors** (`Len`, `Arity`, `Result`, `Instructions`, `Variables`, `ScopeExits`, `Control`, `ResultParts`, `ParameterTypes`) return the program's storage without copying. Read it, but never modify it: compilers trust the validation done at sealing.
- **`Analysis()`** returns the analyses computed once at sealing time as query methods, among them `ScopeAt`, `Parent`, `Bounds`, `Locals`, `VarUses`, `VarSets`, `Uses`, `LastUse`, `Dominates`, `HasLoop`, `IsFloating` and `LabelFloating`.
- **`ir.Seal(p)`** validates a hand-built `*ir.Program` and copies it, so you can keep using `p`. **`ir.SealOwned(p)`** validates and takes ownership without copying; don't use `p` afterwards. **`ir.Analyze(p)`** analyzes a valid program you keep owning.
- **`Clone()`** returns an independent, editable `*ir.Program`, for tools that change a program and seal it again.

Compiler entry points accept `ir.Input`, so a raw `*ir.Program` also works; it is detached and validated on each compilation, so seal it once when you compile it repeatedly. A sealed program can be compiled concurrently with different targets; target-specific lowering stays local to each unit. See [sealed programs](docs/sealed-programs.md).

### Usage

The examples here use the `program` produced in [Getting started](#getting-started).

### Lowering, inspecting and binding

Use the steps directly when you compile many functions, want more than callable code, or want control over each step:

```go
target, err := compile.NewTarget(compile.TargetSpec{}) // host; CPU features resolved once
if err != nil { log.Fatal(err) }
// Share target between goroutines; it is safe for concurrent use.

unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
if err != nil { log.Fatal(err) }
report := unit.Report()          // registers, stack/workspace, clobbers, CPU requirements; no encoding
listing, err := unit.Listing()   // exactly the instructions and offsets that Template encodes
if err != nil { log.Fatal(err) }
template, err := unit.Template() // *execution.Template, encoded on first request
if err != nil { log.Fatal(err) }
code, err := template.Bind(nil)  // a new executable mapping; bind as many times as you need
if err != nil { log.Fatal(err) }
defer code.Close()
```

### Cross-target listings

A unit for any supported target can be listed from any host. Select the target with `TargetSpec.Architecture`; leave it empty for the host:

```go
target, err := compile.NewTarget(compile.TargetSpec{Architecture: "amd64"})
if err != nil { log.Fatal(err) }
unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
if err != nil { log.Fatal(err) }
listing, err := unit.Listing()
if err != nil { log.Fatal(err) }
fmt.Print(listing)
```

**Cross-target units** give exact reports and listings. Go-managed programs with outbound C calls need the target host's gateways for `Listing`, so cross-target they support `ListingSymbolic` only. Templates require the host Linux target and a CPU profile the host supports.

When the program calls C from Go-managed code, the listing has two forms:

- `Unit.Listing()` shows the concrete gateway addresses, matching the template byte for byte.
- `Unit.ListingSymbolic()` shows symbolic gateway names, the form used for cross-target listings. Its offsets can differ from linked byte offsets when symbol constants or gateway addresses use different expansion lengths.

### Symbols

`b.Symbol(name)` is a value whose number is supplied later, in one of two ways:

- **At bind or link time:** `template.Bind(execution.Symbols{"puts": addr})` patches the relocations, so one template can be bound to different addresses.
- **At compile time:** `compile.Func{Constants: map[string]uint64{"k": 7}}` substitutes the value before lowering. The generated code can then specialise on it, for example by using an immediate operand or folding an address.

### Errors

Each entry point's documentation lists its errors:

- `NewTarget`: an unknown architecture matches `compile.ErrUnsupported` and names it; CPU-profile errors and an unknown `Tier` match both `compile.ErrInvalidFunction` and `abi.ErrInvalidSpec`; an extension beyond an explicit CPU profile matches `ErrCPUUnsupported`; an unusable vector extension matches `compile.ErrBackendLimit`.
- `Lower`: the call-context checks (`ErrExternalCallOnly`, `ErrUnsupportedABI`, `ErrStackRequired`, `ErrPointerMapsRequired`), invalid constants (`ErrUnboundSymbol`, `ErrInvalidFunction`) and programs that exceed the target (`ErrBackendLimit`).
- `Unit.Template`: the managed-frame limit (`ErrBackendLimit`) and host requirements (`ErrUnsupported`, `ErrCPUUnsupported`).

Validation failures are an `*ir.ValidationError{Index, Op, Rule}` wrapping `ir.ErrInvalidProgram` (and matching `ErrInvalidFunction`), which names the offending instruction (`Index` is -1 for a rule about a step, label, scope or Var). When a backend limit has a number, the error is a `*compile.LimitError{Kind, Needed, Limit, Arch, Detail}` wrapping `ErrBackendLimit`, so a front end can split or restructure the work: `LimitArguments` (entry parameters and outbound call arguments, also from `compile.PreflightCall`), `LimitSpillWords`, `LimitFrameBytes` (a Go-managed frame above the target's limit), `LimitPredicateNodes` and `LimitBranchRange` (arm64 branch and literal reach). `Needed` can be a lower bound when counting stopped at the limit; zero means unknown. The root Builder's own checks (entry parameters, call arguments, predicate size) report a `LimitError` from `Finish` too; the lower-level `builder.Builder` returns them as `ErrBackendLimit` with the numbers in the message.

## Advanced usage

### Emitting IR directly

A front end with its own IR can skip the Builder and emit NyanJIT IR. [docs/ir.md](docs/ir.md) specifies every opcode's operands, widths and semantics, together with the value, block, scope, variable and predicate rules. `ir.ProgramBuilder` is a thin appender for SSA-style front ends; unlike the Builder, its blocks take parameters, which incoming edges supply. Instructions can be appended to any open block, operands name earlier instructions by the IDs the builder returns, and `Seal` lays the reachable blocks out in reverse postorder:

```go
pb := ir.NewProgramBuilder()
entry := pb.Entry()
n := pb.Param(entry, ir.Scalar(64))
loop := pb.NewBlock(ir.Scalar(64)) // one block parameter
done := pb.NewBlock()
pb.Jump(entry, loop, n)
i := pb.BlockParam(loop, 0)
one := pb.Append(loop, ir.Instruction{Op: ir.OpConst, Imm: 1})
next := pb.Append(loop, ir.Instruction{Op: ir.OpSub, A: i, B: one})
pb.Branch(loop, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: next},
    done, nil, loop, []int{next})
pb.Return(done, n) // n is defined in the entry block, which dominates done
program, err := pb.Seal() // validates and returns *ir.SealedProgram
```

The result compiles like a Builder program. A `*ir.ValidationError` from `Seal` carries the builder's instruction ID. To check a front end, compare against `instrument/irinterp` (a reference interpreter for the IR), or run `instrument/irconform`, which executes programs on the host target and compares the results with the interpreter. See [examples/irfrontend](examples/irfrontend/main.go) for a bytecode front end.

### Using the backend module on its own

Projects with their own IR, control model or calling scheme can use `nyanjit/backend` without NyanJIT's IR:

- **`abi` and `cpuprofile`** classify calls, describe call effects and resolve CPU profiles.
- **`target/...`** provides each ISA's register file, policy and assemblers. The encoder API is the generic one: build `machine.Instruction` values and visit them with a target's byte or text assembler (ppc64 expands each instruction with `Emitter.Expand` first). Each target's `FORMS.md` (for example [backend/target/amd64/FORMS.md](backend/target/amd64/FORMS.md)) lists the instruction forms it accepts, their operand constraints and encoded sizes, and `backend/target/forms_test.go` assembles every row.
- **`machine`** handles layout, branch relaxation, literal pools and relocations. Architecture-specific stream passes sit behind two optional interfaces that target policies implement: `machine.LoopFuser` (countdown loops into LOOP on amd64, CTR loops on ppc64le and register-zero branches on arm64, riscv64 and loong64) and `machine.FlagElider` (unread CR0 and XER updates on ppc64le; on amd64, LEA for additions whose flags no path reads and whose destination differs from the first operand, saving the MOV before the two-address form).
- **`regalloc`** allocates registers for a linear instruction sequence.
  - `regalloc.Residency` takes the values and the positions where each is read. The front end then walks the sequence, asking it to load inputs (`Ensure`), free them at their last use (`Consume`), pick result registers (`Choose`) and free fixed registers (`Evict`). It spills the value used furthest in the future, rematerializes constants, reuses dead slots, and emits spills, reloads and moves through a `Mover` the front end supplies. Blocks are allocated one at a time and joined with `PlanEdgeTransfer`.
  - A `regalloc.Allocator` instead takes a whole `regalloc.Function`: values, the operands of each position with fixed, tied and early-clobber constraints and preferred registers, calls as `abi.CallEffect`s, the entry as an `abi.EntryLayout`, and blocks whose parameters receive edge arguments. The front end walks it in layout order and the allocator emits its spills, reloads, moves and edge transfers through the front end's `Mover`. `regalloc.Batch` allocates the whole function at once with liveness and live-range splitting;
  - `regalloc.Online` allocates as the front end walks, keeping values that cross blocks in home slots;
  - `regalloc.Verify` checks any implementation.
- **`execution` and `goregister`** map the code, register Go frames (each target's `GoFrame` describes them) and call into C (`execution.CallForeign`).

[examples/stackvm](examples/stackvm/main.go) compiles a small stack bytecode with these packages alone. [backend/README.md](backend/README.md) walks through writing a front end and lists the toolkit's ownership and borrowing contracts.

## Safety / trust model

Building or calling native code with nyanjit is equivalent to using Go's `unsafe` package. It is not a sandbox for untrusted programs. IR validation checks graph structure, operand types, widths and resource limits; it cannot prove memory addresses, native code bytes, callee contracts or loop termination.

`Load*`/`Store*` on computed addresses, `Data.Addr`/`Base`/`Rebase`, raw `execution.NewCode`/`Map`, relocation bindings, `EntryAddress`, `Func`, and `goregister.RuntimeEntry` are trusted escape hatches. So is the storage a `SealedProgram` lends out (`Instructions`, `Variables`, `Control` and the slices they reach): compilers trust the validation done at sealing, and an edit, even an ordinary assignment to `Instructions()[i]`, can compile into code that reads arbitrary registers or memory. Use `Clone` for an editable copy. Incorrect addresses, stack metadata or `CallEffect` clobber sets can corrupt memory or runtime state. Registered frames have no pointer stack maps. Go addresses passed to generated code must refer to live, heap-resident objects. `Code.Call` and `CallPair` are raw escape hatches for every entry kind, including managed and aggregate entries. Use `code.Call(execution.Words(&value)...)`: `Words` moves pointer targets to the non-moving heap and retains roots through callbacks and stack growth, while `Call` keeps the original words frame alive until it returns. Do not copy the words and discard the original result. Hand-built `uintptr` addresses remain the caller's responsibility. Direct raw pointer conversions benefit from `//go:uintptrescapes`; Go does not propagate that directive through interface method signatures, so use `Words` for pointer arguments.

Data views check host Go layouts and constant paths/indexes. Dynamic `Index(i, outOfBounds)` and `IndexedAddress` emit the unsigned bounds branch themselves and return only in the in-bounds block. Indexed Data views expire at calls/syscalls; take a fresh Index after a callback. Aggregate entry types are checked exactly by `Code.Func`. `Call`/`CallPair` do not check logical Go types: matching the compiled parameter types, layouts and pointer lifetimes is the caller's contract. `Words` rejects unsupported Go memory shapes and nil aggregate pointers, but cannot compare them with a compiled signature. A pointer-typed `Func` rejects nil in a Go wrapper with `execution.ErrArgumentType`. Nil slice values remain valid empty inputs. Pass logical aggregate values with `code.Call(execution.Words(value, &output, slice)...)`. Type metadata is process-local; JSON is not an aggregate-program serialization format. Raw IR/native-width scalar parameters and raw byte mapping remain trusted APIs. Relocation symbol bindings must be nonzero; use literal `OpConst` for numeric zero. Predicates have a program-wide budget of 16,384 expanded nodes, checked before lowering shared graphs.

Embedders using NaN boxing must canonicalize NaNs: floating point operations do not promise canonical NaN payloads. Keep `compile.TargetSpec`, `compile.Func`, symbol bindings and `CallEffect` contracts under host control, never end-user input. Callbacks must not invalidate borrowed arrays by replacing or growing slices. Hosts must emit their own iteration/work limits: a Go context cancellation or timeout cannot interrupt a running JIT loop. Compile time and memory grow with program size, roughly a kilobyte per lowered instruction, so bound the IR accepted from end users. Some operations lower to many instructions: without a native carryless multiply (x86 `pclmulqdq`), each `CarrylessMul` becomes about 500 scalar instructions, and vector lowering returns `ErrBackendLimit` once its scalar program exceeds 64 instructions per IR instruction plus 131,072. Builder math functions such as `FSin` expand to a few hundred instructions when they are built.

Callable templates enforce actual stack use at Bind: goroutine-stack entries are limited to 640 bytes (240 on 386), including outgoing call space, regardless of `StackPolicy.BytesAvailable`. Larger frames return `ErrBackendLimit`. Foreign entries use a thread stack and are capped at 4096 bytes per frame until guard-page probing is implemented. `CallC` chains between generated foreign functions have no total stack bound; the host must bound their nesting and recursion. Lower, listings and raw byte emission may describe larger bodies; placing such bytes on an execution stack is an unsafe caller responsibility. Raw `execution.CodeOptions.StackBytes` must declare the actual stack bound.

`compile.Func.Harden` is an opt-in mode for constant blinding, randomized entry placement and bounds-edge speculation protection. Blinding and mapping placement support all six targets; hardened control-flow programs support amd64, 386, arm64 and ppc64le, and return `ErrUnsupported` on riscv64 and loong64, which define no speculation barrier. See [optional hardening](docs/optional-hardening-design.md) for the target contracts, costs and limits.

Callable `CallGo` targets must be fixed, closure-free, splittable Go function entries, supplied as constants or symbol bindings. Target proof recognizes constants, symbols, native-width casts and immutable captures; it does not infer target sets through `Select` or mutable Vars, even when their inputs are constants. Dynamic targets and generated entries (registered, unmanaged or foreign) are rejected, so generated calls cannot accumulate individually legal frames on the goroutine stack. Known runtime, reflection, execution and purego trampolines are rejected. Entry metadata cannot prove arbitrary host functions are splittable: the host must never supply NOSPLIT forwarders that enter generated code. Use an ordinary Go callback to return to the checked `Code` API when reentry is needed. Raw byte emission, `EntryAddress` and the package-level `execution.Call`, `CallWithArity` and `CallPairWithArity` are trusted paths that bypass stack, managed-entry, aggregate-type and lifetime checks.

Managed `Bind` permanently retains code and metadata even after `Close`. Reuse bound code; registration is capped at `goregister.MaxRegisteredFunctions` (4096), with `ErrRegistrationLimit` and `RegisteredFunctions` for graceful exhaustion. JIT loops without Go calls cannot be asynchronously preempted and can delay GC or stop-the-world operations indefinitely. Do not run unbounded loops in-process. Build with `-tags=nyanjit_no_goregister` to exclude the private Go runtime linknames entirely; managed binding then returns `compile.ErrUnsupported`. Registration must not run concurrently with cgo `plugin.Open`: the Go runtime's plugin loader and JIT registrar do not share a lock; serialize those operations in the application.

## Packages

| Package | Module | Role |
| --- | --- | --- |
| `nyanjit` | main | `Builder` (with `Value`, `Var`, `Label`, `Predicate`, `VMask`), `Data` views for struct, array and slice parameters, and the one-shot `Compile`. |
| `nyanjit/builder` | main | The Builder's implementation, returning an error from each operation instead of deferring it to `Finish`. |
| `nyanjit/ir` | main | The IR, its specification and validation. Also `SealedProgram` and `Analysis`, plus `ProgramBuilder` for front ends that emit IR directly. |
| `nyanjit/compile` | main | `NewTarget`, `Target`, `Func`, `Lower`, `Unit` and `Emitter`: lowering NyanJIT IR to native code. |
| `nyanjit/cell`, `nyanjit/foreign` | main | Builder helpers: addressable values with a memory home, and calls through [purego](https://github.com/ebitengine/purego) foreign functions and callbacks. |
| `nyanjit/instrument/...` | main | Instrumentation only: `workload` (benchmark programs), `abicheck` (checks that callee-saved registers survive), `irinterp` (reference interpreter) and `irconform` (conformance suite). Production code must not import these packages. |
| `nyanjit/backend/op` | backend | Operation vocabulary: opcodes, conditions, byte orders. |
| `nyanjit/backend/abi` | backend | Calling conventions, call effects, CPU profiles, stack policies, emission reports. |
| `nyanjit/backend/target/...` | backend | Per-ISA register files, policies, assemblers (bytes and text), sizes and Go frames. |
| `nyanjit/backend/machine` | backend | IR-independent machine instruction stream: labels, layout, branch relaxation, literal pools, relocations. |
| `nyanjit/backend/regalloc` | backend | Register allocation: the `Allocator` protocol (`Batch`, `Online`, `Verify`), the client-driven `Residency` and their primitives. |
| `nyanjit/backend/execution` | backend | Executable memory, `Template`, `Arena`, `Bind`, `Code`, typed Go function views. |
| `nyanjit/backend/goregister` | backend | Go runtime frame registration. |
| `nyanjit/backend/cpuprofile` | backend | Host ISA feature detection. |

Each package imports only packages below it:

```text
cell, foreign                     helpers built on the Builder
nyanjit (root)                    Builder and Compile
builder                           the Builder's implementation
compile                           NewTarget, Lower, Unit, Emitter; selection from IR into machine instructions
ir                                sealed programs and their analyses
──────────────────────────────── backend module ────────────────────────────────
cpuprofile                        host ISA feature discovery
execution                         executable memory and the call boundary
target/amd64 … target/riscv64     per-architecture policy, sizes, assemblers and Go frames
machine                           selected instructions, the stream and its layout
abi, regalloc, goregister         ABIs and reports, register allocation, Go runtime registration
op                                operation vocabulary: opcodes, conditions, byte orders
```

The main module requires `backend`; `backend` never imports NyanJIT's IR, Builder or compiler, and `backend/go.mod` does not require the main module. A test (`instrument/imports_test.go`) checks both on every Linux target, test files included. `ir` re-exports the `op` vocabulary under the same names (`ir.OpAdd` is `op.OpAdd`), so IR code does not import `op`. The `instrument/` packages sit outside the stack: they may import anything, and only tests, benchmarks, examples and other `instrument/` packages may import them.

All of these packages are public, supported APIs. NyanJIT is not published yet, so exported APIs may still change. Construct exported structs with keyed fields: field order is not part of the API. `go vet` flags unkeyed literals of imported structs.

## FAQ

**Can I inspect code without executing it?** Yes. `Unit.Report` and `Unit.Listing` never encode or map anything. `Unit.Template` gives bytes, relocation records and the report; `Template.Bytes` returns fully linked bytes when a mapping address is unnecessary. For 386 literal-pool loads it returns `compile.ErrCodeBaseRequired`; use `Template.Bind` to map and link automatically, or `Template.LinkAt` when the final address is known. Lower code meant for another process (an executable file, for example) with `compile.ContextExternal`, which permits system calls.

**Can generated code call Go functions?** Yes, with `Func{Context: compile.ContextGoManaged}`, which registers the pointer-free generated frame with the Go runtime. A C ABI entry can reach this path through a Go runtime callback trampoline. Arbitrary foreign threads cannot call Go through generated code.

**When is a value invalid in another block?** When its definition does not dominate that block, for example a value defined in one arm of a branch and used after the arms merge. Keep it in a `Var`, set on each arm. Values created inside a scope also expire at its `Leave` unless they are exported.

**Is a `Target` safe to share?** Yes. It is meant to be shared: its buffer pool is what makes repeated compilation cheap, and `NewTarget` returns the same Target for equal specs. With `Harden` disabled, the same program and function settings give identical output whether a Target is fresh or warm, and with or without concurrent use.

**Which targets can execute code?** Linux/amd64 and Linux/arm64 are primary. Linux/386, riscv64, ppc64le (POWER8+) and loong64 are experimental. All six support integer, memory, control-flow, ABI and FP32/FP64 operations; ppc64le and loong64 also support C calls and callbacks and registered pointer-free Go calls. Unsupported lowering returns an error.

## Development

The repository holds several modules: the main module, `backend/`, `e2e/`, `e2e/ppc64cgo/`, `examples/snake_termbox/` and `examples/stackvm/`. A `go.work` at the root joins them, so edits in `backend/` are seen by the main module without a release. `go test ./...` at the root does not descend into the other modules; test the toolkit with `go -C backend test ./...`. CI also tests each module with `GOWORK=off`, where the main module finds the toolkit through the `replace` in its `go.mod`, so nothing depends on resolution that only the workspace provides. `scripts/vet-all.sh` vets the main, backend and stackvm modules for every supported Linux architecture, plus the unsupported linux/ppc64 and openbsd/ppc64 build stubs. It also vets snake_termbox on its supported architectures and the E2E harness on the host.

Prefer unexported implementation helpers in the package that uses them. Do not add `internal/` packages: anything shared between packages is a designed public API, and measurement or checking tools go under `instrument/`.

### Performance

CI runs compile-time scaling gates (`NYANJIT_PERF_SCALING=1`) for the workloads in `instrument/workload`. These bound growth for the tested shapes and sizes. Large programs can exceed scratch-retention budgets and regrow buffers on each compilation; [TODO.md](TODO.md#compile-time-scaling-found-in-review-2026-10-24) records those measurements and the alias-chain allocation fix. [docs/perf-baseline.json](docs/perf-baseline.json) records the reference numbers.

A shared `Target` reuses compilation buffers across lowerings and goroutines. Each lowering leases private scratch storage from its target's pool; lazy outputs own their storage. The pool drops pointer-bearing machine buffers over 1 MiB individually, then applies retention budgets of 32 MiB per entry and 4 MiB per remaining buffer. Each buffer records its own demand. While retained scratch exceeds 256 KiB, its recent high-water mark decays by one sixteenth per compile. Slice buffers over 4 KiB whose capacity exceeds four times that mark are discarded individually; scope maps follow the same policy using entry counts. When the total budget is exceeded, the largest releasable buffers and individual scope rows are discarded first until it is met; unrelated smaller buffers stay reusable. This preserves reuse across big/tiny alternations while shedding excess capacity after sustained smaller workloads. The Go garbage collector may discard pooled storage at any collection, so reuse is an optimization rather than a lifetime guarantee. Runtime unwind tables are generated only for managed templates.

#### Comparison with other JIT compilers

The harnesses in [instrument/jitbench](instrument/jitbench/README.md) build the same kernels with each engine's own API and check that they return the same results; `instrument/jitbench/run.sh` runs them all and prints tables like these. The measurements were taken on one Arm Cortex-X925 core under Linux, with one kernel per process, using sljit 39c508d, TPDE-LLVM 9779acf with LLVM 19 IR, and Cranelift 0.137. **Compile** is the time from IR construction through finished machine code. For TPDE and Cranelift it includes building their IR, and for NyanJIT it ends at `Lower` (building the Template adds 0.3–0.4 µs at the baseline tier). It excludes mapping the code executable, which costs about 5 µs per small function for NyanJIT, 7 µs for TPDE and 8 µs for Cranelift. sljit's figure includes allocation from its own reused executable pool.

| Kernel | What it does |
| --- | --- |
| `tiny` | One addition and a return |
| `chain1000` | 4,000 straight-line integer operations (multiply, shift, xor, add) |
| `loop` | A counted loop of 10M iterations with one accumulator |
| `pressure` | A loop that updates 16 accumulators, 1M iterations |
| `collatz` | Nested loops with data-dependent branches |
| `fp` | A degree-8 float polynomial and a float compare per iteration, 1M iterations |

Compile time in µs:

| Kernel | NyanJIT baseline | NyanJIT optimized | sljit | TPDE | Cranelift (`speed`) | Cranelift (`none`, single-pass) |
| --- | --- | --- | --- | --- | --- | --- |
| `tiny` | 1.47 | 5.36 | 0.54 | 1.04 | 6.80 | 3.97 |
| `chain1000` | 231 | 2,671 | 46 | 329 | 3,135 | 1,288 |
| `loop` | 3.81 | 39.7 | 0.63 | 2.03 | 15.7 | 8.47 |
| `pressure` | 10.9 | 141 | 1.28 | 7.60 | 62.4 | 28.3 |
| `collatz` | 6.30 | 72.8 | 0.79 | 2.95 | 23.9 | 13.1 |
| `fp` | 8.46 | 88.4 | 1.00 | 4.19 | 34.4 | 17.5 |

Run time in ms:

| Kernel | NyanJIT baseline | NyanJIT optimized | sljit | TPDE | Cranelift (`speed`) | Cranelift (`none`, single-pass) |
| --- | --- | --- | --- | --- | --- | --- |
| `loop` | 2.61 | 2.61 | 3.46 | 17.6 | 3.20 | 31.8 |
| `pressure` | 1.29 | 1.29 | 1.29 | 3.78 | 1.27 | 6.03 |
| `collatz` | 10.1 | 9.84 | 9.21 | 24.1 | 9.27 | 39.0 |
| `fp` | 1.74 | 1.76 | 1.42 | 3.19 | 1.65 | 4.59 |

sljit is a macro assembler: the caller assigns registers, so it compiles 3 to 9 times faster than the NyanJIT baseline tier, and its code is comparable. The baseline tier compiles 1.4 to 2.1 times slower than TPDE on the small kernels and faster on `chain1000`. On these kernels, the baseline tier's code runs 1.8 to 7 times faster than TPDE's code. It is within 10% of Cranelift's `speed` configuration, and faster on `loop`. The TPDE column uses LLVM IR in SSA form, which compiled as fast or faster and produced faster code than the `alloca` form in this test. The optimized tier compiles 2 to 3 times slower than Cranelift's `speed` configuration on the loop kernels, with similar run time.

### Cross-platform E2E tests

Run `./e2e/run.sh` to build the test container and run the Starlark matrix. Use `./e2e/run.sh e2e/smoke.star -os linux -arch riscv64` for one smoke target. The container needs Docker, privileged mode and a Linux 6.7+ kernel; `/dev/kvm` is optional and accelerates same-architecture VM guests. It stores runtime overlays, build caches and results in the `nyanjit-e2e-cache` volume. See [e2e/README.md](e2e/README.md) for runner details, expected skips, results and image licensing.

## License

nyanjit is licensed under the [Apache License, Version 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.
