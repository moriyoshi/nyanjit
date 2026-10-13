//! Cranelift counterpart of ../sljit/bench.c and ../nyanjit/main.go: the same
//! kernels built with FunctionBuilder, timed per phase (build, compile, bind)
//! with bench.c's method (7 rounds x 300 ms budget, median per phase, run as
//! best of 7 calls).

use std::time::Instant;

use cranelift_codegen::control::ControlPlane;
use cranelift_codegen::ir::condcodes::{FloatCC, IntCC};
use cranelift_codegen::ir::{types, AbiParam, InstBuilder, Signature, UserFuncName};
use cranelift_codegen::isa::OwnedTargetIsa;
use cranelift_codegen::settings::{self, Configurable};
use cranelift_codegen::Context;
use cranelift_frontend::{FunctionBuilder, FunctionBuilderContext, Variable};
use cranelift_jit::{ArenaMemoryProvider, JITBuilder, JITModule};
use cranelift_module::{default_libcall_names, Module};

const I64: types::Type = types::I64;
const F64: types::Type = types::F64;

struct Kernel {
    name: &'static str,
    ops: u32,
    build: fn(&mut FunctionBuilder),
    args: u32,
    a: u64,
    b: u64,
}

/// Creates the entry block with the function's parameters and switches to it.
fn entry(b: &mut FunctionBuilder) -> cranelift_codegen::ir::Block {
    let e = b.create_block();
    b.append_block_params_for_function_params(e);
    b.switch_to_block(e);
    b.seal_block(e);
    e
}

fn tiny(b: &mut FunctionBuilder) {
    let e = entry(b);
    let (x, y) = (b.block_params(e)[0], b.block_params(e)[1]);
    let r = b.ins().iadd(x, y);
    b.ins().return_(&[r]);
}

fn chain(b: &mut FunctionBuilder, n: i64) {
    let e = entry(b);
    let mut x = b.block_params(e)[0];
    for i in 0..n {
        x = b.ins().imul_imm_s(x, 3);
        let t = b.ins().ushr_imm_s(x, 5);
        x = b.ins().bxor(x, t);
        x = b.ins().iadd_imm_s(x, i);
    }
    b.ins().return_(&[x]);
}
fn chain100(b: &mut FunctionBuilder) {
    chain(b, 100)
}
fn chain1000(b: &mut FunctionBuilder) {
    chain(b, 1000)
}

fn var(b: &mut FunctionBuilder, init: i64) -> Variable {
    let v = b.declare_var(I64);
    let c = b.ins().iconst(I64, init);
    b.def_var(v, c);
    v
}

fn lp(b: &mut FunctionBuilder) {
    let e = entry(b);
    let n = b.block_params(e)[0];
    let acc = var(b, 0);
    let i = var(b, 0);
    let (top, body, exit) = (b.create_block(), b.create_block(), b.create_block());
    b.ins().jump(top, &[]);

    b.switch_to_block(top);
    let iv = b.use_var(i);
    let c = b.ins().icmp(IntCC::UnsignedGreaterThanOrEqual, iv, n);
    b.ins().brif(c, exit, &[], body, &[]);

    b.switch_to_block(body);
    b.seal_block(body);
    let iv = b.use_var(i);
    let sq = b.ins().imul(iv, iv);
    let sh = b.ins().ushr_imm_s(iv, 3);
    let t = b.ins().bxor(sq, sh);
    let av = b.use_var(acc);
    let av = b.ins().iadd(av, t);
    b.def_var(acc, av);
    let iv = b.ins().iadd_imm_s(iv, 1);
    b.def_var(i, iv);
    b.ins().jump(top, &[]);
    b.seal_block(top);

    b.switch_to_block(exit);
    b.seal_block(exit);
    let av = b.use_var(acc);
    b.ins().return_(&[av]);
}

const VARS: usize = 16;

fn pressure(b: &mut FunctionBuilder) {
    let e = entry(b);
    let n = b.block_params(e)[0];
    let a: Vec<Variable> = (0..VARS).map(|k| var(b, k as i64 + 1)).collect();
    let i = var(b, 0);
    let (top, body, exit) = (b.create_block(), b.create_block(), b.create_block());
    b.ins().jump(top, &[]);

    b.switch_to_block(top);
    let iv = b.use_var(i);
    let c = b.ins().icmp(IntCC::UnsignedGreaterThanOrEqual, iv, n);
    b.ins().brif(c, exit, &[], body, &[]);

    b.switch_to_block(body);
    b.seal_block(body);
    // Sequential updates: a_15 reads the already-updated a_0, as in bench.c.
    for k in 0..VARS {
        let next = b.use_var(a[(k + 1) % VARS]);
        let iv = b.use_var(i);
        let t = b.ins().bxor(next, iv);
        let cur = b.use_var(a[k]);
        let s = b.ins().iadd(cur, t);
        b.def_var(a[k], s);
    }
    let iv = b.use_var(i);
    let iv = b.ins().iadd_imm_s(iv, 1);
    b.def_var(i, iv);
    b.ins().jump(top, &[]);
    b.seal_block(top);

    b.switch_to_block(exit);
    b.seal_block(exit);
    let mut x = b.use_var(a[0]);
    for v in &a[1..] {
        let y = b.use_var(*v);
        x = b.ins().bxor(x, y);
    }
    b.ins().return_(&[x]);
}

fn collatz(b: &mut FunctionBuilder) {
    let e = entry(b);
    let n = b.block_params(e)[0];
    let x = var(b, 1);
    let total = var(b, 0);
    let y = b.declare_var(I64);
    let outer = b.create_block();
    let outer_body = b.create_block();
    let inner = b.create_block();
    let inner_body = b.create_block();
    let odd = b.create_block();
    let even = b.create_block();
    let one = b.create_block();
    let done = b.create_block();
    b.ins().jump(outer, &[]);

    // outer: if x > n goto done; y = x
    b.switch_to_block(outer);
    let xv = b.use_var(x);
    let c = b.ins().icmp(IntCC::UnsignedGreaterThan, xv, n);
    b.ins().brif(c, done, &[], outer_body, &[]);

    b.switch_to_block(outer_body);
    b.seal_block(outer_body);
    let xv = b.use_var(x);
    b.def_var(y, xv);
    b.ins().jump(inner, &[]);

    // inner: if y == 1 goto one; if y & 1 == 0 goto even
    b.switch_to_block(inner);
    let yv = b.use_var(y);
    let c = b.ins().icmp_imm_s(IntCC::Equal, yv, 1);
    b.ins().brif(c, one, &[], inner_body, &[]);

    b.switch_to_block(inner_body);
    b.seal_block(inner_body);
    let yv = b.use_var(y);
    let low = b.ins().band_imm_s(yv, 1);
    b.ins().brif(low, odd, &[], even, &[]);

    // odd: y = y*3 + 1; total++
    b.switch_to_block(odd);
    b.seal_block(odd);
    let yv = b.use_var(y);
    let yv = b.ins().imul_imm_s(yv, 3);
    let yv = b.ins().iadd_imm_s(yv, 1);
    b.def_var(y, yv);
    let tv = b.use_var(total);
    let tv = b.ins().iadd_imm_s(tv, 1);
    b.def_var(total, tv);
    b.ins().jump(inner, &[]);

    // even: y >>= 1; total++
    b.switch_to_block(even);
    b.seal_block(even);
    let yv = b.use_var(y);
    let yv = b.ins().ushr_imm_s(yv, 1);
    b.def_var(y, yv);
    let tv = b.use_var(total);
    let tv = b.ins().iadd_imm_s(tv, 1);
    b.def_var(total, tv);
    b.ins().jump(inner, &[]);
    b.seal_block(inner);

    // one: x++
    b.switch_to_block(one);
    b.seal_block(one);
    let xv = b.use_var(x);
    let xv = b.ins().iadd_imm_s(xv, 1);
    b.def_var(x, xv);
    b.ins().jump(outer, &[]);
    b.seal_block(outer);

    b.switch_to_block(done);
    b.seal_block(done);
    let tv = b.use_var(total);
    b.ins().return_(&[tv]);
}

static FP_INLINE: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

const COEFFICIENTS: [f64; 9] = [0.1, -0.2, 0.3, -0.4, 0.5, -0.6, 0.7, -0.8, 0.9];

/// Constants are materialised once in the entry block, as bench.c hoists
/// them into FR2.. (CRANELIFT_FP_INLINE=1 emits them at their use instead).
fn fp(b: &mut FunctionBuilder) {
    let inline = FP_INLINE.load(std::sync::atomic::Ordering::Relaxed);
    let e = entry(b);
    let n = b.block_params(e)[0];
    let count = var(b, 0);
    let i = var(b, 0);
    let mut consts: Vec<Option<_>> = vec![None; COEFFICIENTS.len() + 2];
    if !inline {
        consts[0] = Some(b.ins().f64const(0.001));
        consts[1] = Some(b.ins().f64const(0.5));
        for (k, c) in COEFFICIENTS.iter().enumerate() {
            consts[2 + k] = Some(b.ins().f64const(*c));
        }
    }
    let k = |b: &mut FunctionBuilder, idx: usize, v: f64| match consts[idx] {
        Some(c) => c,
        None => b.ins().f64const(v),
    };
    let (top, body, hit, miss, exit) = (
        b.create_block(),
        b.create_block(),
        b.create_block(),
        b.create_block(),
        b.create_block(),
    );
    b.ins().jump(top, &[]);

    b.switch_to_block(top);
    let iv = b.use_var(i);
    let c = b.ins().icmp(IntCC::UnsignedGreaterThanOrEqual, iv, n);
    b.ins().brif(c, exit, &[], body, &[]);

    b.switch_to_block(body);
    b.seal_block(body);
    let iv = b.use_var(i);
    let xf = b.ins().fcvt_from_sint(F64, iv);
    let scale = k(b, 0, 0.001);
    let x = b.ins().fmul(xf, scale);
    let mut y = k(b, 2, COEFFICIENTS[0]);
    for (idx, c) in COEFFICIENTS.iter().enumerate().skip(1) {
        y = b.ins().fmul(y, x);
        let cv = k(b, 2 + idx, *c);
        y = b.ins().fadd(y, cv);
    }
    // count++ unless unordered or y <= 0.5, i.e. iff y > 0.5 (ordered).
    let half = k(b, 1, 0.5);
    let gt = b.ins().fcmp(FloatCC::GreaterThan, y, half);
    b.ins().brif(gt, hit, &[], miss, &[]);

    b.switch_to_block(hit);
    b.seal_block(hit);
    let cv = b.use_var(count);
    let cv = b.ins().iadd_imm_s(cv, 1);
    b.def_var(count, cv);
    b.ins().jump(miss, &[]);

    b.switch_to_block(miss);
    b.seal_block(miss);
    let iv = b.use_var(i);
    let iv = b.ins().iadd_imm_s(iv, 1);
    b.def_var(i, iv);
    b.ins().jump(top, &[]);
    b.seal_block(top);

    b.switch_to_block(exit);
    b.seal_block(exit);
    let cv = b.use_var(count);
    b.ins().return_(&[cv]);
}

const KERNELS: &[Kernel] = &[
    Kernel { name: "tiny", ops: 1, build: tiny, args: 2, a: 3, b: 4 },
    Kernel { name: "chain100", ops: 400, build: chain100, args: 1, a: 12345, b: 0 },
    Kernel { name: "chain1000", ops: 4000, build: chain1000, args: 1, a: 12345, b: 0 },
    Kernel { name: "loop", ops: 6, build: lp, args: 1, a: 10_000_000, b: 0 },
    Kernel { name: "pressure", ops: 2 * VARS as u32 + 3, build: pressure, args: 1, a: 1_000_000, b: 0 },
    Kernel { name: "collatz", ops: 12, build: collatz, args: 1, a: 100_000, b: 0 },
    Kernel { name: "fp", ops: 2 * COEFFICIENTS.len() as u32 + 6, build: fp, args: 1, a: 1_000_000, b: 0 },
];

#[derive(Clone, Copy, PartialEq)]
enum BindMode {
    /// A fresh JITModule (default SystemMemoryProvider) per function; its
    /// memory is released with free_memory() outside the timed region.
    Fresh,
    /// One JITModule (SystemMemoryProvider) shared by all functions, freed and
    /// recreated every RECYCLE functions outside the timed region.
    Shared,
    /// Like Shared but with an ArenaMemoryProvider.
    Arena,
}

const RECYCLE: usize = 1024;
const ARENA_SIZE: usize = 2 * RECYCLE * 4096 + (64 << 20);

struct Binder {
    isa: OwnedTargetIsa,
    mode: BindMode,
    module: Option<JITModule>,
    defined: usize,
}

impl Binder {
    fn new_module(&self) -> JITModule {
        let mut jb = JITBuilder::with_isa(self.isa.clone(), default_libcall_names());
        if self.mode == BindMode::Arena {
            jb.memory_provider(Box::new(ArenaMemoryProvider::new_with_size(ARENA_SIZE).unwrap()));
        }
        JITModule::new(jb)
    }

    /// Untimed: make sure a module with room is ready.
    fn prepare(&mut self) {
        if self.mode != BindMode::Fresh && (self.module.is_none() || self.defined >= RECYCLE) {
            if let Some(m) = self.module.take() {
                unsafe { m.free_memory() };
            }
            self.module = Some(self.new_module());
            self.defined = 0;
        }
    }

    /// Timed: copy the compiled bytes into executable memory.
    fn bind(&mut self, sig: &Signature, align: u64, bytes: &[u8]) -> *const u8 {
        if self.mode == BindMode::Fresh {
            self.module = Some(self.new_module());
        }
        let m = self.module.as_mut().unwrap();
        let id = m.declare_anonymous_function(sig).unwrap();
        m.define_function_bytes(id, align, bytes, &[]).unwrap();
        m.finalize_definitions().unwrap();
        self.defined += 1;
        m.get_finalized_function(id)
    }

    /// Untimed: release what bind() made, where the mode allows it.
    fn release(&mut self) {
        if self.mode == BindMode::Fresh {
            unsafe { self.module.take().unwrap().free_memory() };
        }
    }
}

struct Jit {
    isa: OwnedTargetIsa,
    ctx: Context,
    fctx: FunctionBuilderContext,
}

impl Jit {
    /// build: signature + IR through FunctionBuilder, finalize included.
    fn build(&mut self, k: &Kernel) -> Signature {
        self.ctx.clear();
        let mut sig = Signature::new(self.isa.default_call_conv());
        for _ in 0..k.args {
            sig.params.push(AbiParam::new(I64));
        }
        sig.returns.push(AbiParam::new(I64));
        self.ctx.func.signature = sig.clone();
        self.ctx.func.name = UserFuncName::user(0, 0);
        let mut b = FunctionBuilder::new(&mut self.ctx.func, &mut self.fctx);
        (k.build)(&mut b);
        b.finalize(self.isa.frontend_config());
        sig
    }

    /// compile: Context::compile to machine code; returns (alignment, size).
    fn compile(&mut self) -> (u64, usize) {
        let code = self.ctx.compile(&*self.isa, &mut ControlPlane::default()).unwrap();
        assert!(code.buffer.relocs().is_empty(), "unexpected relocations");
        let align = (code.buffer.min_alignment as u64).max(self.isa.function_alignment().minimum as u64);
        (align, code.code_buffer().len())
    }

    fn bytes(&self) -> &[u8] {
        self.ctx.compiled_code().unwrap().code_buffer()
    }
}

fn median(xs: &mut [f64]) -> f64 {
    xs.sort_by(|a, b| a.partial_cmp(b).unwrap());
    xs[xs.len() / 2]
}

fn usage() -> ! {
    eprintln!("usage: clbench [--config speed|fast] [-k kernel] [--bind fresh|shared|arena] [-t ms] [-r rounds] [--verify] [--no-header] [--dump dir]");
    std::process::exit(2)
}

fn main() {
    let mut config = "speed".to_string();
    let mut only: Option<String> = None;
    let mut mode = BindMode::Fresh;
    let mut budget_ms = 300.0f64;
    let mut rounds = 7usize;
    let mut verify = false;
    let mut header = true;
    let mut dump: Option<String> = None;
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        match a.as_str() {
            "--config" | "-c" => config = args.next().unwrap_or_else(|| usage()),
            "-k" => only = Some(args.next().unwrap_or_else(|| usage())),
            "--bind" => {
                mode = match args.next().as_deref() {
                    Some("fresh") => BindMode::Fresh,
                    Some("shared") => BindMode::Shared,
                    Some("arena") => BindMode::Arena,
                    _ => usage(),
                }
            }
            "-t" => budget_ms = args.next().and_then(|s| s.parse().ok()).unwrap_or_else(|| usage()),
            "-r" => rounds = args.next().and_then(|s| s.parse().ok()).unwrap_or_else(|| usage()),
            "--verify" => verify = true,
            "--no-header" => header = false,
            "--dump" => dump = Some(args.next().unwrap_or_else(|| usage())),
            _ => usage(),
        }
    }

    FP_INLINE.store(std::env::var_os("CRANELIFT_FP_INLINE").is_some(), std::sync::atomic::Ordering::Relaxed);
    let mut flags = settings::builder();
    match config.as_str() {
        "speed" => flags.set("opt_level", "speed").unwrap(),
        "fast" => {
            flags.set("opt_level", "none").unwrap();
            flags.set("regalloc_algorithm", "single_pass").unwrap();
        }
        _ => usage(),
    }
    // Same as JITBuilder::with_flags on aarch64.
    flags.set("use_colocated_libcalls", "false").unwrap();
    flags.set("is_pic", "false").unwrap();
    flags.set("enable_verifier", if verify { "true" } else { "false" }).unwrap();
    let isa = cranelift_native::builder()
        .unwrap()
        .finish(settings::Flags::new(flags))
        .unwrap();

    let mut jit = Jit { isa: isa.clone(), ctx: Context::new(), fctx: FunctionBuilderContext::new() };
    let mut binder = Binder { isa, mode, module: None, defined: 0 };
    let budget = budget_ms * 1e6;

    if header {
        println!(
            "{:<10} {:<6} {:>10} {:>10} {:>10} {:>10} {:>9} {:>8} {:>12} {}",
            "kernel", "config", "build", "compile", "bind", "total", "ns/op", "bytes", "run", "result"
        );
    }
    for k in KERNELS {
        if only.as_deref().is_some_and(|o| o != k.name) {
            continue;
        }
        if let Some(dir) = &dump {
            jit.build(k);
            jit.ctx.set_disasm(true);
            jit.compile();
            let cc = jit.ctx.compiled_code().unwrap();
            let base = format!("{dir}/{}-{config}", k.name);
            std::fs::write(format!("{base}.vcode"), cc.vcode.as_deref().unwrap_or("")).unwrap();
            std::fs::write(format!("{base}.bin"), cc.code_buffer()).unwrap();
            std::fs::write(format!("{base}.clif"), format!("{}", jit.ctx.func.display())).unwrap();
            jit.ctx.set_disasm(false);
            continue;
        }
        let (mut bs, mut cs, mut ds) = (vec![], vec![], vec![]);
        let mut size = 0;
        for _ in 0..rounds {
            let (mut sb, mut sc, mut sd) = (0f64, 0f64, 0f64);
            let mut n = 0u64;
            let start = Instant::now();
            while n == 0 || (start.elapsed().as_nanos() as f64) < budget {
                binder.prepare();
                let t0 = Instant::now();
                let sig = jit.build(k);
                let t1 = Instant::now();
                let (align, sz) = jit.compile();
                let t2 = Instant::now();
                let p = binder.bind(&sig, align, jit.bytes());
                let t3 = Instant::now();
                std::hint::black_box(p);
                binder.release();
                size = sz;
                sb += (t1 - t0).as_nanos() as f64;
                sc += (t2 - t1).as_nanos() as f64;
                sd += (t3 - t2).as_nanos() as f64;
                n += 1;
            }
            let n = n as f64;
            bs.push(sb / n);
            cs.push(sc / n);
            ds.push(sd / n);
        }
        let (b, c, d) = (median(&mut bs), median(&mut cs), median(&mut ds));
        let total = b + c + d;

        // Run: compile once more through the same path, best of 7 calls.
        binder.prepare();
        let sig = jit.build(k);
        let (align, _) = jit.compile();
        let p = binder.bind(&sig, align, jit.bytes());
        let mut best = f64::MAX;
        let mut result = 0u64;
        for _ in 0..7 {
            let t0 = Instant::now();
            result = unsafe {
                if k.args == 2 {
                    let f: extern "C" fn(u64, u64) -> u64 = std::mem::transmute(p);
                    f(std::hint::black_box(k.a), std::hint::black_box(k.b))
                } else {
                    let f: extern "C" fn(u64) -> u64 = std::mem::transmute(p);
                    f(std::hint::black_box(k.a))
                }
            };
            best = best.min(t0.elapsed().as_nanos() as f64);
        }
        binder.release();
        println!(
            "{:<10} {:<6} {:>8.2}us {:>8.2}us {:>8.2}us {:>8.2}us {:>9.1} {:>8} {:>10.3}us {}",
            k.name,
            config,
            b / 1e3,
            c / 1e3,
            d / 1e3,
            total / 1e3,
            total / k.ops as f64,
            size,
            best / 1e3,
            result
        );
    }
}
