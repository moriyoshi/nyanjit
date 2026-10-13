//go:build linux && ppc64

package execution

import (
	"encoding/binary"
	"reflect"
	"sync"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/ppc64"
)

type ppc64Callback struct {
	fn               reflect.Value
	code, descriptor []byte
}

var ppc64Callbacks struct {
	sync.RWMutex
	entries []*ppc64Callback
}

var ppc64CallbackDispatcher = dispatchPPC64Callback

func callbackNativeAddress() uintptr

type ppc64CallbackFrame struct {
	Index  uint64
	Args   [8]uint64
	Result uint64
}

func callbackWordType(kind reflect.Kind) bool {
	switch kind {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return false
}

func registerForeignCallback(fn any) (uintptr, error) {
	return registerPPC64ForeignCallback(fn, abi.PPC64CABIUnspecified)
}

func registerPPC64ForeignCallback(fn any, convention abi.PPC64CABI) (uintptr, error) {
	if !foreignSupported() {
		return 0, ErrUnsupported
	}
	if convention == abi.PPC64CABIUnspecified {
		convention, _ = abi.DefaultPPC64CABI("ppc64")
	}
	if _, err := abi.NewPPC64Profile("ppc64", abi.C, convention); err != nil {
		return 0, ErrInvalidCodeOptions
	}
	value := reflect.ValueOf(fn)
	if !value.IsValid() || value.Kind() != reflect.Func || value.IsNil() {
		return 0, ErrInvalidCallback
	}
	typ := value.Type()
	if typ.IsVariadic() || typ.NumIn() > 8 || typ.NumOut() > 1 {
		return 0, ErrInvalidCallback
	}
	for i := 0; i < typ.NumIn(); i++ {
		if !callbackWordType(typ.In(i).Kind()) {
			return 0, ErrInvalidCallback
		}
	}
	if typ.NumOut() == 1 && !callbackWordType(typ.Out(0).Kind()) {
		return 0, ErrInvalidCallback
	}

	ppc64Callbacks.Lock()
	defer ppc64Callbacks.Unlock()
	index := uint64(len(ppc64Callbacks.entries))
	profile, _ := abi.NewPPC64Profile("ppc64", abi.C, convention)
	emitter, _ := ppc64.NewEmitter(profile, abi.StackNoUse)
	assembler, _ := ppc64.NewByteAssembler(ir.ByteOrderBigEndian)
	for _, op := range []machine.Instruction{
		{Op: machine.OpConst, A: 11, Imm: index},
		{Op: machine.OpConst, A: 12, Imm: uint64(callbackNativeAddress())},
	} {
		sequence, err := emitter.Expand(op, assembler.AssembledSize())
		if err != nil {
			return 0, err
		}
		for _, ins := range sequence {
			if err := assembler.Visit(ins); err != nil {
				return 0, err
			}
		}
	}
	for _, op := range []ppc64.Opcode{ppc64.Mtctr, ppc64.Bctr} {
		if err := assembler.Visit(ppc64.Instruction{Op: op, A: 12, PC: assembler.AssembledSize()}); err != nil {
			return 0, err
		}
	}
	code, err := Map(assembler.Bytes())
	if err != nil {
		return 0, err
	}
	callback := &ppc64Callback{fn: value, code: code}
	address := Entry(code)
	if convention == abi.PPC64ELFv1 {
		callback.descriptor, err = Scratch(24)
		if err != nil {
			_ = Unmap(code)
			return 0, err
		}
		binary.BigEndian.PutUint64(callback.descriptor, uint64(address))
		address = Entry(callback.descriptor)
	}
	// Callbacks have process lifetime; retain the mappings and Go closure.
	ppc64Callbacks.entries = append(ppc64Callbacks.entries, callback)
	return address, nil
}

func dispatchPPC64Callback(frame *ppc64CallbackFrame) {
	ppc64Callbacks.RLock()
	fn := ppc64Callbacks.entries[frame.Index].fn
	ppc64Callbacks.RUnlock()
	typ := fn.Type()
	arguments := make([]reflect.Value, typ.NumIn())
	for i := range arguments {
		value := reflect.New(typ.In(i)).Elem()
		word := frame.Args[i]
		switch value.Kind() {
		case reflect.Bool:
			value.SetBool(uint8(word) != 0)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			value.SetInt(int64(word))
		default:
			value.SetUint(word)
		}
		arguments[i] = value
	}
	result := fn.Call(arguments)
	frame.Result = 0
	if len(result) != 0 {
		switch result[0].Kind() {
		case reflect.Bool:
			if result[0].Bool() {
				frame.Result = 1
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			frame.Result = uint64(result[0].Int())
		default:
			frame.Result = result[0].Uint()
		}
	}
}
