package main

import (
	"log"
	"os"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/rumpl/typeconvert/pkg/codegen"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name: "typeconvert",
		Authors: []*cli.Author{
			{
				Name:  "Djordje Lukic",
				Email: "djordje.lukic@docker.com",
			},
		},
		Usage:     "convert your dockerfiles to typebuild files",
		UsageText: "typeconvert [DOCKERFILE] [OUTPUT_DIR]",
		Action: func(ctx *cli.Context) error {
			if ctx.Args().Len() != 2 {
				return cli.Exit("A dockerfile expected and an output directory, none given", -1)
			}

			dockerfile := ctx.Args().First()
			output := ctx.Args().Get(1)
			f, err := os.Open(dockerfile)
			if err != nil {
				return err
			}

			b, err := parser.Parse(f)
			if err != nil {
				return err
			}

			stages, meta, err := instructions.Parse(b.AST)
			if err != nil {
				return err
			}

			return codegen.Codegen(stages, meta, output)
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
