package codegen

import (
	"fmt"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
)

func Codegen(stages []instructions.Stage) {
	var sb strings.Builder
	sb.WriteString("import { Stage } from \"https://raw.githubusercontent.com/rumpl/typebuild-node/main/index.ts\";\n\n")
	def := ""

	for _, stage := range stages {
		name := stage.Name
		if name == "" {
			name = stage.BaseName
		}
		sb.WriteString("const " + name + ` = new Stage("` + name + `", "` + stage.BaseName + "\");\n\n")
		var cb strings.Builder
		for _, command := range stage.Commands {
			switch c := command.(type) {
			case *instructions.EntrypointCommand:
				commands := []string{}
				for _, c := range c.CmdLine {
					commands = append(commands, "\""+c+"\"")
				}
				cb.WriteString("\n  .entrypoint([" + strings.Join(commands, ", ") + "])")
			case *instructions.WorkdirCommand:
				cb.WriteString("\n  .workdir(\"" + c.Path + "\")")
			case *instructions.RunCommand:
				// TODO: manage flags
				cb.WriteString("\n  .run(\"" + strings.Join(c.CmdLine, "") + "\")")
			case *instructions.EnvCommand:
				for _, kv := range c.Env {
					cb.WriteString("\n  .env(\"" + kv.Key + "\", \"" + kv.Value + "\")")
				}
			}
		}
		def = name
		sb.WriteString("export default " + def + cb.String() + ";\n")
	}

	fmt.Println(sb.String())
}
