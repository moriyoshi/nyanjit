//go:build linux && (arm64 || amd64 || riscv64)

// snake_termbox emits the game loop, input handling, rules, and rendering with
// Builder. Small scalar Go functions connect generated code to termbox-go.
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"time"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	termbox "github.com/nsf/termbox-go"
)

const (
	width       = 30
	height      = 18
	boardBytes  = width * height
	maxSegments = (width - 2) * (height - 2)
	segmentsAt  = (boardBytes + 7) &^ 7
	stateAt     = segmentsAt + maxSegments*8
	memoryBytes = stateAt + 17*8
)

const (
	lengthField = iota * 8
	directionField
	headField
	nextField
	foodField
	ateField
	shiftField
	scanField
	requestField
	statusField
	scoreField
	xField
	yField
	colorField
	eventField
	remainderField
	digitField
)

const (
	playing = iota
	lost
	won
)

const (
	eventTick  uint64 = 1 << 63
	eventError uint64 = 1 << 62
	eventKey   uint64 = 1 << 61
)

var (
	inputEvents <-chan termbox.Event
	tickEvents  <-chan time.Time
	terminalErr error
)

// The bridge only converts termbox's pointer-bearing results to scalar values.
// All game decisions and cell iteration are emitted by Builder.
func nextTerminalEvent() uint64 {
	select {
	case event := <-inputEvents:
		if event.Type == termbox.EventError {
			terminalErr = event.Err
			return eventError
		}
		if event.Type == termbox.EventKey {
			return eventKey | uint64(event.Key)<<32 | uint64(uint32(event.Ch))
		}
		return 0
	case <-tickEvents:
		return eventTick
	}
}

func clearTerminal() uint64 {
	if err := termbox.Clear(termbox.ColorDefault, termbox.ColorDefault); err != nil {
		terminalErr = err
		return 1
	}
	return 0
}

func putTerminalCell(x, y, ch, color uint64) uint64 {
	termbox.SetCell(int(x), int(y), rune(ch), termbox.Attribute(color), termbox.ColorDefault)
	return 0
}

func flushTerminal() uint64 {
	if err := termbox.Flush(); err != nil {
		terminalErr = err
		return 1
	}
	return 0
}

type terminalBridge struct {
	cell, clear, flush, event uint64
}

// buildGame describes the game with the public Builder API.
func buildGame(b *nyanjit.Builder, bridge terminalBridge) {
	labels := make(map[string]nyanjit.Label)
	label := func(name string) nyanjit.Label {
		if existing, ok := labels[name]; ok {
			return existing
		}
		next := b.NewLabel()
		labels[name] = next
		return next
	}
	address := func(field int) nyanjit.Value {
		return b.Add(b.Symbol("state"), b.Const(uint64(field)))
	}
	load := func(field int) nyanjit.Value { return b.Load64(address(field)) }
	store := func(field int, value nyanjit.Value) { b.Store64(address(field), value) }
	board := func(index nyanjit.Value) nyanjit.Value {
		return b.Symbol("board").Add(index)
	}
	segment := func(index nyanjit.Value) nyanjit.Value {
		return b.Symbol("segments").Add(index.MulConst(8))
	}
	cellAt := func(index nyanjit.Value) nyanjit.Value { return b.Load8(board(index)) }
	putAt := func(index, value nyanjit.Value) { b.Store8(board(index), value) }
	drawCell := func(x, y, char, color nyanjit.Value) {
		b.CallGo(b.Const(bridge.cell), x, y, char, color)
	}
	drawText := func(text string, start int) {
		for offset, char := range text {
			drawCell(b.Const(uint64(start+offset)), b.Const(height), b.Const(uint64(char)), b.Const(uint64(termbox.ColorWhite)))
		}
	}
	eventChar := func() nyanjit.Value { return b.And(load(eventField), b.Const(0xffffffff)) }
	decodedKey := func() nyanjit.Value { return b.And(b.ShiftRight(load(eventField), 32), b.Const(0xffff)) }

	// reset
	b.Bind(label("reset"))
	store(lengthField, b.Const(3))
	store(directionField, b.Const(1))
	store(headField, b.Const(height/2*width+width/2))
	store(foodField, b.Const(3*width+width/2))
	store(statusField, b.Const(playing))
	store(scoreField, b.Const(0))
	store(requestField, b.Const(0))
	store(scanField, b.Const(0))
	store(xField, b.Const(0))
	store(yField, b.Const(0))
	b.Jump(label("resetLoop"))

	b.Bind(label("resetLoop"))
	b.Branch(b.CompareImmediate(load(scanField), nyanjit.BranchLess, boardBytes), label("resetCheckX0"), label("resetSnake"))
	b.Bind(label("resetCheckX0"))
	b.Branch(b.CompareImmediate(load(xField), nyanjit.BranchEqual, 0), label("resetWall"), label("resetCheckXEnd"))
	b.Bind(label("resetCheckXEnd"))
	b.Branch(b.CompareImmediate(load(xField), nyanjit.BranchEqual, width-1), label("resetWall"), label("resetCheckY0"))
	b.Bind(label("resetCheckY0"))
	b.Branch(b.CompareImmediate(load(yField), nyanjit.BranchEqual, 0), label("resetWall"), label("resetCheckYEnd"))
	b.Bind(label("resetCheckYEnd"))
	b.Branch(b.CompareImmediate(load(yField), nyanjit.BranchEqual, height-1), label("resetWall"), label("resetSpace"))
	b.Bind(label("resetWall"))
	putAt(load(scanField), b.Const('#'))
	b.Jump(label("resetAdvance"))
	b.Bind(label("resetSpace"))
	putAt(load(scanField), b.Const(' '))
	b.Jump(label("resetAdvance"))
	b.Bind(label("resetAdvance"))
	store(scanField, b.Add(load(scanField), b.Const(1)))
	store(xField, b.Add(load(xField), b.Const(1)))
	b.Branch(b.CompareImmediate(load(xField), nyanjit.BranchEqual, width), label("resetNextRow"), label("resetLoop"))
	b.Bind(label("resetNextRow"))
	store(xField, b.Const(0))
	store(yField, b.Add(load(yField), b.Const(1)))
	b.Jump(label("resetLoop"))

	b.Bind(label("resetSnake"))
	for i := uint64(0); i < 3; i++ {
		at := height/2*width + width/2 - int(i)
		b.Store64(segment(b.Const(i)), b.Const(uint64(at)))
		putAt(b.Const(uint64(at)), b.Const('o'))
	}
	putAt(load(headField), b.Const('@'))
	putAt(load(foodField), b.Const('*'))
	b.Jump(label("render"))

	// render
	b.Bind(label("render"))
	store(requestField, b.Const(0))
	b.Branch(b.CompareImmediate(b.CallGo(b.Const(bridge.clear)), nyanjit.BranchEqual, 0), label("renderInit"), label("error"))
	b.Bind(label("renderInit"))
	store(scanField, b.Const(0))
	store(xField, b.Const(0))
	store(yField, b.Const(0))
	b.Jump(label("renderLoop"))
	b.Bind(label("renderLoop"))
	b.Branch(b.CompareImmediate(load(yField), nyanjit.BranchLess, height), label("renderColor"), label("renderMessage"))
	b.Bind(label("renderColor"))
	store(colorField, b.Const(uint64(termbox.ColorDefault)))
	b.Branch(b.CompareImmediate(cellAt(load(scanField)), nyanjit.BranchEqual, '#'), label("renderBlue"), label("renderCheckHead"))
	b.Bind(label("renderBlue"))
	store(colorField, b.Const(uint64(termbox.ColorBlue)))
	b.Jump(label("renderPut"))
	b.Bind(label("renderCheckHead"))
	b.Branch(b.CompareImmediate(cellAt(load(scanField)), nyanjit.BranchEqual, '@'), label("renderHead"), label("renderCheckBody"))
	b.Bind(label("renderHead"))
	store(colorField, b.Const(uint64(termbox.ColorGreen|termbox.AttrBold)))
	b.Jump(label("renderPut"))
	b.Bind(label("renderCheckBody"))
	b.Branch(b.CompareImmediate(cellAt(load(scanField)), nyanjit.BranchEqual, 'o'), label("renderBody"), label("renderCheckFood"))
	b.Bind(label("renderBody"))
	store(colorField, b.Const(uint64(termbox.ColorGreen)))
	b.Jump(label("renderPut"))
	b.Bind(label("renderCheckFood"))
	b.Branch(b.CompareImmediate(cellAt(load(scanField)), nyanjit.BranchEqual, '*'), label("renderFood"), label("renderPut"))
	b.Bind(label("renderFood"))
	store(colorField, b.Const(uint64(termbox.ColorRed|termbox.AttrBold)))
	b.Jump(label("renderPut"))
	b.Bind(label("renderPut"))
	drawCell(load(xField), load(yField), cellAt(load(scanField)), load(colorField))
	store(scanField, b.Add(load(scanField), b.Const(1)))
	store(xField, b.Add(load(xField), b.Const(1)))
	b.Branch(b.CompareImmediate(load(xField), nyanjit.BranchEqual, width), label("renderNextRow"), label("renderLoop"))
	b.Bind(label("renderNextRow"))
	store(xField, b.Const(0))
	store(yField, b.Add(load(yField), b.Const(1)))
	b.Jump(label("renderLoop"))

	b.Bind(label("renderMessage"))
	b.Branch(b.CompareImmediate(load(statusField), nyanjit.BranchEqual, lost), label("renderLost"), label("renderCheckWon"))
	b.Bind(label("renderCheckWon"))
	b.Branch(b.CompareImmediate(load(statusField), nyanjit.BranchEqual, won), label("renderWon"), label("renderPlaying"))
	b.Bind(label("renderLost"))
	drawText("Game over! R: restart Q: quit", 0)
	b.Jump(label("renderFlush"))
	b.Bind(label("renderWon"))
	drawText("You win! R: restart Q: quit", 0)
	b.Jump(label("renderFlush"))
	b.Bind(label("renderPlaying"))
	drawText("Score: ", 0)
	store(remainderField, load(scoreField))
	store(xField, b.Const(7))
	for _, item := range []struct {
		name  string
		place int64
	}{{"hundreds", 100}, {"tens", 10}, {"ones", 1}} {
		store(digitField, b.Const(0))
		b.Jump(label(item.name + "Loop"))
		b.Bind(label(item.name + "Loop"))
		b.Branch(b.CompareImmediate(load(remainderField), nyanjit.BranchLess, item.place), label(item.name+"Done"), label(item.name+"Sub"))
		b.Bind(label(item.name + "Sub"))
		store(remainderField, b.Sub(load(remainderField), b.Const(uint64(item.place))))
		store(digitField, b.Add(load(digitField), b.Const(1)))
		b.Jump(label(item.name + "Loop"))
		b.Bind(label(item.name + "Done"))
		drawCell(load(xField), b.Const(height), b.Add(b.Const('0'), load(digitField)), b.Const(uint64(termbox.ColorWhite)))
		store(xField, b.Add(load(xField), b.Const(1)))
	}
	drawText("  WASD/Arrows Q: quit", 10)
	b.Jump(label("renderFlush"))
	b.Bind(label("renderFlush"))
	b.Branch(b.CompareImmediate(b.CallGo(b.Const(bridge.flush)), nyanjit.BranchEqual, 0), label("event"), label("error"))

	// events
	b.Bind(label("event"))
	store(eventField, b.CallGo(b.Const(bridge.event)))
	b.Branch(b.CompareImmediate(b.And(load(eventField), b.Const(eventError)), nyanjit.BranchNotEqual, 0), label("error"), label("eventTickCheck"))
	b.Bind(label("eventTickCheck"))
	b.Branch(b.CompareImmediate(b.And(load(eventField), b.Const(eventTick)), nyanjit.BranchNotEqual, 0), label("eventTick"), label("eventKeyCheck"))
	b.Bind(label("eventTick"))
	b.Branch(b.CompareImmediate(load(statusField), nyanjit.BranchEqual, playing), label("tick"), label("event"))
	b.Bind(label("eventKeyCheck"))
	b.Branch(b.CompareImmediate(b.And(load(eventField), b.Const(eventKey)), nyanjit.BranchEqual, 0), label("event"), label("eventQuitKey"))
	b.Bind(label("eventQuitKey"))
	b.Branch(b.CompareImmediate(decodedKey(), nyanjit.BranchEqual, int64(termbox.KeyEsc)), label("done"), label("eventQuitQ"))
	b.Bind(label("eventQuitQ"))
	b.Branch(b.CompareImmediate(eventChar(), nyanjit.BranchEqual, 'q'), label("done"), label("eventQuitUpperQ"))
	b.Bind(label("eventQuitUpperQ"))
	b.Branch(b.CompareImmediate(eventChar(), nyanjit.BranchEqual, 'Q'), label("done"), label("eventRestartCheck"))
	b.Bind(label("eventRestartCheck"))
	b.Branch(b.CompareImmediate(load(statusField), nyanjit.BranchEqual, playing), label("eventDirection"), label("eventRestartLower"))
	b.Bind(label("eventRestartLower"))
	b.Branch(b.CompareImmediate(eventChar(), nyanjit.BranchEqual, 'r'), label("reset"), label("eventRestartUpper"))
	b.Bind(label("eventRestartUpper"))
	b.Branch(b.CompareImmediate(eventChar(), nyanjit.BranchEqual, 'R'), label("reset"), label("event"))
	b.Bind(label("eventDirection"))
	direction := b.CallGo(b.Symbol("direction"), load(eventField))
	predicate := b.CompareImmediate(direction, nyanjit.BranchNotEqual, 0)
	b.BranchArgs(predicate, label("eventDirectionStore"), []nyanjit.Value{direction}, label("event"), nil)
	params := b.BindParams(label("eventDirectionStore"), 1)
	store(requestField, params[0])
	b.Jump(label("event"))

	// tick
	b.Bind(label("tick"))
	b.Branch(b.CompareImmediate(load(requestField), nyanjit.BranchEqual, 0), label("move"), label("checkReverse"))
	b.Bind(label("checkReverse"))
	b.Branch(b.CompareImmediate(b.Add(load(requestField), load(directionField)), nyanjit.BranchEqual, 0), label("move"), label("applyDirection"))
	b.Bind(label("applyDirection"))
	store(directionField, load(requestField))
	b.Jump(label("move"))
	b.Bind(label("move"))
	store(nextField, b.Add(load(headField), load(directionField)))
	b.Branch(b.CompareImmediate(cellAt(load(nextField)), nyanjit.BranchEqual, '*'), label("eat"), label("checkCell"))
	b.Bind(label("checkCell"))
	b.Branch(b.CompareImmediate(cellAt(load(nextField)), nyanjit.BranchEqual, ' '), label("ordinary"), label("checkTail"))
	b.Bind(label("checkTail"))
	tail := b.Load64(segment(b.Sub(load(lengthField), b.Const(1))))
	b.Branch(b.Compare(load(nextField), nyanjit.BranchEqual, tail), label("ordinary"), label("gameOver"))
	b.Bind(label("ordinary"))
	store(ateField, b.Const(0))
	tail = b.Load64(segment(b.Sub(load(lengthField), b.Const(1))))
	putAt(tail, b.Const(' '))
	b.Jump(label("afterTail"))
	b.Bind(label("eat"))
	b.Branch(b.CompareImmediate(load(lengthField), nyanjit.BranchGreaterEqual, maxSegments-1), label("eatWin"), label("eatDo"))
	b.Bind(label("eatWin"))
	store(statusField, b.Const(won))
	b.Jump(label("eatDo"))
	b.Bind(label("eatDo"))
	store(lengthField, b.Add(load(lengthField), b.Const(1)))
	store(scoreField, b.Add(load(scoreField), b.Const(1)))
	store(ateField, b.Const(1))
	b.Jump(label("afterTail"))
	b.Bind(label("afterTail"))
	store(shiftField, b.Sub(load(lengthField), b.Const(1)))
	b.Jump(label("shift"))
	b.Bind(label("shift"))
	b.Branch(b.CompareImmediate(load(shiftField), nyanjit.BranchEqual, 0), label("shiftDone"), label("shiftBody"))
	b.Bind(label("shiftBody"))
	index := load(shiftField)
	previous := b.Sub(index, b.Const(1))
	b.Store64(segment(index), b.Load64(segment(previous)))
	store(shiftField, previous)
	b.Jump(label("shift"))
	b.Bind(label("shiftDone"))
	putAt(load(headField), b.Const('o'))
	b.Store64(b.Symbol("segments"), load(nextField))
	store(headField, load(nextField))
	putAt(load(nextField), b.Const('@'))
	b.Branch(b.CompareImmediate(load(ateField), nyanjit.BranchEqual, 0), label("render"), label("maybeFood"))
	b.Bind(label("maybeFood"))
	b.Branch(b.CompareImmediate(load(statusField), nyanjit.BranchEqual, won), label("render"), label("foodInit"))
	b.Bind(label("foodInit"))
	store(scanField, load(foodField))
	b.Jump(label("foodScan"))
	b.Bind(label("foodScan"))
	store(scanField, b.Add(load(scanField), b.Const(1)))
	b.Branch(b.CompareImmediate(load(scanField), nyanjit.BranchLess, boardBytes), label("foodCandidate"), label("foodWrap"))
	b.Bind(label("foodWrap"))
	store(scanField, b.Const(width+1))
	b.Jump(label("foodCandidate"))
	b.Bind(label("foodCandidate"))
	b.Branch(b.CompareImmediate(cellAt(load(scanField)), nyanjit.BranchEqual, ' '), label("foodPlace"), label("foodScan"))
	b.Bind(label("foodPlace"))
	putAt(load(scanField), b.Const('*'))
	store(foodField, load(scanField))
	b.Jump(label("render"))
	b.Bind(label("gameOver"))
	store(statusField, b.Const(lost))
	b.Jump(label("render"))

	b.Bind(label("done"))
	b.Return(b.Const(0))
	b.Bind(label("error"))
	b.Return(b.Const(1))
}

// compileDirection emits a separate Go ABI function. Its sole argument is
// the packed key event returned by nextTerminalEvent; zero means no direction.
func compileDirection() (*execution.Code, error) {
	b := nyanjit.NewBuilder()
	checks := []struct {
		key   bool
		value int64
		delta int64
	}{
		{true, int64(termbox.KeyArrowUp), -width},
		{true, int64(termbox.KeyArrowDown), width},
		{true, int64(termbox.KeyArrowLeft), -1},
		{true, int64(termbox.KeyArrowRight), 1},
		{false, 'w', -width}, {false, 'W', -width},
		{false, 's', width}, {false, 'S', width},
		{false, 'a', -1}, {false, 'A', -1},
		{false, 'd', 1}, {false, 'D', 1},
	}
	entry := b.NewLabel()
	next := make([]nyanjit.Label, len(checks)+1)
	matches := make([]nyanjit.Label, len(checks))
	for i := range checks {
		next[i] = b.NewLabel()
		matches[i] = b.NewLabel()
	}
	next[len(checks)] = b.NewLabel()
	b.Bind(entry)
	event := b.Param()
	b.JumpArgs(next[0], event)
	for i, check := range checks {
		params := b.BindParams(next[i], 1)
		event = params[0]
		var value nyanjit.Value
		if check.key {
			shifted := b.ShiftRight(event, 32)
			mask := b.Const(0xffff)
			value = b.And(shifted, mask)
		} else {
			mask := b.Const(0xffffffff)
			value = b.And(event, mask)
		}
		predicate := b.CompareImmediate(value, nyanjit.BranchEqual, check.value)
		b.BranchArgs(predicate, matches[i], nil, next[i+1], []nyanjit.Value{event})
	}
	b.BindParams(next[len(checks)], 1)
	zero := b.Const(0)
	b.Return(zero)
	for i, check := range checks {
		b.Bind(matches[i])
		value := b.Const(uint64(check.delta))
		b.Return(value)
	}
	program, err := b.Finish()
	if err != nil {
		return nil, err
	}
	return compile.CompileGo(program, nyanjit.CompileOptions{
		ABI: nyanjit.ABIGo, Stack: nyanjit.StackPlatform, StackBytesAvailable: 240, AssumePointerFree: true,
	})
}

// gameCode owns both generated functions. The game loop contains a direct
// call to direction's entry, so direction must outlive the loop.
type gameCode struct {
	loop, direction *execution.Code
}

// Call runs the game loop. The example only builds on 64-bit hosts, where its
// one-word result holds the whole status.
func (c *gameCode) Call() (uint64, error) {
	result, err := c.loop.Call()
	return uint64(result), err
}

func (c *gameCode) Close() error {
	loopErr := c.loop.Close()
	directionErr := c.direction.Close()
	if loopErr != nil {
		return loopErr
	}
	return directionErr
}

func compileGame(memory []byte) (*gameCode, error) {
	return compileGameWithBridge(memory, terminalBridge{
		cell:  uint64(reflect.ValueOf(putTerminalCell).Pointer()),
		clear: uint64(reflect.ValueOf(clearTerminal).Pointer()),
		flush: uint64(reflect.ValueOf(flushTerminal).Pointer()),
		event: uint64(reflect.ValueOf(nextTerminalEvent).Pointer()),
	})
}

func compileGameWithBridge(memory []byte, bridge terminalBridge) (*gameCode, error) {
	direction, err := compileDirection()
	if err != nil {
		return nil, err
	}
	directionEntry, err := direction.EntryAddress()
	if err != nil {
		_ = direction.Close()
		return nil, err
	}
	base := uint64(uintptr(unsafe.Pointer(&memory[0])))
	b := nyanjit.NewBuilder()
	buildGame(b, bridge)
	program, err := b.Finish()
	if err != nil {
		_ = direction.Close()
		return nil, err
	}
	loop, err := compile.CompileGoWithBindings(program, nyanjit.CompileOptions{
		ABI: nyanjit.ABIGo, Stack: nyanjit.StackPlatform, StackBytesAvailable: 240, AssumePointerFree: true,
	}, map[string]uint64{"board": base, "segments": base + segmentsAt, "state": base + stateAt, "direction": uint64(directionEntry)})
	if err != nil {
		_ = direction.Close()
		return nil, err
	}
	return &gameCode{loop: loop, direction: direction}, nil
}

func run() error {
	memory := make([]byte, memoryBytes)
	var pinner runtime.Pinner
	pinner.Pin(&memory[0])
	defer pinner.Unpin()
	if err := termbox.Init(); err != nil {
		return err
	}
	defer termbox.Close()
	code, err := compileGame(memory)
	if err != nil {
		return err
	}
	defer code.Close()
	events := make(chan termbox.Event, 16)
	inputEvents = events
	go func() {
		for {
			events <- termbox.PollEvent()
		}
	}()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	tickEvents = ticker.C
	terminalErr = nil
	result, err := code.Call()
	if err != nil {
		return err
	}
	if result != 0 {
		if terminalErr != nil {
			return terminalErr
		}
		return fmt.Errorf("terminal operation failed")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
