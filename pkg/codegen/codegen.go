package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/sirupsen/logrus"
)

func Codegen(stages []instructions.Stage, meta []instructions.ArgCommand, output string) error {
	for _, stage := range stages {
		if err := codegenStage(stages, stage, meta, output); err != nil {
			return err
		}
	}

	return nil
}

func codegenStage(stages []instructions.Stage, stage instructions.Stage, meta []instructions.ArgCommand, output string) error {
	var sb strings.Builder

	sb.WriteString("//syntax=rumpl/typebuild\n\n")
	foundBase := false
	for _, s := range stages {
		if stage.BaseName == s.Name {
			foundBase = true
		}
	}
	if !foundBase {
		sb.WriteString("import { Image } from \"https://raw.githubusercontent.com/rumpl/typebuild-node/main/index.ts\";\n\n")
	}

	mounts := getMounts(stage)
	for _, m := range mounts {
		sb.WriteString("import {" + m + "} from \"https://raw.githubusercontent.com/rumpl/typebuild-node/main/index.ts\";\n\n")
	}

	imports := getImports(stage)
	for _, im := range imports {
		sb.WriteString("import " + strcase.ToLowerCamel(im) + " from \"./" + strcase.ToLowerCamel(im) + ".ts\";\n")
	}

	if len(imports) != 0 {
		sb.WriteString("\n")
	}

	name := strcase.ToLowerCamel(stage.Name)
	if name == "" {
		name = stage.BaseName
	}

	if foundBase {
		sb.WriteString("import " + strcase.ToLowerCamel(stage.BaseName) + " from \"./" + strcase.ToLowerCamel(stage.BaseName) + ".ts\";\n\n")
		sb.WriteString("const " + name + ` = ` + strcase.ToLowerCamel(stage.BaseName) + ";\n\n")
	} else {
		usedArgs := getUsedArgs(convertArgs(stage.BaseName))
		for _, arg := range usedArgs {
			sb.WriteString("const " + arg + " = buildArg(\"" + arg + "\");\n")
		}

		if len(usedArgs) != 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("const " + name + " = new Image(`" + convertArgs(stage.BaseName) + "`);\n\n")
	}

	args := getArgs(stage)
	for _, arg := range args {
		sb.WriteString("const " + arg + " = buildArg(\"" + arg + "\");\n")
	}

	if len(args) != 0 {
		sb.WriteString("\n")
	}

	var cb strings.Builder
	for _, command := range stage.Commands {
		switch c := command.(type) {
		case *instructions.EntrypointCommand:
			commands := []string{}
			for _, c := range c.CmdLine {
				commands = append(commands, "`"+c+"`")
			}
			cb.WriteString("\n  .entrypoint([" + strings.Join(commands, ", ") + "])")
		case *instructions.WorkdirCommand:
			cb.WriteString("\n  .workdir(`" + c.Path + "`)")
		case *instructions.RunCommand:
			c.Expand(func(word string) (string, error) { return word, nil }) // nolint
			// TODO: manage flags
			cb.WriteString("\n  .run(`" + convertArgs(strings.Join(c.CmdLine, "")) + "`")
			mounts := instructions.GetMounts(c)
			if len(mounts) == 0 {
				cb.WriteString(")")
			}
			if len(mounts) != 0 {
				cb.WriteString(", [")
			}
			for _, mount := range mounts {
				if mount.Type == instructions.MountTypeBind {
					from := ""
					source := ""
					target := ""
					if mount.From != "" {
						from = fmt.Sprintf(`"from": %s,`, strcase.ToLowerCamel(mount.From))
					}
					if mount.Source != "" {
						source = fmt.Sprintf(`"source": "%s",`, mount.Source)
					}
					if mount.Target != "" {
						target = fmt.Sprintf(`"target": "%s",`, mount.Target)
					}
					cb.WriteString(fmt.Sprintf("new BindMount({ %s %s %s }),", from, source, target))
				}
			}
			if len(mounts) != 0 {
				cb.WriteString("])")
			}
		case *instructions.EnvCommand:
			for _, kv := range c.Env {
				cb.WriteString("\n  .env(`" + kv.Key + "`, `" + kv.Value + "`)")
			}
		case *instructions.CopyCommand:
			from := strcase.ToLowerCamel(c.From)
			if strings.Contains(c.From, "/") {
				from = fmt.Sprintf("new Image(\"" + c.From + "\")")
			}
			source := strings.Join(c.SourcePaths, ",")
			destination := c.DestPath
			cb.WriteString("\n  .copy({\n")
			if from != "" {
				cb.WriteString("    from: " + from + ",\n")
			}
			cb.WriteString("    source: `" + source + "`,\n    destination: `" + destination + "`\n  })")
		case *instructions.LabelCommand:
			for _, label := range c.Labels {
				cb.WriteString("\n  .label(`" + label.Key + "`, `" + label.Value + "`)")
			}
		case *instructions.UserCommand:
			cb.WriteString("\n  .user(`" + c.User + "`)")
		case *instructions.VolumeCommand:
			for _, volume := range c.Volumes {
				cb.WriteString("\n  .volume(`" + volume + "`)")
			}
		case *instructions.ArgCommand:
		default:
			logrus.Warnf("unknown instruction %v", c)
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
				if !strings.Contains(c.From, "/") {
					imports = append(imports, strcase.ToLowerCamel(c.From))
				}
			}
		case *instructions.RunCommand:
			c.Expand(func(w string) (string, error) { return w, nil }) // nolint
			mounts := instructions.GetMounts(c)
			for _, mount := range mounts {
				if mount.From != "" {
					imports = append(imports, mount.From)
				}
			}
		}
	}
	return unique(imports)
}

func getMounts(stage instructions.Stage) []string {
	imports := []string{}
	for _, command := range stage.Commands {
		switch c := command.(type) {
		case *instructions.RunCommand:
			mounts := instructions.GetMounts(c)
			for _, m := range mounts {
				if m.Type == instructions.MountTypeBind {
					imports = append(imports, "BindMount")
				}
			}
		}
	}
	return unique(imports)
}

func unique(slice []string) []string {
	// create a map with all the values as key
	uniqMap := make(map[string]struct{})
	for _, v := range slice {
		uniqMap[v] = struct{}{}
	}

	// turn the map keys into a slice
	uniqSlice := make([]string, 0, len(uniqMap))
	for v := range uniqMap {
		uniqSlice = append(uniqSlice, v)
	}
	return uniqSlice
}

func getArgs(stage instructions.Stage) []string {
	args := []string{}
	for _, command := range stage.Commands {
		switch c := command.(type) {
		case *instructions.ArgCommand:
			for _, arg := range c.Args {
				args = append(args, arg.Key)
			}
		}
	}
	return args
}

var argRegex = regexp.MustCompile(`\$(\w+)`)

func convertArgs(cmd string) string {
	return argRegex.ReplaceAllString(cmd, "${$1}")
}

var usedArgRegex = regexp.MustCompile(`\${(\w+)}`)

func getUsedArgs(cmd string) []string {
	found := usedArgRegex.FindAllStringSubmatch(cmd, -1)
	if found == nil {
		return []string{}
	}
	ret := []string{}
	for _, matches := range found {
		ret = append(ret, matches[1])
	}

	return ret
}
