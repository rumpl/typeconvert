package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/sirupsen/logrus"
)

func Codegen(stages []instructions.Stage, output string) error {
	for _, stage := range stages {
		if err := codegenStage(stages, stage, output); err != nil {
			return err
		}
	}

	return nil
}

func codegenStage(stages []instructions.Stage, stage instructions.Stage, output string) error {
	var sb strings.Builder
	foundBase := false
	for _, s := range stages {
		if stage.BaseName == s.Name {
			foundBase = true
		}
	}
	if !foundBase {
		sb.WriteString("import { Stage } from \"https://raw.githubusercontent.com/rumpl/typebuild-node/main/index.ts\";\n\n")
	}

	imports := getImports(stage)
	for _, im := range imports {
		sb.WriteString("import " + im + " from \"./" + im + ".ts\";\n")
	}

	if len(imports) != 0 {
		sb.WriteString("\n")
	}

	name := stage.Name
	if name == "" {
		name = stage.BaseName
	}

	if foundBase {
		sb.WriteString("import " + stage.BaseName + " from \"./" + stage.BaseName + ".ts\";\n\n")
		sb.WriteString("const " + name + ` = ` + stage.BaseName + ";\n\n")
	} else {
		sb.WriteString("const " + name + ` = new Stage("` + name + `", "` + stage.BaseName + "\");\n\n")
	}

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
		case *instructions.CopyCommand:
			from := c.From
			source := strings.Join(c.SourcePaths, ",")
			destination := c.DestPath
			cb.WriteString("\n  .copy({\n")
			if from != "" {
				cb.WriteString("    from: " + from + ",\n")
			}
			cb.WriteString("    source: \"" + source + "\",\n    destination: \"" + destination + "\"\n  })")
		case *instructions.LabelCommand:
			for _, label := range c.Labels {
				cb.WriteString("\n  .label(\"" + label.Key + "\", \"" + label.Value + "\")")
			}
		case *instructions.UserCommand:
			cb.WriteString("\n  .user(\"" + c.User + "\")")
		default:
			logrus.Fatalf("unknown instruction %v", c)
		}
	}

	sb.WriteString("export default " + name + cb.String() + ";\n")

	f, err := os.Create(getFileName(output, name))
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(f, sb.String())

	return err
}

func getFileName(outDir string, stageName string) string {
	return filepath.Join(outDir, strcase.ToLowerCamel(stageName)+".ts")
}

func getImports(stage instructions.Stage) []string {
	imports := []string{}
	for _, command := range stage.Commands {
		switch c := command.(type) {
		case *instructions.CopyCommand:
			if c.From != "" {
				imports = append(imports, c.From)
			}
		}
	}
	return imports
}
