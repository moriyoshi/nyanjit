// snake runs generated game rules in the Go process on supported Linux targets.
// Go handles terminal input and output; Builder emits each game step.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

const (
	width         = 16
	height        = 10
	stride        = width + 1
	interiorCells = (width - 2) * (height - 2)
	prefix        = "\x1b[H\x1b[2J"
	maxSegments   = interiorCells
)

const (
	lengthField     = 0
	directionField  = 1
	headField       = 2
	nextField       = 3
	foodField       = 4
	ateField        = 5
	shiftIndexField = 6
	scanIndexField  = 7
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" && runtime.GOARCH != "386" && runtime.GOARCH != "riscv64" && runtime.GOARCH != "ppc64le" {
		return compile.ErrUnsupported
	}
	return runGame(os.Stdin, os.Stdout)
}

func runGame(input io.Reader, output io.Writer) error {
	board, gridEnd, head, food := initialBoard()
	inputOffset := align8(len(board))
	segmentsOffset := align8(inputOffset + 1)
	stateOffset := align8(segmentsOffset + maxSegments)
	data := make([]byte, stateOffset+8)
	copy(data, board)
	data[segmentsOffset] = byte(head)
	data[segmentsOffset+1] = byte(head - 1)
	data[segmentsOffset+2] = byte(head - 2)
	data[stateOffset+lengthField] = 3
	data[stateOffset+directionField] = 1
	data[stateOffset+headField] = byte(head)
	data[stateOffset+foodField] = byte(food)
	var pinner runtime.Pinner
	pinner.Pin(&data[0])
	defer pinner.Unpin()
	code, err := compileSnake(uintptr(unsafe.Pointer(&data[0])), gridEnd, inputOffset, segmentsOffset, stateOffset)
	if err != nil {
		return err
	}
	defer code.Close()
	reader := bufio.NewReader(input)
	for {
		if err := writeAll(output, data[:len(board)]); err != nil {
			return err
		}
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		if len(line) == 0 {
			return nil
		}
		data[inputOffset] = line[0]
		result, err := code.Call()
		if err != nil {
			return err
		}
		switch result {
		case 0:
		case 1:
			return writeAll(output, []byte("\nGame over!\n"))
		case 2:
			return writeAll(output, []byte("\nYou win!\n"))
		case 3:
			return nil
		default:
			return fmt.Errorf("generated game returned status %d", result)
		}
	}
}

func writeAll(output io.Writer, data []byte) error {
	n, err := output.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func align8(n int) int { return (n + 7) &^ 7 }

func initialBoard() ([]byte, int, int, int) {
	board := []byte(prefix)
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			cell := byte(' ')
			if row == 0 || row == height-1 || column == 0 || column == width-1 {
				cell = '#'
			}
			board = append(board, cell)
		}
		board = append(board, '\n')
	}
	gridEnd := len(board)
	head := len(prefix) + 5*stride + 8
	food := len(prefix) + 3*stride + 10
	board[head] = '@'
	board[head-1], board[head-2] = 'o', 'o'
	board[food] = '*'
	board = append(board, []byte("\nWASD + Enter to move; q + Enter to quit\n")...)
	return board, gridEnd, head, food
}

func compileSnake(dataAddress uintptr, gridEnd, inputOffset, segmentsOffset, stateOffset int) (*execution.Code, error) {
	b := nyanjit.NewBuilder()
	load := func(address nyanjit.Value) nyanjit.Value {
		return b.CastNative(b.LoadBits(address, 8))
	}
	store := func(address, value nyanjit.Value) {
		b.StoreBits(address, b.CastUnsigned(value, 8))
	}
	var (
		inputCheck, checkW, setW, doW, checkA, setA, doA, checkS               nyanjit.Label
		setS, doS, checkD, setD, doD, move, checkSpace, ordinary               nyanjit.Label
		eat, eatDo, afterTail, shift, shiftBody, shiftDone, foodInit, foodScan nyanjit.Label
		foodWrap, foodCandidate, foodPlace, gameOver, win, quit, done          nyanjit.Label
	)
	for _, label := range []*nyanjit.Label{&inputCheck, &checkW, &setW, &doW, &checkA, &setA, &doA, &checkS, &setS, &doS, &checkD, &setD, &doD, &move, &checkSpace, &ordinary, &eat, &eatDo, &afterTail, &shift, &shiftBody, &shiftDone, &foodInit, &foodScan, &foodWrap, &foodCandidate, &foodPlace, &gameOver, &win, &quit, &done} {
		*label = b.NewLabel()
	}
	branch := func(value nyanjit.Value, immediate int64, condition ir.BranchCondition, yes, no nyanjit.Label) {
		predicate := b.CompareImmediate(b.CastUnsigned(value, 8), condition, immediate)
		b.Branch(predicate, yes)
		b.Jump(no)
	}
	exit := func(status uint64) { b.Return(b.ConstBits(8, status)) }
	board := dataAddress
	input := board + uintptr(inputOffset)
	segments := board + uintptr(segmentsOffset)
	state := board + uintptr(stateOffset)

	b.Bind(inputCheck)
	key := b.CastUnsigned(load(b.ConstNative(input)), 8)
	quitLower := b.CompareImmediate(key, ir.BranchEqual, 'q')
	quitUpper := b.CompareImmediate(key, ir.BranchEqual, 'Q')
	b.Branch(quitLower.Or(quitUpper), quit)
	b.Jump(checkW)

	b.Bind(checkW)
	branch(load(b.ConstNative(input)), 'w', ir.BranchEqual, setW, checkA)
	b.Bind(setW)
	branch(load(b.ConstNative(state+directionField)), stride, ir.BranchEqual, move, doW)
	b.Bind(doW)
	store(b.ConstNative(state+directionField), b.ConstNative(^uintptr(stride-1)))
	b.Jump(move)

	b.Bind(checkA)
	branch(load(b.ConstNative(input)), 'a', ir.BranchEqual, setA, checkS)
	b.Bind(setA)
	branch(load(b.ConstNative(state+directionField)), 1, ir.BranchEqual, move, doA)
	b.Bind(doA)
	store(b.ConstNative(state+directionField), b.ConstNative(^uintptr(0)))
	b.Jump(move)

	b.Bind(checkS)
	branch(load(b.ConstNative(input)), 's', ir.BranchEqual, setS, checkD)
	b.Bind(setS)
	branch(load(b.ConstNative(state+directionField)), 256-stride, ir.BranchEqual, move, doS)
	b.Bind(doS)
	store(b.ConstNative(state+directionField), b.ConstNative(stride))
	b.Jump(move)

	b.Bind(checkD)
	branch(load(b.ConstNative(input)), 'd', ir.BranchEqual, setD, move)
	b.Bind(setD)
	branch(load(b.ConstNative(state+directionField)), 255, ir.BranchEqual, move, doD)
	b.Bind(doD)
	store(b.ConstNative(state+directionField), b.ConstNative(1))
	b.Jump(move)

	b.Bind(move)
	next := b.And(b.Add(load(b.ConstNative(state+headField)), load(b.ConstNative(state+directionField))), b.ConstNative(255))
	store(b.ConstNative(state+nextField), next)
	branch(load(b.ConstNative(board).Add(next)), '*', ir.BranchEqual, eat, checkSpace)

	b.Bind(checkSpace)
	branch(load(b.ConstNative(board).Add(load(b.ConstNative(state+nextField)))), ' ', ir.BranchNotEqual, gameOver, ordinary)

	b.Bind(ordinary)
	store(b.ConstNative(state+ateField), b.ConstNative(0))
	tailIndex := b.Sub(load(b.ConstNative(state+lengthField)), b.ConstNative(1))
	tail := load(b.ConstNative(segments).Add(tailIndex))
	store(b.ConstNative(board).Add(tail), b.ConstNative(uintptr(' ')))
	b.Jump(afterTail)

	b.Bind(eat)
	branch(load(b.ConstNative(state+lengthField)), interiorCells-1, ir.BranchAboveEqual, win, eatDo)
	b.Bind(eatDo)
	store(b.ConstNative(state+lengthField), b.Add(load(b.ConstNative(state+lengthField)), b.ConstNative(1)))
	store(b.ConstNative(state+ateField), b.ConstNative(1))
	b.Jump(afterTail)

	b.Bind(afterTail)
	store(b.ConstNative(state+shiftIndexField), b.Sub(load(b.ConstNative(state+lengthField)), b.ConstNative(1)))
	b.Jump(shift)
	b.Bind(shift)
	branch(load(b.ConstNative(state+shiftIndexField)), 0, ir.BranchEqual, shiftDone, shiftBody)
	b.Bind(shiftBody)
	index := load(b.ConstNative(state + shiftIndexField))
	previous := b.Sub(index, b.ConstNative(1))
	store(b.ConstNative(segments).Add(index), load(b.ConstNative(segments).Add(previous)))
	store(b.ConstNative(state+shiftIndexField), previous)
	b.Jump(shift)

	b.Bind(shiftDone)
	store(b.ConstNative(board).Add(load(b.ConstNative(state+headField))), b.ConstNative(uintptr('o')))
	next = load(b.ConstNative(state + nextField))
	store(b.ConstNative(segments), next)
	store(b.ConstNative(state+headField), next)
	store(b.ConstNative(board).Add(next), b.ConstNative(uintptr('@')))
	branch(load(b.ConstNative(state+ateField)), 0, ir.BranchEqual, done, foodInit)

	b.Bind(foodInit)
	store(b.ConstNative(state+scanIndexField), load(b.ConstNative(state+foodField)))
	b.Jump(foodScan)
	b.Bind(foodScan)
	scan := b.Add(load(b.ConstNative(state+scanIndexField)), b.ConstNative(1))
	store(b.ConstNative(state+scanIndexField), scan)
	branch(scan, int64(gridEnd), ir.BranchBelow, foodCandidate, foodWrap)
	b.Bind(foodWrap)
	store(b.ConstNative(state+scanIndexField), b.ConstNative(uintptr(len(prefix)+stride+1)))
	b.Jump(foodCandidate)
	b.Bind(foodCandidate)
	branch(load(b.ConstNative(board).Add(load(b.ConstNative(state+scanIndexField)))), ' ', ir.BranchNotEqual, foodScan, foodPlace)
	b.Bind(foodPlace)
	scan = load(b.ConstNative(state + scanIndexField))
	store(b.ConstNative(board).Add(scan), b.ConstNative(uintptr('*')))
	store(b.ConstNative(state+foodField), scan)
	b.Jump(done)

	b.Bind(gameOver)
	exit(1)
	b.Bind(win)
	exit(2)
	b.Bind(quit)
	exit(3)
	b.Bind(done)
	exit(0)
	program, err := b.Finish()
	if err != nil {
		return nil, err
	}
	return nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{
		ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096},
	})
}
