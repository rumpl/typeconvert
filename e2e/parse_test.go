package e2e

import (
	"fmt"
	"os"
	"testing"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/rumpl/typeconvert/pkg/codegen"
)

func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"parse": func() int {
			f, err := os.Open("Dockerfile")
			if err != nil {
				fmt.Println(err)
				return -1
			}
			b, err := parser.Parse(f)
			if err != nil {
				fmt.Println(err)
				return -1
			}

			stages, _, err := instructions.Parse(b.AST)
			if err != nil {
				fmt.Println(err)
				return -1
			}

			if err := codegen.Codegen(stages, []instructions.ArgCommand{}, "", false); err != nil {
				return -1
			}
			return 0
		},
	}))
}

func TestFoo(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata",
	})
}
