package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/sirupsen/logrus"
)

const typebuildSyntax = "//syntax=rumpl/typebuild\n\n"
const typebuildImport = "https://raw.githubusercontent.com/rumpl/typebuild-node/main/index.ts"

func Codegen(stages []instructions.Stage, meta []instructions.ArgCommand, output string, format bool) error {
	if err := writeArgs(output, argToMap(meta)); err != nil {
		return err
	}

	for _, stage := range stages {
		if err := codegenStage(stages, stage, argToMap(meta), output); err != nil {
			return err
		}
	}

	if format {
		return prettier(output)
	}
	return nil
}

func prettier(output string) error {
	out, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	return exec.Command("docker", "run", "--rm", "-v", fmt.Sprintf("%s:/work", out), "tmknom/prettier", "--write", "--parser=typescript", "*.ts").Run()
}

func writeArgs(output string, meta map[string]string) error {
	var sb strings.Builder
	sb.WriteString(typebuildSyntax)
	sb.WriteString(fmt.Sprintf("import %q", typebuildImport))

	for k, v := range meta {
		sb.WriteString(fmt.Sprintf("export const %s = buildArg(%q, %q);\n", k, k, v))
	}

	return writeFile(output, "args", sb)
}

func argToMap(meta []instructions.ArgCommand) map[string]string {
	ret := map[string]string{}
	for _, m := range meta {
		for _, arg := range m.Args {
			ret[arg.Key] = *arg.Value
		}
	}
	return ret
}

func codegenStage(stages []instructions.Stage, stage instructions.Stage, meta map[string]string, output string) error {
	var sb strings.Builder

	sb.WriteString(typebuildSyntax)

	foundBase := false
	for _, s := range stages {
		if stage.BaseName == s.Name {
			foundBase = true
		}
	}

	if !foundBase {
		sb.WriteString(fmt.Sprintf("import { Image } from %q;\n\n", typebuildImport))
	}

	mounts := getMounts(stage)
	for _, m := range mounts {
		sb.WriteString(fmt.Sprintf("import { "+m+" } from %q;\n", typebuildImport))
	}

	if len(mounts) > 0 {
		sb.WriteString("\n")
	}

	imports := getImports(stage)
	for _, im := range imports {
		toImport := strcase.ToLowerCamel(im)
		sb.WriteString(fmt.Sprintf("import %[1]s from \"./%[1]s.ts\";\n", toImport))
	}

	if len(imports) != 0 {
		sb.WriteString("\n")
	}

	argsToImport := []string{}
	for _, arg := range append(getArgs(stage), getUsedArgs(convertArgs(stage.BaseName))...) {
		if _, ok := meta[arg]; ok {
			argsToImport = append(argsToImport, fmt.Sprintf("%s as ARG_%s", arg, arg))
		}
	}

	if len(argsToImport) != 0 {
		sb.WriteString(fmt.Sprintf("import { %s } from \"./args.ts\";\n", strings.Join(argsToImport, ", ")))
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
			if _, ok := meta[arg]; ok {
				sb.WriteString(fmt.Sprintf("const %s = ARG_%s;\n", arg, arg))
			} else {
				sb.WriteString("const " + arg + " = buildArg(\"" + arg + "\");\n")
			}
		}

		if len(usedArgs) != 0 {
			sb.WriteString("\n")
		}

		plat := ""
		aa := []string{}
		if stage.Platform != "" {
			plat = fmt.Sprintf(", `%s`", stage.Platform)
			aa = getUsedArgs(convertArgs(stage.Platform))
		}
		for _, a := range aa {
			if _, ok := meta[a]; ok {
				sb.WriteString(fmt.Sprintf("const %s = ARG_%s;\n", a, a))
			} else {
				sb.WriteString(fmt.Sprintf("const %[1]s = buildArg(%[1]q);\n", a))
			}
		}
		if len(aa) != 0 {
			sb.WriteString("\n")
		}

		sb.WriteString(fmt.Sprintf("const %s = new Image(`%s`%s);\n\n", name, convertArgs(stage.BaseName), convertArgs(plat)))
	}

	args := getArgs(stage)
	for _, arg := range args {
		if _, ok := meta[arg]; ok {
			sb.WriteString(fmt.Sprintf("const %[1]s = ARG_%[1]s;\n", arg))
		} else {
			sb.WriteString(fmt.Sprintf("const %[1]s = buildArg(%[1]q);\n", arg))
		}
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
			cb.WriteString(fmt.Sprintf("\n  .entrypoint([%s])", strings.Join(commands, ", ")))
		case *instructions.WorkdirCommand:
			cb.WriteString(fmt.Sprintf("\n  .workdir(%q)", c.Path))
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

	return writeFile(output, name, sb)
}

func writeFile(output string, name string, sb strings.Builder) error {
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
