package main

import (
	"os"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/rumpl/typeconvert/pkg/codegen"
	"github.com/sirupsen/logrus"
)

func main() {
	f, err := os.Open(os.Args[1])
	if err != nil {
		logrus.Fatal(err)
	}
	b, err := parser.Parse(f)
	if err != nil {
		logrus.Fatal(err)
	}

	stages, _, err := instructions.Parse(b.AST)
	if err != nil {
		logrus.Fatal(err)
	}

	codegen.Codegen(stages)

}
