package machine

import (
	"encoding/binary"
	"errors"

	"github.com/moriyoshi/nyanjit/regalloc"
)

// ErrUnboundSymbol means a symbol load has no binding.
var ErrUnboundSymbol = errors.New("nyanjit: symbolic constant is not bound")

// LinkRelocations copies machine bytes and patches every symbol load.
func LinkRelocations(code []byte, relocations []Relocation, bindings map[string]uint64) ([]byte, error) {
	linked := append([]byte(nil), code...)
	used := make(map[string]bool)
	for _, relocation := range relocations {
		value, ok := bindings[relocation.Symbol]
		if !ok {
			return nil, ErrUnboundSymbol
		}
		used[relocation.Symbol] = true
		switch relocation.Encoding {
		case Reloc386Immediate:
			if value > 0xffffffff || relocation.Offset < 0 || relocation.Offset > len(linked)-5 ||
				linked[relocation.Offset]&0xf8 != 0xb8 {
				return nil, regalloc.ErrInvalidFunction
			}
			binary.LittleEndian.PutUint32(linked[relocation.Offset+1:], uint32(value))
		case RelocAMD64Immediate:
			if relocation.Offset < 0 || relocation.Offset > len(linked)-10 {
				return nil, regalloc.ErrInvalidFunction
			}
			if linked[relocation.Offset]&^byte(1) != 0x48 || linked[relocation.Offset+1]&0xf8 != 0xb8 {
				return nil, regalloc.ErrInvalidFunction
			}
			binary.LittleEndian.PutUint64(linked[relocation.Offset+2:], value)
		case RelocARM64WideMoves:
			if relocation.Offset < 0 || relocation.Offset > len(linked)-16 {
				return nil, regalloc.ErrInvalidFunction
			}
			for i := 0; i < 4; i++ {
				offset := relocation.Offset + 4*i
				word := binary.LittleEndian.Uint32(linked[offset:])
				expected := uint32(0xF2800000 | uint32(i)<<21)
				if i == 0 {
					expected = 0xD2800000
				}
				if word&^uint32(0x1fffe0|0x1f) != expected {
					return nil, regalloc.ErrInvalidFunction
				}
				word = word&^0x1fffe0 | uint32(uint16(value>>(16*i)))<<5
				binary.LittleEndian.PutUint32(linked[offset:], word)
			}
		case RelocRISCV64ByteSequence:
			if relocation.Offset < 0 || relocation.Offset > len(linked)-60 {
				return nil, regalloc.ErrInvalidFunction
			}
			dst := binary.LittleEndian.Uint32(linked[relocation.Offset:]) >> 7 & 31
			for i := 0; i < 15; i++ {
				offset := relocation.Offset + i*4
				word := binary.LittleEndian.Uint32(linked[offset:])
				if i%2 == 1 {
					// SLLI rd, rd, 8.
					if word&^uint32(31<<15|31<<7) != 0x00801013 || word>>7&31 != dst || word>>15&31 != dst {
						return nil, regalloc.ErrInvalidFunction
					}
					continue
				}
				// Each ADDI/ORI immediate holds one unsigned byte. The upper
				// four immediate bits must be clear because patching only
				// replaces the low eight bits.
				if word>>28 != 0 {
					return nil, regalloc.ErrInvalidFunction
				}
				if i == 0 {
					if word&0x0000707f != 0x13 || word>>15&31 != 0 || word>>7&31 != dst {
						return nil, regalloc.ErrInvalidFunction
					}
				} else if word&0x0000707f != 0x6013 || word>>7&31 != dst || word>>15&31 != dst {
					return nil, regalloc.ErrInvalidFunction
				}
				shift := uint((14 - i) / 2 * 8)
				word = word&^uint32(0xff<<20) | uint32(byte(value>>shift))<<20
				binary.LittleEndian.PutUint32(linked[offset:], word)
			}
		case RelocPPC64BigEndian, RelocPPC64LittleEndian:
			if relocation.Offset < 0 || relocation.Offset > len(linked)-20 || relocation.Offset%4 != 0 {
				return nil, regalloc.ErrInvalidFunction
			}
			var order binary.ByteOrder = binary.BigEndian
			if relocation.Encoding == RelocPPC64LittleEndian {
				order = binary.LittleEndian
			}
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
						return nil, regalloc.ErrInvalidFunction
					}
					continue
				}
				if word&^0xffff != expected[i] {
					return nil, regalloc.ErrInvalidFunction
				}
				order.PutUint32(linked[offset:], expected[i]|uint32(uint16(value>>shifts[i])))
			}
		default:
			return nil, regalloc.ErrInvalidFunction
		}
	}
	for name := range bindings {
		if !used[name] {
			return nil, regalloc.ErrInvalidFunction
		}
	}
	return linked, nil
}
