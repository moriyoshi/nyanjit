//go:build linux && (arm64 || amd64 || riscv64)

package main

import (
	"encoding/binary"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/backend/goregister"
	termbox "github.com/nsf/termbox-go"
)

var (
	testEvents  []uint64
	testCells   int
	testFlushes int
)

func testNextEvent() uint64 {
	runtime.GC()
	time.Sleep(time.Millisecond)
	if len(testEvents) == 0 {
		return eventKey | uint64('q')
	}
	next := testEvents[0]
	testEvents = testEvents[1:]
	return next
}

func testCell(_, _, _, _ uint64) uint64 {
	testCells++
	if testCells%100 == 0 {
		runtime.GC()
	}
	return 0
}
func testClear() uint64 { return 0 }
func testFlush() uint64 { testFlushes++; return 0 }

func testBridge() terminalBridge {
	return terminalBridge{
		cell:  uint64(reflect.ValueOf(testCell).Pointer()),
		clear: uint64(reflect.ValueOf(testClear).Pointer()),
		flush: uint64(reflect.ValueOf(testFlush).Pointer()),
		event: uint64(reflect.ValueOf(testNextEvent).Pointer()),
	}
}

func TestDirectionBridge(t *testing.T) {
	for _, test := range []struct {
		name  string
		event uint64
		want  int64
	}{
		{"arrow up", eventKey | uint64(termbox.KeyArrowUp)<<32, -width},
		{"arrow down", eventKey | uint64(termbox.KeyArrowDown)<<32, width},
		{"arrow left", eventKey | uint64(termbox.KeyArrowLeft)<<32, -1},
		{"arrow right", eventKey | uint64(termbox.KeyArrowRight)<<32, 1},
		{"w", eventKey | 'w', -width}, {"W", eventKey | 'W', -width},
		{"s", eventKey | 's', width}, {"S", eventKey | 'S', width},
		{"a", eventKey | 'a', -1}, {"A", eventKey | 'A', -1},
		{"d", eventKey | 'd', 1}, {"D", eventKey | 'D', 1},
		{"unknown", eventKey | 'x', 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := directionForEvent(test.event)
			if got != uint64(test.want) {
				t.Fatalf("direction(%#x) = %#x; want %#x", test.event, got, uint64(test.want))
			}
		})
	}
}

func TestCompileGame(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	memory := make([]byte, memoryBytes)
	var pinner runtime.Pinner
	pinner.Pin(&memory[0])
	defer pinner.Unpin()
	var err error
	code, err := compileGame(memory)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
}

func TestGeneratedGameLoop(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	memory := make([]byte, memoryBytes)
	var pinner runtime.Pinner
	pinner.Pin(&memory[0])
	defer pinner.Unpin()
	var err error
	code, err := compileGameWithBridge(memory, testBridge())
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	testEvents = []uint64{eventTick, eventKey | uint64('q')}
	testCells, testFlushes = 0, 0
	result, err := code.Call()
	if err != nil {
		t.Fatal(err)
	}
	if result != 0 || testFlushes != 2 || testCells < boardBytes*2 {
		t.Fatalf("game result=%d, flushes=%d, cells=%d", result, testFlushes, testCells)
	}
	head := height/2*width + width/2 + 1
	if memory[head] != '@' || memory[head-1] != 'o' || memory[3*width+width/2] != '*' {
		t.Fatalf("board after one tick: head=%q previous=%q food=%q", memory[head], memory[head-1], memory[3*width+width/2])
	}
	if got := binary.LittleEndian.Uint64(memory[stateAt+headField:]); got != uint64(head) {
		t.Fatalf("head index = %d, want %d", got, head)
	}
}

func TestGeneratedInputAndRestart(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	for _, test := range []struct {
		name       string
		events     []uint64
		wantHead   int
		wantStatus uint64
	}{
		{"turn up", []uint64{eventKey | 'w', eventTick, eventKey | 'q'}, height/2*width + width/2 - width, playing},
		{"arrow up", []uint64{eventKey | uint64(termbox.KeyArrowUp)<<32, eventTick, eventKey | 'q'}, height/2*width + width/2 - width, playing},
		{"unknown keeps turn", []uint64{eventKey | 'w', eventKey | 'x', eventTick, eventKey | 'q'}, height/2*width + width/2 - width, playing},
		{"reverse ignored", []uint64{eventKey | 'a', eventTick, eventKey | 'q'}, height/2*width + width/2 + 1, playing},
		{"wall and restart", func() []uint64 {
			events := make([]uint64, 14)
			for i := range events {
				events[i] = eventTick
			}
			return append(events, eventKey|'r', eventKey|'q')
		}(), height/2*width + width/2, playing},
	} {
		t.Run(test.name, func(t *testing.T) {
			memory := make([]byte, memoryBytes)
			var pinner runtime.Pinner
			pinner.Pin(&memory[0])
			defer pinner.Unpin()
			var err error
			code, err := compileGameWithBridge(memory, testBridge())
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			testEvents = append([]uint64(nil), test.events...)
			result, err := code.Call()
			if err != nil {
				t.Fatal(err)
			}
			if result != 0 {
				t.Fatalf("game returned %d", result)
			}
			gotHead := binary.LittleEndian.Uint64(memory[stateAt+headField:])
			gotStatus := binary.LittleEndian.Uint64(memory[stateAt+statusField:])
			if gotHead != uint64(test.wantHead) || gotStatus != test.wantStatus || memory[test.wantHead] != '@' {
				t.Fatalf("head=%d status=%d cell=%q, want head=%d status=%d", gotHead, gotStatus, memory[test.wantHead], test.wantHead, test.wantStatus)
			}
		})
	}
}

func TestGeneratedLongSession(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	memory := make([]byte, memoryBytes)
	var pinner runtime.Pinner
	pinner.Pin(&memory[0])
	defer pinner.Unpin()
	code, err := compileGameWithBridge(memory, testBridge())
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	testCells, testFlushes = 0, 0
	testEvents = nil
	for cycle := 0; cycle < 5; cycle++ {
		for tick := 0; tick < 14; tick++ {
			testEvents = append(testEvents, eventTick)
		}
		testEvents = append(testEvents, eventKey|'r')
	}
	testEvents = append(testEvents, eventKey|'q')
	result, err := code.Call()
	if err != nil || result != 0 || testFlushes < 50 {
		t.Fatalf("session result=%d err=%v flushes=%d", result, err, testFlushes)
	}
}
