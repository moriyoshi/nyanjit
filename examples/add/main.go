package main

import (
	"fmt"
	"log"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func main() {
	b := nyanjit.NewBuilder()
	b.Return(b.Add(b.ParamNative(), b.ParamNative()))
	program, err := b.Finish()
	if err != nil {
		log.Fatal(err)
	}
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		log.Fatal(err)
	}
	unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
	if err != nil {
		log.Fatal(err)
	}
	template, err := unit.Template()
	if err != nil {
		log.Fatal(err)
	}
	code, err := template.Bind(nil)
	if err != nil {
		log.Fatal(err)
	}
	defer code.Close()

	result, err := code.Call(20, 22)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
