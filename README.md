# NyanJIT

[![CI](https://github.com/moriyoshi/nyanjit/actions/workflows/ci.yml/badge.svg)](https://github.com/moriyoshi/nyanjit/actions/workflows/ci.yml)
[![Linux E2E](https://github.com/moriyoshi/nyanjit/actions/workflows/e2e-linux.yml/badge.svg)](https://github.com/moriyoshi/nyanjit/actions/workflows/e2e-linux.yml)

<img src="assets/nyanjit-logo-29x23-large.png" width="324" />

## What is NyanJIT

NyanJIT builds, compiles, and executes native integer and floating point code from Go without cgo. A `nyanjit.Builder` records target-independent values and control flow. `Finish` returns an `ir.Program`; `compile.Compile` lowers it to callable code. `compile.EmitAssembly` renders an assembly listing without executing the program.

Linux/amd64 and Linux/arm64 are the primary targets. Linux/386 and Linux/riscv64 have experimental backends; riscv64 also supports floating point arithmetic. The module targets Go 1.26 and supports Go 1.27 on its managed-call path.

Linux/ppc64 and Linux/ppc64le have experimental POWER8 backends. They support byte and text emission, control flow, memory access, register spills, 128-bit values, Linux syscalls, C calls, and registered pointer-free Go calls with C or Go entries. Both byte orders support runtime-originating C callbacks; big-endian callbacks accept up to eight integer/bool arguments and at most one integer/bool result. The big-endian gateway and managed registration require Go 1.26 or 1.27. FP32/FP64 arithmetic and FMA use POWER8 instructions. The portable snake example runs on both byte orders.

## Comparison with other Go JIT projects

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
| [wazero](https://github.com/wazero/wazero) | WebAssembly runtime; its optimizing engine (wazevo) compiles whole modules before execution | wazevo translates Wasm into an SSA IR with block parameters and runs CFG passes. It lowers to amd64/arm64 and allocates registers with function-wide liveness analysis, placing spills at the common ancestor of reloads. Code runs on a wazero-managed stack and leaves through exit codes for host calls, memory growth, and stack growth. NyanJIT callers build the IR directly. It allocates per block, with label arguments and scope-lifetime variable homes instead of global liveness. Go-ABI code runs on the goroutine stack, and managed frames are registered with the runtime. |
| [wago](https://github.com/wago-org/wago) | Pure-Go WebAssembly runtime with a single-pass compiler | Code generation and register allocation are fused into one forward pass over a symbolic value stack, following WARP's Valent-Block technique. Control-flow joins agree on frame slots, and there is no IR on the production path. Code runs on an off-heap execution stack, and synchronous host calls yield back to Go. NyanJIT keeps a reusable IR snapshot and separate selection, allocation, and emission passes across six architectures. Its code calls C, Go, and syscalls directly with explicit call effects. |
| [go-xla](https://github.com/gomlx/go-xla) | Go bindings for OpenXLA StableHLO and PJRT | Programs are tensor dataflow graphs built as StableHLO. A dynamically loaded PJRT plugin compiles them with XLA for a CPU, GPU, or TPU, and data moves through host and device buffers. NyanJIT compiles scalar and SIMD instruction streams to host machine code that runs in-process on Go memory. |

Related projects outside the table: [frugal](https://github.com/cloudwego/frugal) had a Thrift codec JIT, disabled by default in v0.2.4 and removed in v0.3.0. [goloader](https://github.com/eh-steve/goloader) compiles Go source with a patched compiler and links the objects into the running process; this is runtime loading, not instruction-level code generation. [wasmtime-go](https://github.com/bytecodealliance/wasmtime-go) and [wasmer-go](https://github.com/wasmerio/wasmer-go) reach Rust JIT backends (Cranelift; Singlepass, Cranelift, or LLVM) through cgo.

[purego](https://github.com/ebitengine/purego) is an FFI library that NyanJIT uses for its optional foreign-call path.

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
    "github.com/moriyoshi/nyanjit/compile"
)

func main() {
    b := nyanjit.NewBuilder()
    b.Return(b.Add(b.ParamNative(), b.ParamNative()))
    program, err := b.Finish()
    if err != nil { log.Fatal(err) }

    code, err := compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC})
    if err != nil { log.Fatal(err) }
    defer code.Close()

    result, err := code.Call(20, 22)
    if err != nil { log.Fatal(err) }
    fmt.Println(result) // 42
}
```

The root Builder defers its first construction error until `Finish`. The lower-level `builder.Builder` returns errors at each operation. `ir.Program` has public fields and can be validated, cloned, and compiled more than once. Use the root package's `CompileOptions` and ABI constants with `compile` functions; `abi` exposes the underlying convention and call-effect types when needed.

### Concepts

- **Values** are typed virtual integers that can also carry floating point bit patterns. Values from different builders cannot mix. An arithmetic expression such as `base.Add(index.MulConst(8))` is materialized at use; the target chooses instructions and addressing modes.
- **Labels and blocks** define control flow. The function has an implicit entry block. `NewLabel` may be called before or after entry instructions. `Bind` starts another block after the current block ends with `Jump`, `Branch`, or `Return`.
- **Block values** are immutable snapshots, normally usable only in the block that defines them. Read shared state with `Get` after each `Bind`. A one-target conditional `Branch` takes that target on true and falls through on false; an empty predicate makes it an unconditional jump.
- **Variables** from `Var(initial)` hold mutable state across blocks. Declare them in their scope's entry sequence, before its first `Jump`, `Branch`, or `Return`. `Get(v)` reads a snapshot; `Set(v, value)` replaces the stored value without changing earlier snapshots. The value must have the variable's type.
- **Scopes** from `Enter` and `Leave` bound variable lifetimes and capture enclosing block values for use throughout the entered scope. Export local values that must survive `Leave`. Variables from outer scopes remain accessible inside nested scopes; inner variables expire at `Leave`. Branches cannot skip a scope boundary.
- **Stack options** choose where spills live. `StackNoUse` keeps the stack pointer unchanged and uses workspace supplied by `Code.Call` when needed. `StackPlatform` permits a bounded stack frame with `StackBytesAvailable`.

Variables keep inputs available in either branch. This computes signed `max(x, y)` at native integer width:

```go
b := nyanjit.NewBuilder()
x, y := b.ParamNative(), b.ParamNative()
left, right := b.Var(x), b.Var(y)
bigger, other := b.NewLabel(), b.NewLabel()
b.Branch(b.Compare(b.Get(left), nyanjit.BranchGreater, b.Get(right)),
    bigger, other)
b.Bind(bigger)
b.Return(b.Get(left))
b.Bind(other)
b.Return(b.Get(right))
program, err := b.Finish()
```

A loop updates variables in a scope enclosing its labels. For nonnegative `n`, this computes the sum from 1 through `n`:

```go
b := nyanjit.NewBuilder()
n := b.ParamNative()
b.Enter()
acc, i := b.Var(b.ConstNative(0)), b.Var(n)
loop, done := b.NewLabel(), b.NewLabel()
b.Jump(loop)
b.Bind(loop)
b.Branch(b.IsZero(b.Get(i)), done) // False falls through to the body.
b.Set(acc, b.Add(b.Get(acc), b.Get(i)))
b.Set(i, b.Sub(b.Get(i), b.ConstNative(1)))
b.Jump(loop)
b.Bind(done)
result := b.Get(acc)
b.Leave(result)
b.Return(result)
program, err := b.Finish()
```

Compile either program with `compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC})`. The allocator chooses register or spill-slot homes for variables; the loop needs no copies of its state at the back edge. See [examples/add](examples/add/main.go) for a runnable Builder example.

### Value scopes

`Enter` opens a nested value scope. Values from its enclosing scope remain usable inside it. `Leave` exports the listed values to the parent scope; other values created inside the scope become invalid even if their Go variables remain in scope.

```go
b := nyanjit.NewBuilder()
x := b.ParamNative()
b.Enter()
shifted := b.Add(x, b.ConstNative(2))
scaled := b.Mul(shifted, b.ConstNative(3))
b.Leave(scaled) // Export scaled; shifted and the constants expire.
b.Return(b.Add(scaled, x)) // (x + 2) * 3 + x
program, err := b.Finish()
```

Here, `x` remains valid because it belongs to the enclosing scope, and `scaled` survives because it was exported. Using `shifted` after `Leave` would cause `Finish` to return `ErrInvalidFunction`. Call `Leave()` without arguments when no local values need to survive. For nested scopes, export a value at each boundary it must cross. These scopes control value lifetimes while building the program; they do not introduce runtime branches.

Run `go run ./examples/scopes` to compile this function and call it with `x = 9`, producing `42`. See [examples/scopes](examples/scopes/main.go) for the complete example.

### Integer widths and shifts

Integer values carry a width of 8, 16, 32, 64 or 128 bits; `ParamNative` and `ConstNative` use the architecture’s pointer width. Arithmetic wraps at the value width. Logical shifts return zero for counts at least that width; arithmetic right shifts fill with the sign bit. Rotations reduce the count modulo the width. The `*By` methods accept a `Value` count.

The PPC64 backend selects native register-count shifts/rotations, carry chains for wide addition/subtraction, and high-product instructions for wide multiplication. The Builder API remains the same across architectures.

### Portable SIMD

`Type` distinguishes scalars, vectors and boolean masks. `Value.Bits()` returns
total width as `uint16`; scalar-width APIs use `value.Type().LaneBits`.

```go
b := nyanjit.NewBuilder()
dst, a, c := b.ParamNative(), b.ParamNative(), b.ParamNative()
t := nyanjit.Vec(4, 32)
b.VStore(dst, b.Add(b.VLoad(t, a), b.VLoad(t, c)))
b.Return(b.Literal(0))
program, err := b.Finish()
```

Matching vectors use the ordinary integer arithmetic, bitwise, shift and rotation
methods. Phase 1 adds `VLoad`, `VStore`, `Splat`, `ConstVector`, lane extraction and
insertion, integer comparisons and `MaskBits`. SSE2 on amd64 and NEON on arm64
execute supported operations in vector registers; wider vectors split, and other
operations/backends use scalar lanes. Loads/stores allow unaligned addresses and
place lane zero at the lowest address.

`abi.CompileOptions{Features: []string{}}` forces scalar SIMD lowering for testing;
nil selects the baseline SIMD supported by the host/explicit target. Each operation
appears in `EmissionReport.VectorOperations` as Native, Expanded, Split or Scalarized.
Use `Var` to carry vectors and masks across blocks. A mask is stored with
`b.Var(m.Value())` and read with `b.Get(v).Mask()`. Phase 1 scalarizes scoped
vector and mask homes. See the
[SIMD design](docs/simd-design.md) for semantics, examples and later phases.

### Floating point arithmetic

Floating point arithmetic uses IEEE 754 bit patterns in ordinary `Value`s. `Float32` and `Float64` create constants; `FAdd`, `FSub`, `FMul`, and `FDiv` operate on two matching 32- or 64-bit values. Use `ParamBits(32)` or `ParamBits(64)` for inputs, pass their bits to `Code.Call`, and reinterpret the returned integer bits with `math.Float32frombits` or `math.Float64frombits`. This interface uses integer argument and result registers, including at calls; it does not implement native float ABI parameters or results.

For example, with `math` and `github.com/moriyoshi/nyanjit/execution` added to the imports from the getting-started example:

```go
b := nyanjit.NewBuilder()
x, y := b.ParamBits(64), b.ParamBits(64)
b.Return(b.FAdd(x, y))
program, err := b.Finish()
if err != nil { log.Fatal(err) }

code, err := compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC})
if err != nil { log.Fatal(err) }
defer code.Close()

// Words splits each 64-bit ('L') argument into two words on 32-bit hosts;
// CallPair and Wide return the 64-bit result portably.
lo, hi, err := code.CallPair(execution.Words("LL", math.Float64bits(1.5), math.Float64bits(2.25))...)
if err != nil { log.Fatal(err) }
fmt.Println(math.Float64frombits(execution.Wide(lo, hi))) // 3.75
```

`Code.Call` takes native ABI words (`uintptr`) and returns the first result word. Pointer and `ParamNative` arguments are one word everywhere. On 386, 8-, 16- and 32-bit parameters take one stack word; 64-bit parameters take two consecutive words (low first, as in cdecl). `CallPair` returns full 64-bit results from EAX:EDX for C entries or the two-word result slot for Go entries. `execution.Words` builds the argument words from a format of `l` (native word) and `L` (64-bit value) letters. On 64-bit hosts every value is one word.

Go ABI code can also be called directly as a typed Go function, without the call trampoline:

```go
code, err := compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIGo, Stack: nyanjit.StackPlatform, StackBytesAvailable: 240})
if err != nil { log.Fatal(err) }
add, err := execution.Func[func(uint64, uint64) uint64](code)
if err != nil { log.Fatal(err) }
fmt.Println(add(20, 22)) // 42
```

`Func` checks that the function type's parameters are integers matching the compiled parameter widths, and that it has at most one integer result. Floating point values still travel as bit patterns. The code must not need a workspace, so compile it with `StackPlatform`. The function value does not keep the `Code` mapped: code registered by `CompileGo` is never unmapped, but other code must stay open while the function may be called. Pass memory as a pinned `uintptr` rather than a Go pointer.

`FMA(x, y, z)` explicitly requests hardware-fused `x*y+z` with one rounding step. A deferred expression such as `x.FMul(y).FAdd(z)` is contracted to one FMA instruction when its multiply has one use and the CPU supports FMA; otherwise it remains a multiply followed by an add. Explicitly emitted `FMul` and `FAdd` retain separate rounding. Arithmetic follows the current CPU floating point environment.

`FSqrt(x)` uses hardware square root. `FConvert(x, bits)` converts between
binary32 and binary64. The math API also includes `FSin`, `FCos`, `FTan`,
`FAsin`, `FAcos`, `FAtan`, `FAtan2`, `FSinh`, `FCosh`, `FTanh`, their inverse
hyperbolic functions, `FExp`, `FExp2`, `FExpm1`, `FLog`, `FLog2`, `FLog10`,
`FLog1p`, `FPow`, `FCbrt`, `FHypot`, and floating point rounding functions.
These methods also compose deferred `Value` expressions.

Math functions lower to shared native helpers generated from Go's portable
math algorithms, including full-range trigonometric argument reduction and
special-value handling. Binary32 inputs are widened for evaluation and rounded
back to binary32. Helpers are bound automatically and remain mapped for the
process lifetime. Use `CompileForeign` with `StackPlatform` and a stack budget
for their generated calls; `FSqrt` and `FConvert` also work in leaf functions.

Floating arithmetic is currently emitted on amd64, arm64, riscv64, ppc64, and ppc64le. On 386 it reports a backend limit; explicit FMA also requires FMA3 on amd64. PPC64 and PPC64LE use POWER8 instructions for both FP32 and FP64, including single-precision FMA with one rounding step. `EmissionReport.FPStatusModified` reports possible floating point status changes separately from integer flags; `ClobberedFPRegisters` lists modified floating point registers.

## Lowering details

The path is **Builder → program snapshot → instruction selection → register allocation and spill scheduling → physical emitter → byte or text assembler**. Selection may fold arithmetic into memory addresses, use immediates, or reuse arithmetic flags for a following branch. Integer widths include 8, 16, 32, 64, and 128 bits; `NativeBits` follows the emitting process's pointer width. [The design document](docs/virtual-register-design.md) describes these stages and ABI constraints.

`CompileOptions.ABI` selects the generated function's entry convention. Each `CallC` or `CallGo` selects its callee convention independently. `compile.Compile` is for leaf functions; `CompileForeign` allows outbound C calls in a foreign context, and `CompileGo` registers a Go-managed frame for Go calls. Managed frames currently require pointer-free generated values and a supported Go runtime. `compile.PreflightCall` reports call locations and conservative clobbers; `compile.Analyze` reports the generated body's register, memory, stack, and workspace effects. `compile.EmitAssembly` prints the allocated instructions.

On PPC64, `CompileOptions.PPC64CABI` selects the C entry ELF variant. Unspecified follows the Go executable's platform convention: ELFv1 for big-endian Go 1.26, ELFv2 for Go 1.27 and PPC64LE. C callees choose independently: use `abi.PreflightCall(abi.CallSpec{ABI: abi.C, PPC64CABI: abi.PPC64ELFv1, ArgumentCount: n})` and `CallCWithEffect` for a descriptor pointer, or `PPC64ELFv2` for a global code entry. `Code.EntryAddress` always returns raw generated code; an ELFv1 C caller must supply a descriptor.

Set `CompileOptions.CPU.Level` to `"power8"`, `"power9"`, or `"power10"` to select the minimum instruction set. Zero selects POWER8. `PPC64CPU` remains a compatibility option. POWER9 and POWER10 contract a single-use integer multiply followed by add into `MADDLD`, including arbitrary multiplicands and widths of 8, 16, 32, or 64 bits; narrower results keep their modular arithmetic semantics. Native compilation checks kernel CPU capabilities and rejects an unavailable level. Cross-target assembly text can use any supported level. `EmissionReport.CPU` records the resolved level and extensions.

PPC64 arithmetic predicates reuse CR0 sign/zero and XER carry/borrow when their value and width still match. A decrement followed by a nonzero branch can emit `addic.` and `bne`. POWER9/10 can also reuse 32-bit carry through XER.CA32. Set `PreferCompactLoops: true` to select `mtctr` / `bdnz` for eligible countdown loops. Counter uses in the body, live decrement flags, calls, side entries and competing CTR loops retain ordinary arithmetic branches. `PreflightRegisterScopes` reports optional `CTR/BDNZ` opportunities; emitted reports list actual CTR clobbers.

For callbacks, `execution.RegisterForeignCallbackWithOptions(fn, execution.CallbackOptions{PPC64CABI: ...})` selects the C pointer convention independently of the Go binary. Pure-Go big-endian callbacks must be invoked through the runtime-aware C gateway (`CallForeign`, `CompileForeign`, or managed C calls); they recover the Go system-stack context through standard C back chains and preserve the full C nonvolatile register set. When real cgo is linked, C-created threads can enter through the callback trampoline; the runtime attaches them using its TLS path. Registered callback mappings remain alive for the process lifetime.

Managed PPC64 frames reserve all twelve Go argument homes, preserve the C nonvolatile GPR/FP/vector/condition resources for C entries, and are limited to 640 bytes. Supply at least that stack budget for a managed C entry.

Native loads and stores require an address valid for the entire access. `Store8`, `Store16`, and `Store32` accept their matching value widths as well as wider values whose low bytes are used. `Load64` and `Store64` use the target's native word representation; `LoadBits` and `StoreBits` use little-endian order at every value width, including 128 bits. IR memory operations record byte order explicitly so addressing selection cannot change it. `Code.CallInto` pins a byte buffer for the duration of one C ABI call.

### amd64 lowering examples

`compile.EmitAssembly` can emit a Linux target's listing from any host. Select the target with `CompileOptions.Architecture`; leave it empty to use the host platform. For example, after building and finishing a program:

```go
assembly, err := compile.EmitAssembly(program, nyanjit.CompileOptions{
    Architecture: "amd64",
    ABI: nyanjit.ABIC,
})
if err != nil { log.Fatal(err) }
fmt.Print(assembly)
```

Each row below starts with `b := nyanjit.NewBuilder()` and uses that configuration with the default `StackNoUse` policy. These listings were generated on a 64-bit host in Intel syntax (destination first), with byte offsets omitted. The C ABI supplies integer arguments in `RDI`, `RSI`, and `RDX` and returns the result in `RAX`. Variables can occupy registers or spill slots. These examples fit in registers; register choices can change as the allocator evolves.

| Example | Builder snippet | amd64 assembly | Lowering |
| --- | --- | --- | --- |
| Constant | `b.Return(b.Const(42))` | `mov EAX, 0x2a`<br>`ret` | A small constant uses a 32-bit move, which zero-extends into RAX. |
| Integer addition | `x, y := b.Param(), b.Param()`<br>`b.Return(b.Add(x, y))` | `mov RAX, RDI`<br>`add RAX, RSI`<br>`ret` | Arguments arrive in RDI and RSI; the result is computed in RAX. |
| Immediate addition | `x := b.Param()`<br>`b.Return(b.Add(x, b.Const(7)))` | `mov RAX, RDI`<br>`add RAX, 7`<br>`ret` | The constant becomes an immediate operand. |
| Mutable variable | `x := b.ParamNative()`<br>`v := b.Var(x)`<br>`b.Set(v, b.Add(b.Get(v), b.ConstNative(7)))`<br>`b.Return(b.Get(v))` | `add RDI, 7`<br>`mov RAX, RDI`<br>`ret` | The variable stays in RDI; Get and Set need no separate copies for this update. |
| Snapshot before Set | `x := b.ParamNative()`<br>`v := b.Var(x)`<br>`before := b.Get(v)`<br>`b.Set(v, b.Add(before, b.ConstNative(7)))`<br>`b.Return(b.Add(before, b.Get(v)))` | `mov RAX, RDI`<br>`add RAX, 7`<br>`mov RCX, RDI`<br>`mov RDI, RAX`<br>`mov RAX, RCX`<br>`add RAX, RDI`<br>`ret` | The old snapshot is preserved in RCX before Set replaces the variable's home; the result is `2*x + 7`. |
| Increment | `x := b.Param()`<br>`b.Return(b.Add(x, b.Const(1)))` | `mov RAX, RDI`<br>`inc RAX`<br>`ret` | Adding one selects inc. |
| Scaled expression | `base, index := b.Param(), b.Param()`<br>`b.Return(base.Add(index.MulConst(8)).Add(b.Literal(16)))` | `lea RAX, [RDI + RSI*8 + 16]`<br>`ret` | Multiply, add, and displacement combine into lea. |
| Folded memory address | `base, index := b.Param(), b.Param()`<br>`b.Return(b.Load64(base.Add(index.MulConst(8)).Add(b.Literal(16))))` | `mov RAX, qword ptr [RDI + RSI*8 + 16]`<br>`ret` | The address expression folds directly into the load. |
| Conditional selection | `condition, x, y := b.Param(), b.Param(), b.Param()`<br>`b.Return(b.Select(condition, x, y))` | `mov RAX, RDX`<br>`test RDI, RDI`<br>`cmovne RAX, RSI`<br>`ret` | A nonzero condition selects x with a conditional move. |
| Scope export | `x := b.ParamNative()`<br>`b.Enter()`<br>`shifted := b.Add(x, b.ConstNative(2))`<br>`scaled := b.Mul(shifted, b.ConstNative(3))`<br>`b.Leave(scaled)`<br>`b.Return(b.Add(scaled, x))` | `mov RAX, RDI`<br>`add RAX, 2`<br>`mov ECX, 0x3`<br>`imul RAX, RCX`<br>`add RAX, RDI`<br>`ret` | Enter and Leave control value lifetimes without adding scope instructions. |
| FP64 addition | `x, y := b.ParamBits(64), b.ParamBits(64)`<br>`b.Return(b.FAdd(x, y))` | `movq xmm0, RDI`<br>`movq xmm1, RSI`<br>`addsd xmm0, xmm1`<br>`movq RAX, xmm0`<br>`ret` | Integer-register bit patterns move through XMM registers for arithmetic. |

See [CPU profiles](docs/cpu-profiles.md) for ISA selection and [backend parity](docs/backend-parity.md) for native integer operations, flag reuse, and validation.

Supported assembly targets are `amd64`, `arm64`, `386`, `riscv64`, `ppc64`, and `ppc64le`. An explicit amd64 target uses baseline floating point instructions; set `CPU: abi.CPUProfile{Extensions: []string{"fma"}}` to permit FMA3/AVX and deferred multiply-add contraction. The default host target detects CPU features. `NativeBits` and `ParamNative` still follow the process building the IR, so choose explicit value widths for cross-target programs. Callable compilation and native byte emission require the host architecture. Managed C gateways appear as symbolic addresses in text listings.

## Examples

| Example | Run | Shows |
| --- | --- | --- |
| [add](examples/add/main.go) | `go run ./examples/add` | Builder, compilation, and `Code.Call`. |
| [scopes](examples/scopes/main.go) | `go run ./examples/scopes` | `Enter`/`Leave`, temporary lifetimes, and exporting a value. |
| [dctlogo](examples/dctlogo/main.go) | `go run ./examples/dctlogo` | Generated FP64 inverse DCT with square root, cosine, and nested loops. |
| [elfwrite](examples/elfwrite/main.go) | `go run ./examples/elfwrite /tmp/elfwrite` | A standalone Linux ELF and assembly listing. |
| [snake](examples/snake/main.go) | `go run ./examples/snake` | Game rules built with labels and predicates. |
| [snake_termbox](examples/snake_termbox/main.go) | `go run ./examples/snake_termbox` | Interactive rendering and managed Go ABI calls. |

## FAQ

**Can I inspect code without executing it?** Yes. Use `compile.EmitAssembly` for text or `compile.Emit` for bytes and an emission report.

**Can generated code call Go functions?** `CompileGo` registers supported pointer-free generated frames and can call Go functions from a Go-managed context. A C ABI entry may reach this path through a Go runtime callback trampoline. Arbitrary foreign threads cannot call Go directly through generated code.

**Why are some values invalid after `Bind`?** Ordinary values, including `Get` snapshots, belong to the block that defines them. Use a scoped `Var` and a fresh `Get` in the next block. Values captured by `Enter` remain usable throughout that entered scope until `Leave`.

**Which targets can execute code?** Linux/amd64 and Linux/arm64 are the primary targets. Linux/386, Linux/riscv64, Linux/ppc64, and Linux/ppc64le provide experimental backends. All support integer, memory, control-flow, and ABI operations; all except 386 also support FP32/FP64 arithmetic. The PPC64 backends require POWER8 or newer. Unsupported lowering returns an error.

## Cross-platform E2E tests

Run `./e2e/run.sh` to build the test container and execute the Starlark matrix.
Use `./e2e/run.sh e2e/smoke.star -os linux -arch riscv64` for one smoke
target. The container needs Docker, privileged mode, and `/dev/kvm`; it keeps
VM downloads in the `nyanjit-e2e-cache` volume. See [e2e/README.md](e2e/README.md)
for runner details, expected skips, results, and image licensing.

## License

nyanjit is licensed under the [Apache License, Version 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.
