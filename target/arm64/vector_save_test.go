package arm64

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
)

func TestCFloatSaveUsesLowDRegisterAndGoHomeOffset(t *testing.T) {
	byteAssembler := NewByteAssembler(abi.StackPlatform, 9)
	byteAssembler.SaveCFloat(8, 0)
	byteAssembler.RestoreCFloat(8, 0)
	code := byteAssembler.Bytes()
	if len(code) != 8 || binary.LittleEndian.Uint32(code) != 0xfd002be8 || binary.LittleEndian.Uint32(code[4:]) != 0xfd402be8 {
		t.Fatalf("D8 save/restore encoding = %x", code)
	}
	textAssembler := NewTextAssembler(abi.StackPlatform, 9)
	textAssembler.SaveCFloat(8, 0)
	textAssembler.RestoreCFloat(8, 0)
	text := textAssembler.Text()
	if !strings.Contains(text, "str D8, [SP, #80]") || !strings.Contains(text, "ldr D8, [SP, #80]") {
		t.Fatalf("D8 save/restore text = %q", text)
	}
}
