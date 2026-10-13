package machine

import (
	"encoding/binary"
	"errors"

	"github.com/moriyoshi/nyanjit/backend"
)

// ErrUnboundSymbol means a symbol load has no binding.
var ErrUnboundSymbol = errors.New("nyanjit: symbolic constant is not bound")

// ErrCodeBaseRequired means an absolute local address needs a final code base.
var ErrCodeBaseRequired = errors.New("nyanjit: code base is required to link local addresses")

// LinkRelocations copies machine bytes and patches every symbol load.
// External symbol bindings must be nonzero; zero returns backend.ErrInvalidFunction.
// A code-base relocation returns ErrCodeBaseRequired; use LinkRelocationsAt.
func LinkRelocations(code []byte, relocations []Relocation, bindings map[string]uint64) ([]byte, error) {
	return linkRelocations(code, relocations, bindings, nil)
}

// LinkRelocationsAt also resolves local addresses relative to the final mapping.
// The returned bytes belong at base; moving them requires linking again.
func LinkRelocationsAt(code []byte, relocations []Relocation, bindings map[string]uint64, base uint64) ([]byte, error) {
	return linkRelocations(code, relocations, bindings, &base)
}

func linkRelocations(code []byte, relocations []Relocation, bindings map[string]uint64, base *uint64) ([]byte, error) {
	linked := append([]byte(nil), code...)
	used := make(map[string]bool)
	for _, relocation := range relocations {
		if relocation.NeedsCodeBase() {
			if base == nil {
				return nil, ErrCodeBaseRequired
			}
			// Only absolute MOVSS/MOVSD/MOVDQU loads are supported. Validate
			// both the patch site and the complete literal footprint.
			off := relocation.Offset
			if relocation.Symbol != "" || off < 0 || off > len(linked)-8 ||
				linked[off+1] != 0x0f || linked[off+3]&0xc7 != 0x05 {
				return nil, backend.ErrInvalidFunction
			}
			width := uint64(0)
			switch {
			case linked[off] == 0xf2 && linked[off+2] == 0x10:
				width = 8
			case linked[off] == 0xf3 && linked[off+2] == 0x10:
				width = 4
			case linked[off] == 0xf3 && linked[off+2] == 0x6f:
				width = 16
			}
			const max = uint64(0xffffffff)
			if width == 0 || width > uint64(len(linked)) || relocation.TargetOffset > uint64(len(linked))-width ||
				*base > max || uint64(len(linked))-1 > max-*base {
				return nil, backend.ErrInvalidFunction
			}
			binary.LittleEndian.PutUint32(linked[off+4:], uint32(*base+relocation.TargetOffset))
			continue
		}
		value, ok := bindings[relocation.Symbol]
		if !ok {
			return nil, ErrUnboundSymbol
		}
		if value == 0 {
			return nil, backend.ErrInvalidFunction
		}
		used[relocation.Symbol] = true
		switch relocation.Encoding {
		case Reloc386WideImmediate:
			if relocation.Offset < 0 || relocation.Offset > len(linked)-10 ||
				linked[relocation.Offset]&0xf8 != 0xb8 || linked[relocation.Offset+5]&0xf8 != 0xb8 {
				return nil, backend.ErrInvalidFunction
			}
			binary.LittleEndian.PutUint32(linked[relocation.Offset+1:], uint32(value))
			binary.LittleEndian.PutUint32(linked[relocation.Offset+6:], uint32(value>>32))
		case Reloc386Immediate:
			if value > 0xffffffff || relocation.Offset < 0 || relocation.Offset > len(linked)-5 ||
				linked[relocation.Offset]&0xf8 != 0xb8 {
				return nil, backend.ErrInvalidFunction
			}
			binary.LittleEndian.PutUint32(linked[relocation.Offset+1:], uint32(value))
		case RelocAMD64Immediate:
			if relocation.Offset < 0 || relocation.Offset > len(linked)-10 {
				return nil, backend.ErrInvalidFunction
			}
			if linked[relocation.Offset]&^byte(1) != 0x48 || linked[relocation.Offset+1]&0xf8 != 0xb8 {
				return nil, backend.ErrInvalidFunction
			}
			binary.LittleEndian.PutUint64(linked[relocation.Offset+2:], value)
		case RelocARM64WideMoves:
			if relocation.Offset < 0 || relocation.Offset > len(linked)-16 {
				return nil, backend.ErrInvalidFunction
			}
			for i := 0; i < 4; i++ {
				offset := relocation.Offset + 4*i
				word := binary.LittleEndian.Uint32(linked[offset:])
				expected := uint32(0xF2800000 | uint32(i)<<21)
				if i == 0 {
					expected = 0xD2800000
				}
				if word&^uint32(0x1fffe0|0x1f) != expected {
					return nil, backend.ErrInvalidFunction
				}
				word = word&^0x1fffe0 | uint32(uint16(value>>(16*i)))<<5
				binary.LittleEndian.PutUint32(linked[offset:], word)
			}
		case RelocRISCV64ByteSequence:
			if relocation.Offset < 0 || relocation.Offset > len(linked)-60 {
				return nil, backend.ErrInvalidFunction
			}
			dst := binary.LittleEndian.Uint32(linked[relocation.Offset:]) >> 7 & 31
			for i := 0; i < 15; i++ {
				offset := relocation.Offset + i*4
				word := binary.LittleEndian.Uint32(linked[offset:])
				if i%2 == 1 {
					// SLLI rd, rd, 8.
					if word&^uint32(31<<15|31<<7) != 0x00801013 || word>>7&31 != dst || word>>15&31 != dst {
						return nil, backend.ErrInvalidFunction
					}
					continue
				}
				// Each ADDI/ORI immediate holds one unsigned byte. The upper
				// four immediate bits must be clear because patching only
				// replaces the low eight bits.
				if word>>28 != 0 {
					return nil, backend.ErrInvalidFunction
				}
				if i == 0 {
					if word&0x0000707f != 0x13 || word>>15&31 != 0 || word>>7&31 != dst {
						return nil, backend.ErrInvalidFunction
					}
				} else if word&0x0000707f != 0x6013 || word>>7&31 != dst || word>>15&31 != dst {
					return nil, backend.ErrInvalidFunction
				}
				shift := uint((14 - i) / 2 * 8)
				word = word&^uint32(0xff<<20) | uint32(byte(value>>shift))<<20
				binary.LittleEndian.PutUint32(linked[offset:], word)
			}

		case RelocLoong64Constant:
			off := relocation.Offset
			if off < 0 || off%4 != 0 || off > len(linked)-16 {
				return nil, backend.ErrInvalidFunction
			}
			dst := binary.LittleEndian.Uint32(linked[off:]) & 31
			expected := [4]uint32{0x14000000 | dst, 0x03800000 | dst<<5 | dst, 0x16000000 | dst, 0x03000000 | dst<<5 | dst}
			masks := [4]uint32{0x01ffffe0, 0x003ffc00, 0x01ffffe0, 0x003ffc00}
			vals := [4]uint32{uint32(value>>12&0xfffff) << 5, uint32(value&4095) << 10, uint32(value>>32&0xfffff) << 5, uint32(value>>52) << 10}
			for n := range expected {
				w := binary.LittleEndian.Uint32(linked[off+n*4:])
				if w&^masks[n] != expected[n] {
					return nil, backend.ErrInvalidFunction
				}
				binary.LittleEndian.PutUint32(linked[off+n*4:], expected[n]|vals[n])
			}
		case RelocPPC64LittleEndian:
			if relocation.Offset < 0 || relocation.Offset > len(linked)-20 || relocation.Offset%4 != 0 {
				return nil, backend.ErrInvalidFunction
			}
			order := binary.LittleEndian
			dst := order.Uint32(linked[relocation.Offset:]) >> 21 & 31
			expected := [5]uint32{
				15<<26 | dst<<21,
				24<<26 | dst<<21 | dst<<16,
				30<<26 | dst<<21 | dst<<16 | 31<<6 | 2 | 4,
				25<<26 | dst<<21 | dst<<16,
				24<<26 | dst<<21 | dst<<16,
			}
			shifts := [5]uint{48, 32, 0, 16, 0}
			for i := range expected {
				offset := relocation.Offset + i*4
				word := order.Uint32(linked[offset:])
				if i == 2 {
					if word != expected[i] {
						return nil, backend.ErrInvalidFunction
					}
					continue
				}
				if word&^0xffff != expected[i] {
					return nil, backend.ErrInvalidFunction
				}
				order.PutUint32(linked[offset:], expected[i]|uint32(uint16(value>>shifts[i])))
			}
		default:
			return nil, backend.ErrInvalidFunction
		}
	}
	for name := range bindings {
		if !used[name] {
			return nil, backend.ErrInvalidFunction
		}
	}
	return linked, nil
}
