package compile

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"slices"
	"sync"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetarm64 "github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/ir"
)

// baselineEmitState holds the reusable encoders and the final layout.
type baselineEmitState struct {
	asm        *targetarm64.ByteAssembler
	asmStack   abi.StackPractice
	asmHomes   int
	sizer      *targetarm64.ByteAssembler
	sizerStack abi.StackPractice
	sizerHomes int
	sizeErr    bool
	phys       []bPhys
	labels     []int32
	// pool holds the literal pool's entries from poolBase; poolIndex maps
	// a value to its entry.
	pool      []uint64
	poolIndex map[uint64]int32
	poolBase  int
	poolBytes []byte
	// arch is architecture-private reusable state, such as encoders.
	arch any
	// amd64 is amd64's reusable encoder state.
	amd64 amd64BaselineEmit
	// i386 is 386's reusable encoder state.
	i386 *i386EmitState
}

// baselineUnit is the output of a baseline lowering.
type baselineUnit struct {
	options    compileOptions
	arch       baselineArch
	m          baselineModel
	phys       []bPhys
	labels     []int32
	symbols    []string
	pool       []uint64
	poolBase   int
	code       []byte
	relocs     []machine.Relocation
	arity      int
	paramBits  []uint8
	paramTypes []reflect.Type
	boundary   callableBoundary
	// architecture, entry and facts are what the report is built from:
	// the frame part a Template needs with the unit, the rest on first use.
	architecture     string
	entry            abiEntryLayout
	facts            baselineFacts
	vectorOperations []abi.VectorOperation
	reportOnce       sync.Once
	report           abi.Report
	native           bool

	listingOnce  sync.Once
	listing      string
	listingErr   error
	templateOnce sync.Once
	template     *execution.Template
	templateErr  error
}

// unit runs analysis, allocation and emission and returns the Unit.
func (e *baselineEngine) unit() (*Unit, error) {
	if len(e.f.Constants) != 0 {
		for name := range e.f.Constants {
			if !e.usedSymbols[name] {
				return nil, ErrInvalidFunction
			}
		}
	}
	if err := e.checkCallable(); err != nil {
		return nil, err
	}
	if err := e.analyze(); err != nil {
		return nil, err
	}
	if err := e.allocate(); err != nil {
		return nil, err
	}
	return e.finish()
}

// checkCallable applies Lower's context checks.
func (e *baselineEngine) checkCallable() error {
	o := &e.options
	arch := e.target.architecture
	if len(e.params) > abi.MaxArgumentsFor(arch) {
		return entryArityError(len(e.params), arch)
	}
	boundary := e.f.Context.boundary()
	switch boundary {
	case boundaryForeign:
		if e.partsReturns != 0 {
			return ErrBackendLimit
		}
		if o.ABI != abi.C {
			return ErrUnsupportedABI
		}
		if o.Stack != abi.StackPlatform || o.StackPointer != abi.StackPointerReserved {
			return ErrStackRequired
		}
	}
	if boundary != boundaryExternal {
		if e.hasSyscall || boundary == boundaryLeaf && e.hasCall || boundary == boundaryForeign && e.hasGoCall {
			return ErrExternalCallOnly
		}
	}
	if e.hasCall && o.Stack != abi.StackPlatform {
		return ErrStackRequired
	}
	if e.hasGoCall && o.ABI != abi.Go {
		return ErrExternalCallOnly
	}
	return nil
}

// finish lays out and encodes the function and builds its Unit.
func (e *baselineEngine) finish() (*Unit, error) {
	a := &e.alloc
	s := &e.emit
	if s.sizeErr {
		s.sizeErr = false
		return nil, fmt.Errorf("baseline: an operation does not encode: %w", ErrBackendLimit)
	}
	// Callee-saved registers the body writes are saved in the frame.
	saved := a.used & e.calleeSavedMask()
	saveBase := a.nslots
	slots := int(a.nslots) + bits.OnesCount64(saved)
	if slots > e.m.spillLimit {
		return nil, spillStorageLimitError(slots, int(a.nslots), e.m.spillLimit)
	}
	frame, err := e.arch.frame(e, slots, a.hasCall)
	if err != nil {
		return nil, err
	}
	start := a.reserve
	if frame > 0 {
		start--
	}
	start -= bits.OnesCount64(saved)
	pro := a.main[start:start]
	add := func(p bPhys) {
		// Sized in place: &p passed to the interface method would escape.
		pro = append(pro, p)
		if q := &pro[len(pro)-1]; q.sz == 0 {
			q.sz = uint8(e.arch.size(e, q))
		}
	}
	if frame > 0 {
		add(bPhys{k: pkFrameEnter, imm: uint64(frame)})
	}
	slot := saveBase
	for m := saved; m != 0; m &= m - 1 {
		add(bPhys{k: pkSpill, a: int8(bits.TrailingZeros64(m)), imm: uint64(slot)})
		slot++
	}
	// The body follows the prologue in place.
	pro = a.main[start:]
	add(bPhys{k: pkLabel, imm: uint64(a.epilogue)})
	slot = saveBase
	for m := saved; m != 0; m &= m - 1 {
		add(bPhys{k: pkReload, a: int8(bits.TrailingZeros64(m)), imm: uint64(slot)})
		slot++
	}
	if frame > 0 {
		add(bPhys{k: pkFrameLeave, imm: uint64(frame)})
	}
	add(bPhys{k: pkRet})
	pro = append(pro, a.stubs...)
	if full := a.main[:cap(a.main)]; start >= len(full) || &pro[0] != &full[start] {
		// The epilogue or the stubs outgrew main: keep the larger buffer.
		a.main = pro[:0]
	}
	s.phys = pro
	// Literal pool entries, deduplicated.
	s.pool = s.pool[:0]
	if e.literals {
		e.collectLiterals(pro)
	}
	// Label PCs, then long forms for conditional branches out of range.
	s.labels = growInt32(s.labels, int(a.labels))
	size := 0
	for i := range pro {
		p := &pro[i]
		if p.k == pkLabel {
			s.labels[p.imm] = int32(size)
		}
		size += int(p.sz)
	}
	if len(s.pool) != 0 {
		// Relaxation at most doubles the code; loads that may not reach
		// the pool from there take their long form.
		if lit := e.arch.(baselineLiteralArch); lit.literalsLong(2*size + 16 + 8*len(s.pool)) {
			size = 0
			for i := range pro {
				p := &pro[i]
				if p.k == pkLiteral {
					p.sub = 1
					p.sz = uint8(e.arch.size(e, p))
				}
				if p.k == pkLabel {
					s.labels[p.imm] = int32(size)
				}
				size += int(p.sz)
			}
		}
	}
	relax, _ := e.arch.(baselineRelaxer)
	for changed := e.branches || relax != nil; changed; {
		changed = false
		pc := 0
		for i := range pro {
			p := &pro[i]
			if p.k == pkJumpIf && e.arch.longBranch(p, pc, int(s.labels[p.imm])) {
				p.k, p.sz, changed = pkJumpIfLong, uint8(e.arch.size(e, &bPhys{k: pkJumpIfLong})), true
			} else if relax != nil && (p.k == pkJump || p.k == pkJumpIfLong) && relax.relax(e, p, pc, int(s.labels[p.imm])) {
				changed = true
			}
			pc += int(p.sz)
		}
		if changed {
			size = 0
			for i := range pro {
				p := &pro[i]
				if p.k == pkLabel {
					s.labels[p.imm] = int32(size)
				}
				size += int(p.sz)
			}
		}
	}
	if size >= 1<<27 {
		for i, pc := 0, 0; i < len(pro); i++ {
			p := &pro[i]
			if p.k == pkJump || p.k == pkJumpIfLong {
				from := pc
				if p.k == pkJumpIfLong {
					from += 4
				}
				if d := (int(s.labels[p.imm]) - from) / 4; d < -(1<<25) || d >= 1<<25 {
					return nil, &LimitError{Kind: LimitBranchRange, Needed: d, Limit: 1 << 25, Arch: e.target.architecture, Detail: "baseline branch"}
				}
			}
			pc += int(p.sz)
		}
	}
	code := size
	s.poolBase = 0
	if len(s.pool) != 0 {
		s.poolBase = (size + 15) &^ 15
		size = s.poolBase + 8*len(s.pool)
	}
	enc := e.arch.encoder(e, false)
	if _, err := e.arch.encode(e, enc, pro, 0, s.labels); err != nil {
		return nil, baselineEncodeError(err)
	}
	if len(s.pool) != 0 {
		if err := e.arch.(baselineLiteralArch).encodePool(e, enc, code, s.poolBase, s.pool); err != nil {
			return nil, baselineEncodeError(err)
		}
	}
	if err := enc.Err(); err != nil {
		return nil, baselineEncodeError(err)
	}
	if enc.AssembledSize() != size {
		return nil, fmt.Errorf("baseline: encoded %d bytes, laid out %d: %w", enc.AssembledSize(), size, ErrInvalidFunction)
	}
	out := enc.(interface {
		Bytes() []byte
		Relocations() []machine.Relocation
	})
	o := &e.options
	if e.partsReturns != 0 && o.ABI != abi.C {
		return nil, ErrBackendLimit
	}
	target := newMachineTarget(*o)
	words := make([]int, len(e.params))
	regBits := int(target.RegisterBits())
	for k := range words {
		words[k] = (int(e.recs[e.params[k]].bits) + regBits - 1) / regBits
	}
	entry, err := entryLayout(target, words)
	if err != nil {
		return nil, err
	}
	// One allocation holds the Unit and its baseline part.
	storage := &baselineUnitStorage{}
	u := &storage.base
	*u = baselineUnit{
		options: e.options, arch: e.arch, m: e.m, architecture: e.target.architecture,
		phys: slices.Clone(pro), labels: slices.Clone(s.labels), symbols: slices.Clone(e.symbols),
		pool: slices.Clone(s.pool), poolBase: s.poolBase,
		code: out.Bytes(), relocs: slices.Clone(out.Relocations()),
		arity: len(e.params), paramTypes: e.paramTypes, boundary: e.f.Context.boundary(), native: e.target.native,
		entry: entry,
		facts: baselineFacts{
			blocks: len(e.blocks), frame: frame, slots: slots,
			used: a.used, callers: a.callers, flags: a.flags, framedCall: a.hasCall,
			twoResults: e.partsReturns != 0, hasCall: e.hasCall, hasGoCall: e.hasGoCall,
			hasSyscall: e.hasSyscall, hasFunctionCall: e.hasFunctionCall, fpArith: e.fpArith,
			memRead: e.memRead, memWrite: e.memWrite, loads: e.loads, stores: e.stores,
		},
	}
	if len(u.paramTypes) == 0 {
		u.paramTypes = nil
	}
	u.paramBits = make([]uint8, len(e.params))
	for k, v := range e.params {
		u.paramBits[k] = e.recs[v].bits
	}
	storage.unit = Unit{baseline: u, view: baselineReport{u}, options: e.options, boundary: u.boundary}
	return &storage.unit, nil
}

// baselineUnitStorage allocates a baseline Unit together with its code.
type baselineUnitStorage struct {
	unit Unit
	base baselineUnit
}

// baselineFacts are the allocation and effect facts of a baseline function
// that its report is built from.
type baselineFacts struct {
	blocks, frame, slots int
	// used are the registers the body writes, callers those its calls
	// clobber; flags reports a flags write and framedCall a lowered call.
	used, callers      uint64
	flags, framedCall  bool
	twoResults         bool
	hasCall, hasGoCall bool
	hasSyscall         bool
	hasFunctionCall    bool
	fpArith            bool
	memRead, memWrite  bool
	loads, stores      bool
}

func baselineEncodeError(err error) error {
	if errors.Is(err, ErrInvalidFunction) || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrBackendLimit) {
		return err
	}
	return fmt.Errorf("baseline encoding: %w: %w", ErrBackendLimit, err)
}

// frameReport is the part of the report a Template needs: the entry and
// result layout and the stack the code uses.
func (u *baselineUnit) frameReport() abi.EmissionReport {
	f := &u.facts
	r := abi.EmissionReport{
		Architecture: u.architecture, ArgumentLocations: u.entry.arguments,
		ResultLocation: u.entry.result, ResultWords: 1, StackBytes: f.frame,
	}
	if f.twoResults {
		r.ResultWords = 2
		r.ResultLocation.Words = []abi.WordLocation{
			{Storage: abi.Register, Register: u.arch.registerName(u.m.results[0])},
			{Storage: abi.Register, Register: u.arch.registerName(u.m.results[1])},
		}
	}
	if u.options.Stack != abi.StackPlatform && f.slots != 0 {
		r.WorkspaceBytes = f.slots * 8
	}
	if f.framedCall {
		r.CallStackBytes = u.m.callPush
	}
	return r
}

// buildReport builds the unit's resource and effect report.
func (u *baselineUnit) buildReport() abi.EmissionReport {
	f := &u.facts
	o := &u.options
	r := u.frameReport()
	r.LoweringStats = abi.LoweringStats{Blocks: f.blocks, Allocations: 1}
	r.CPU, r.ABI = reportCPU(*o), o.ABI
	r.Stack, r.StackPointer = o.Stack, o.StackPointer
	r.ArgumentRegisters = make([]string, u.arity)
	r.ResultRegister = singleABIRegister(u.entry.result)
	r.FlagsClobbered, r.CodeBytes, r.WorkspaceAlignment = f.flags, len(u.code), 8
	r.SpillStorage = abi.SpillNone
	r.FPStatusModified = f.fpArith || f.hasCall
	r.MayReadMemory, r.MayWriteMemory = f.memRead, f.memWrite
	r.HasSystemCalls, r.HasFunctionCalls = f.hasSyscall, f.hasFunctionCall
	r.ExplicitMemoryReads, r.ExplicitMemoryWrites = f.loads, f.stores
	r.VectorOperations = u.vectorOperations
	for k := range r.ArgumentRegisters {
		r.ArgumentRegisters[k] = singleABIRegister(u.entry.arguments[k])
	}
	if r.WorkspaceBytes != 0 {
		target := newMachineTarget(*o)
		r.WorkspaceRegister = target.RegisterName(target.WorkspaceRegister())
		r.SpillStorage = abi.SpillWorkspace
	}
	if f.frame != 0 && f.slots != 0 {
		r.SpillStorage = abi.SpillStack
	}
	saved := u.m.saved
	written := f.used | f.callers
	adjust, own := u.arch.(baselineReporter)
	if o.Stack == abi.StackNoUse && !own {
		written &^= regBit(8)
	}
	for m := written &^ saved & bankMaskGP; m != 0; m &= m - 1 {
		r.ClobberedRegisters = append(r.ClobberedRegisters, u.arch.registerName(int8(bits.TrailingZeros64(m))))
	}
	for m := written &^ saved &^ bankMaskGP; m != 0; m &= m - 1 {
		r.ClobberedFPRegisters = append(r.ClobberedFPRegisters, u.arch.registerName(int8(bits.TrailingZeros64(m))))
	}
	if f.framedCall && u.architecture == "arm64" {
		r.ClobberedRegisters = append(r.ClobberedRegisters, "X30")
	}
	if own {
		adjust.adjustReport(u, &r)
	}
	return r
}

// reportView builds the report on first use.
func (u *baselineUnit) reportView() abi.Report {
	u.reportOnce.Do(func() { u.report = abi.ReportOf(u.buildReport()) })
	return u.report
}

// collectLiterals gives each pkLiteral in ps its pool entry, replacing the
// value in imm with the entry's index.
func (e *baselineEngine) collectLiterals(ps []bPhys) {
	s := &e.emit
	for i := range ps {
		p := &ps[i]
		if p.k != pkLiteral {
			continue
		}
		if s.poolIndex == nil {
			s.poolIndex = map[uint64]int32{}
		}
		if len(s.pool) == 0 {
			clear(s.poolIndex)
		}
		index, ok := s.poolIndex[p.imm]
		if !ok {
			index = int32(len(s.pool))
			s.poolIndex[p.imm] = index
			s.pool = append(s.pool, p.imm)
		}
		p.imm = uint64(index)
	}
}

// renderListing replays the physical records into the text assembler.
func (u *baselineUnit) renderListing() {
	e := &baselineEngine{options: u.options, m: u.m, symbols: u.symbols, arch: u.arch}
	e.emit.poolBase = u.poolBase
	enc := u.arch.encoder(e, true)
	pc, err := u.arch.encode(e, enc, u.phys, 0, u.labels)
	if err != nil {
		u.listingErr = err
		return
	}
	if len(u.pool) != 0 {
		if err := u.arch.(baselineLiteralArch).encodePool(e, enc, pc, u.poolBase, u.pool); err != nil {
			u.listingErr = err
			return
		}
	}
	if err := enc.Err(); err != nil {
		u.listingErr = err
		return
	}
	u.listing = enc.(interface{ Text() string }).Text()
}

func (u *baselineUnit) templateFor(view abi.Report) (*execution.Template, error) {
	u.templateOnce.Do(func() {
		if err := validateEmissionTarget(u.options, emitNative); err != nil {
			u.templateErr = err
			return
		}
		emission := emissionTemplate{Bytes: u.code, Report: u.frameReport(), Relocations: u.relocs}
		u.template = newExecutableTemplate(emission, view, u.arity, u.paramBits, u.paramTypes, u.options.ABI,
			u.boundary == boundaryForeign, false, u.boundary == boundaryExternal, nil, false)
	})
	return u.template, u.templateErr
}

// lowerBaseline lowers a program in the baseline tier by feeding its
// segments to a Stream.
func lowerBaseline(t Target, program ir.Input, f Func) (*Unit, error) {
	if len(f.Constants) != 0 {
		bound, err := bindSymbols(program, f.Constants)
		if err != nil {
			return nil, err
		}
		program = bound
		f.Constants = nil
	}
	var view ir.Program
	if _, err := validatedSourceInto(program, &view); err != nil {
		return nil, err
	}
	source := &view
	var vectorOperations []abi.VectorOperation
	s, err := NewStream(t, f)
	if err != nil {
		return nil, err
	}
	err = programSegments(source, s.Segment)
	if err != nil && errors.Is(err, ErrUnsupported) && programHasVectors(source) {
		// Vectors are scalarized lane by lane before the baseline sees
		// them, as the optimized tier does in control programs.
		options := t.resolved().options(f)
		options.Features = []string{}
		scalar, ops, lerr := legalizeVectors(source, options)
		if lerr != nil {
			return nil, lerr
		}
		source, vectorOperations = scalar, ops
		if s, err = NewStream(t, f); err != nil {
			return nil, err
		}
		err = programSegments(source, s.Segment)
	}
	if err != nil {
		return nil, err
	}
	u, err := s.Unit()
	if err != nil {
		return nil, err
	}
	u.baseline.vectorOperations = vectorOperations
	return u, nil
}

func programHasVectors(p *ir.Program) bool {
	for i := range p.Instructions {
		ins := &p.Instructions[i]
		if ins.Type.Kind == ir.VectorKind || ins.Type.Kind == ir.MaskKind || ins.Op >= ir.OpVLoad && ins.Op <= ir.OpVInsertZero || ins.Op == ir.OpVCarrylessMul {
			return true
		}
	}
	for _, v := range p.Variables {
		if v.Type.Kind == ir.VectorKind || v.Type.Kind == ir.MaskKind {
			return true
		}
	}
	if p.Control != nil {
		for _, types := range p.Control.ParamTypes {
			for _, t := range types {
				if t.Kind == ir.VectorKind || t.Kind == ir.MaskKind {
					return true
				}
			}
		}
	}
	return false
}

// baselinePoolBytes returns the literal pool's bytes from pc: zero padding
// to base, then each entry, little-endian.
func baselinePoolBytes(e *baselineEngine, pc, base int, pool []uint64) []byte {
	n := base - pc + 8*len(pool)
	buf := e.emit.poolBytes[:0]
	if cap(buf) < n {
		buf = make([]byte, 0, n)
	}
	buf = buf[:base-pc]
	clear(buf)
	for _, v := range pool {
		buf = binary.LittleEndian.AppendUint64(buf, v)
	}
	e.emit.poolBytes = buf
	return buf
}
