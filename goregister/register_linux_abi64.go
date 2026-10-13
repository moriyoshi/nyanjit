//go:build linux && (arm64 || amd64 || 386 || riscv64 || ppc64 || ppc64le)

package goregister

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// These layouts mirror runtime/symtab.go moduledata and runtime/runtime2.go
// _func for the supported Go release families.
type jitPCHeader struct {
	magic          uint32
	pad1, pad2     uint8
	minLC, ptrSize uint8
	nfunc, nfiles  uintptr
	unused         uintptr
	funcnameOffset uintptr
	cuOffset       uintptr
	filetabOffset  uintptr
	pctabOffset    uintptr
	pclnOffset     uintptr
}

type jitFuncTab struct {
	entryoff uint32
	funcoff  uint32
}

type jitBitvector struct {
	n        int32
	bytedata *byte
}

type jitModulePrefix struct {
	pcHeader     *jitPCHeader
	funcnametab  []byte
	cutab        []uint32
	filetab      []byte
	pctab        []byte
	pclntable    []byte
	ftab         []jitFuncTab
	findfunctab  uintptr
	minpc, maxpc uintptr

	text, etext           uintptr
	noptrdata, enoptrdata uintptr
	data, edata           uintptr
	bss, ebss             uintptr
	noptrbss, enoptrbss   uintptr
	covctrs, ecovctrs     uintptr
	end, gcdata, gcbss    uintptr
}

type jitModuleData126 struct {
	jitModulePrefix
	types, etypes uintptr
	rodata        uintptr
	gofunc        uintptr
	epclntab      uintptr

	textsectmap  []byte // nil; same slice layout as []runtime.textsect
	typelinks    []int32
	itablinks    []unsafe.Pointer
	ptab         []byte // nil; same slice layout as []runtime.ptabEntry
	pluginpath   string
	pkghashes    []byte // nil; same slice layout as []runtime.modulehash
	inittasks    []unsafe.Pointer
	modulename   string
	modulehashes []byte // nil; same slice layout as []runtime.modulehash
	hasmain      uint8
	bad          bool
	gcdatamask   jitBitvector
	gcbssmask    jitBitvector
	typemap      map[int32]unsafe.Pointer
	next         *jitModuleData126
}

type jitModuleData127 struct {
	jitModulePrefix
	types, typedesclen, etypes uintptr
	itaboffset, itabsize       uintptr
	rodata                     uintptr
	gofunc                     uintptr
	epclntab                   uintptr

	textsectmap  []byte // nil; same slice layout as []runtime.textsect
	ptab         []byte // nil; same slice layout as []runtime.ptabEntry
	pluginpath   string
	pkghashes    []byte // nil; same slice layout as []runtime.modulehash
	inittasks    []unsafe.Pointer
	modulename   string
	modulehashes []byte // nil; same slice layout as []runtime.modulehash
	hasmain      uint8
	bad          bool
	gcdatamask   jitBitvector
	gcbssmask    jitBitvector
	typemap      map[unsafe.Pointer]unsafe.Pointer
	next         *jitModuleData127
}

//go:linkname jitLastModule runtime.lastmoduledatap
var jitLastModule unsafe.Pointer

// Capture the executable module before any JIT/plugin modules are appended.
var jitRuntimeModule = jitLastModule

//go:linkname jitPluginLastModuleInit plugin.lastmoduleinit
func jitPluginLastModuleInit() (string, map[string]any, []unsafe.Pointer, string)

type jitRegisteredModule struct {
	module   unsafe.Pointer
	layout   runtimeLayout
	storage  []byte
	code     []byte
	metadata []byte
	lookup   []byte
	funcdata []byte
	name     string
	pinner   runtime.Pinner
}

var jitRegistry struct {
	sync.Mutex
	modules []*jitRegisteredModule
}
var jitModuleSequence atomic.Uint64
var jitEmptyGCProg = [1]byte{0}

type runtimeLayout uint8

const (
	layoutUnknown runtimeLayout = iota
	layout126
	layout127
)

func layoutForVersion(version string) runtimeLayout {
	for _, candidate := range []struct {
		prefix string
		layout runtimeLayout
	}{{"go1.26", layout126}, {"go1.27", layout127}} {
		if version == candidate.prefix {
			return candidate.layout
		}
		if patch, ok := strings.CutPrefix(version, candidate.prefix+"."); ok {
			if _, err := strconv.ParseUint(patch, 10, 32); err == nil {
				return candidate.layout
			}
		}
	}
	return layoutUnknown
}

func validRuntimeAnchor(layout runtimeLayout, module unsafe.Pointer) bool {
	if module == nil {
		return false
	}
	prefix := (*jitModulePrefix)(module)
	if prefix.pcHeader == nil || prefix.pcHeader.magic != 0xfffffff1 ||
		prefix.pcHeader.ptrSize != uint8(unsafe.Sizeof(uintptr(0))) || int(prefix.pcHeader.minLC) != pcQuantum() {
		return false
	}
	switch layout {
	case layout126:
		return (*jitModuleData126)(module).next == nil
	case layout127:
		return (*jitModuleData127)(module).next == nil
	default:
		return false
	}
}

// Supported reports whether the running Go release has a known module layout.
func Supported() bool { return layoutForVersion(runtime.Version()) != layoutUnknown }

func pcQuantum() int {
	if runtime.GOARCH == "arm64" || runtime.GOARCH == "ppc64" || runtime.GOARCH == "ppc64le" {
		return 4
	}
	if runtime.GOARCH == "riscv64" {
		return 2
	}
	return 1
}

func encodePCValues(length int, points []SPPoint) ([]byte, error) {
	quantum := pcQuantum()
	if length <= 0 || length%quantum != 0 || len(points) == 0 || points[0].PC != 0 {
		return nil, ErrInvalidFrame
	}
	var result []byte
	var tmp [binary.MaxVarintLen32]byte
	previous := int32(-1)
	for i, point := range points {
		end := length
		if i+1 < len(points) {
			end = points[i+1].PC
		}
		if point.PC < 0 || point.PC >= end || end > length || point.PC%quantum != 0 || end%quantum != 0 {
			return nil, ErrInvalidFrame
		}
		value := int32(point.Delta)
		change := value - previous
		encoded := uint32(change<<1) ^ uint32(change>>31)
		n := binary.PutUvarint(tmp[:], uint64(encoded))
		result = append(result, tmp[:n]...)
		n = binary.PutUvarint(tmp[:], uint64((end-point.PC)/quantum))
		result = append(result, tmp[:n]...)
		previous = value
	}
	return append(result, 0), nil
}

func align8(value int) int { return (value + 7) &^ 7 }

func newJITModule(layout runtimeLayout, code []byte, info Frame) (*jitRegisteredModule, error) {
	frameAlignment := int(unsafe.Sizeof(uintptr(0)))
	if runtime.GOARCH == "arm64" || runtime.GOARCH == "riscv64" {
		frameAlignment = 16
	}
	maxFrameBytes := 240
	if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
		// Leave 160 bytes of Go's 800-byte nosplit reserve for gateways.
		maxFrameBytes = 640
	}
	if runtime.GOARCH == "riscv64" {
		maxFrameBytes = 512
	}
	if runtime.GOARCH == "ppc64" || runtime.GOARCH == "ppc64le" {
		maxFrameBytes = 640
	}
	if len(code) == 0 || len(code) != info.CodeBytes || len(code)%pcQuantum() != 0 ||
		uint64(len(code)) > uint64(^uint32(0)) || info.FrameBytes < 0 ||
		info.FrameBytes > maxFrameBytes || info.FrameBytes%frameAlignment != 0 ||
		info.ArgBytes < 0 || info.ArgBytes > 4096 || info.ArgBytes%frameAlignment != 0 ||
		strings.IndexByte(info.Name, 0) >= 0 {
		return nil, ErrInvalidFrame
	}
	for _, point := range info.SP {
		if point.Delta < 0 || point.Delta > info.FrameBytes {
			return nil, ErrInvalidFrame
		}
	}
	pcsp, err := encodePCValues(len(code), info.SP)
	if err != nil {
		return nil, err
	}
	unsafePC, err := encodePCValues(len(code), []SPPoint{{PC: 0, Delta: -2}})
	if err != nil {
		return nil, err
	}
	stackPC, err := encodePCValues(len(code), []SPPoint{{PC: 0, Delta: 0}})
	if err != nil {
		return nil, err
	}
	unknownPC, err := encodePCValues(len(code), []SPPoint{{PC: 0, Delta: -1}})
	if err != nil {
		return nil, err
	}
	pctab := []byte{0} // A zero offset means an absent PC table.
	pcspOff := len(pctab)
	pctab = append(pctab, pcsp...)
	unsafeOff := len(pctab)
	pctab = append(pctab, unsafePC...)
	stackOff := len(pctab)
	pctab = append(pctab, stackPC...)
	unknownOff := len(pctab)
	pctab = append(pctab, unknownPC...)

	const funcSize = 44
	const funcOff = 16 // two functab entries, aligned to 8 bytes
	functab := make([]byte, funcOff+funcSize+2*4+2*4)
	binary.NativeEndian.PutUint32(functab[4:], funcOff)
	binary.NativeEndian.PutUint32(functab[8:], uint32(len(code)))
	fn := functab[funcOff:]
	binary.NativeEndian.PutUint32(fn[4:], 1) // name offset zero means unnamed
	binary.NativeEndian.PutUint32(fn[8:], uint32(info.ArgBytes))
	binary.NativeEndian.PutUint32(fn[16:], uint32(pcspOff))
	binary.NativeEndian.PutUint32(fn[20:], uint32(unknownOff))
	binary.NativeEndian.PutUint32(fn[24:], uint32(unknownOff))
	binary.NativeEndian.PutUint32(fn[28:], 2)          // unsafe point and stack-map index
	binary.NativeEndian.PutUint32(fn[32:], ^uint32(0)) // no compilation unit
	fn[41] = 4                                         // abi.FuncFlagAsm: no asynchronous preemption
	fn[43] = 2                                         // argument and local pointer maps
	binary.NativeEndian.PutUint32(fn[44:], uint32(unsafeOff))
	binary.NativeEndian.PutUint32(fn[48:], uint32(stackOff))
	// Both maps have one entry and zero pointer bits. Managed pointers are
	// excluded until value types and liveness are available.
	funcdata := make([]byte, 8)
	binary.NativeEndian.PutUint32(funcdata, 1)

	functionName := info.Name
	if functionName == "" {
		functionName = "goregister.generated"
	}
	names := append(append([]byte{0}, functionName...), 0)
	files := []byte("?\x00")
	headerSize := int(unsafe.Sizeof(jitPCHeader{}))
	nameOff := headerSize
	cuOff := nameOff + len(names)
	fileOff := cuOff
	pctabOff := fileOff + len(files)
	pclnOff := align8(pctabOff + len(pctab))
	metadata := make([]byte, pclnOff+len(functab))
	copy(metadata[nameOff:], names)
	copy(metadata[fileOff:], files)
	copy(metadata[pctabOff:], pctab)
	copy(metadata[pclnOff:], functab)
	hdr := (*jitPCHeader)(unsafe.Pointer(&metadata[0]))
	hdr.magic, hdr.minLC, hdr.ptrSize = 0xfffffff1, uint8(pcQuantum()), uint8(unsafe.Sizeof(uintptr(0)))
	hdr.nfunc = 1
	hdr.funcnameOffset = uintptr(nameOff)
	hdr.cuOffset = uintptr(cuOff)
	hdr.filetabOffset = uintptr(fileOff)
	hdr.pctabOffset = uintptr(pctabOff)
	hdr.pclnOffset = uintptr(pclnOff)
	// One function covers the whole code mapping. Every lookup bucket thus
	// starts at function index zero, including its sixteen subbuckets.
	buckets := make([]byte, ((len(code)+4095)/4096)*20)
	name := fmt.Sprintf("goregister.module.%d", jitModuleSequence.Add(1))
	start := uintptr(unsafe.Pointer(&code[0]))
	// plugin.lastmoduleinit compares even empty data, BSS, and type ranges
	// inclusively. Distinct pinned metadata gives each code-only module a
	// distinct zero-length range.
	anchor := uintptr(unsafe.Pointer(&metadata[0]))
	// runtime.moduledata embeds NotInHeap. Keep the mirror outside the Go
	// heap as the normal linker and dynamic loader do.
	moduleBytes := int(unsafe.Sizeof(jitModuleData126{}))
	if layout == layout127 {
		moduleBytes = int(unsafe.Sizeof(jitModuleData127{}))
	}
	storage, err := syscall.Mmap(-1, 0, moduleBytes,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, err
	}
	module := unsafe.Pointer(&storage[0])
	prefix := jitModulePrefix{
		pcHeader: hdr, funcnametab: metadata[nameOff:cuOff],
		filetab: metadata[fileOff:pctabOff], pctab: metadata[pctabOff:pclnOff],
		pclntable:   metadata[pclnOff:],
		ftab:        unsafe.Slice((*jitFuncTab)(unsafe.Pointer(&metadata[pclnOff])), 2),
		findfunctab: uintptr(unsafe.Pointer(&buckets[0])),
		minpc:       start, maxpc: start + uintptr(len(code)),
		text: start, etext: start + uintptr(len(code)),
		data: anchor, edata: anchor,
		bss: anchor, ebss: anchor,
		gcdata: uintptr(unsafe.Pointer(&jitEmptyGCProg[0])),
		gcbss:  uintptr(unsafe.Pointer(&jitEmptyGCProg[0])),
	}
	funcDataAddress := uintptr(unsafe.Pointer(&funcdata[0]))
	endPCLN := uintptr(unsafe.Pointer(&metadata[len(metadata)-1])) + 1
	switch layout {
	case layout126:
		*(*jitModuleData126)(module) = jitModuleData126{
			jitModulePrefix: prefix,
			types:           anchor, etypes: anchor,
			gofunc: funcDataAddress, epclntab: endPCLN,
			pluginpath: name, modulename: name,
		}
	case layout127:
		*(*jitModuleData127)(module) = jitModuleData127{
			jitModulePrefix: prefix,
			types:           anchor, etypes: anchor,
			gofunc: funcDataAddress, epclntab: endPCLN,
			pluginpath: name, modulename: name,
		}
	default:
		_ = syscall.Munmap(storage)
		return nil, ErrUnsupported
	}
	retained := &jitRegisteredModule{module: module, layout: layout, storage: storage, code: code, metadata: metadata, lookup: buckets, funcdata: funcdata, name: name}
	retained.pinner.Pin(&metadata[0])
	retained.pinner.Pin(&buckets[0])
	retained.pinner.Pin(&funcdata[0])
	return retained, nil
}

// Register publishes code as a pointer-free Go function. Code must already be
// executable and remain immutable. It retains runtime metadata for the process
// lifetime. linked reports whether the runtime can see the module; once true,
// the caller must never unmap code, even if Register returns an error.
func Register(code []byte, info Frame) (linked bool, err error) {
	if !info.PointerFree {
		return false, ErrInvalidFrame
	}
	layout := layoutForVersion(runtime.Version())
	if layout == layoutUnknown {
		return false, ErrUnsupported
	}
	retained, err := newJITModule(layout, code, info)
	if err != nil {
		return false, err
	}
	jitRegistry.Lock()
	defer jitRegistry.Unlock()
	if !validRuntimeAnchor(layout, jitLastModule) {
		retained.pinner.Unpin()
		_ = syscall.Munmap(retained.storage)
		return false, ErrUnsupported
	}
	// plugin.lastmoduleinit uses inclusive text endpoints. Reject touching
	// ranges before publication, rather than letting the runtime abort.
	start := uintptr(unsafe.Pointer(&code[0]))
	end := start + uintptr(len(code))
	for _, previous := range jitRegistry.modules {
		oldStart := uintptr(unsafe.Pointer(&previous.code[0]))
		oldEnd := oldStart + uintptr(len(previous.code))
		if start <= oldEnd && oldStart <= end {
			retained.pinner.Unpin()
			_ = syscall.Munmap(retained.storage)
			return false, fmt.Errorf("registered text [%#x,%#x] touches [%#x,%#x]: %w", start, end, oldStart, oldEnd, ErrInvalidFrame)
		}
	}
	// Retain every backing object before the runtime can see its moduledata.
	jitRegistry.modules = append(jitRegistry.modules, retained)
	switch layout {
	case layout126:
		(*jitModuleData126)(jitLastModule).next = (*jitModuleData126)(retained.module)
	case layout127:
		(*jitModuleData127)(jitLastModule).next = (*jitModuleData127)(retained.module)
	}
	jitLastModule = retained.module
	_, _, _, errstr := jitPluginLastModuleInit()
	if errstr != "" {
		switch layout {
		case layout126:
			(*jitModuleData126)(retained.module).bad = true
		case layout127:
			(*jitModuleData127)(retained.module).bad = true
		}
		return true, fmt.Errorf("nyanjit: Go module registration: %s", errstr)
	}
	return true, nil
}
