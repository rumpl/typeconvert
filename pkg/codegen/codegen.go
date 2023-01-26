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
	if err := writeArgsFile(output, argToMap(meta)); err != nil {
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

	// We don't really care if prettier failed or not
	exec.Command("docker", "run", "--rm", "-w", "/work", "-v", fmt.Sprintf("%s:/work", out), "tmknom/prettier", "--write", "--parser=typescript", "*.ts").Run() // nolint
	return nil
}

func writeArgsFile(output string, meta map[string]string) error {
	if len(meta) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(typebuildSyntax)
	sb.WriteString(fmt.Sprintf("import %q;\n", typebuildImport))

	for k, v := range meta {
		sb.WriteString(fmt.Sprintf("export const %s = buildArg(%q, %q);\n", k, k, v))
	}

	return writeFile(output, "args", sb)
}

func argToMap(meta []instructions.ArgCommand) map[string]string {
	ret := map[string]string{}
	for _, m := range meta {
		for _, arg := range m.Args {
			if arg.Value != nil {
				ret[arg.Key] = *arg.Value
			}
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
	if len(mounts) != 0 {
		sb.WriteString(fmt.Sprintf("import { %s } from %q;\n", strings.Join(mounts, ", "), typebuildImport))
	}

	imports, err := getImports(stage)
	if err != nil {
		return err
	}
	for _, im := range imports {
		toImport := strcase.ToLowerCamel(im)
		sb.WriteString(fmt.Sprintf("import %[1]s from \"./%[1]s.ts\";\n", toImport))
	}

	newline(imports, &sb)

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
		name = strcase.ToLowerCamel(stage.BaseName)
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

		newline(usedArgs, &sb)

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

		newline(aa, &sb)

		sb.WriteString(fmt.Sprintf("const %s = new Image(`%s`%s);\n\n", name, convertArgs(stage.BaseName), convertArgs(plat)))
	}

	var usedArgs []string
	args := getArgs(stage)
	for _, arg := range args {
		if _, ok := meta[arg]; ok {
			sb.WriteString(fmt.Sprintf("const %[1]s = ARG_%[1]s;\n", arg))
		} else {
			sb.WriteString(fmt.Sprintf("const %[1]s = buildArg(%[1]q);\n", arg))
			usedArgs = append(usedArgs, arg)
		}
	}

	newline(args, &sb)

	cb, err := codegenCommands(stage)
	if err != nil {
		return err
	}

	prefix := name
	for _, usedArg := range usedArgs {
		prefix += fmt.Sprintf(".env(%[1]q, %[1]s)\n", usedArg)
	}

	sb.WriteString(fmt.Sprintf("export default %s;\n", prefix+cb))

	return writeFile(output, name, sb)
}

func newline(s []string, sb *strings.Builder) {
	if len(s) != 0 {
		sb.WriteString("\n")
	}
}

func codegenCommands(stage instructions.Stage) (string, error) {
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
			if err := codegenRun(c, &cb); err != nil {
				return "", err
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
		case *instructions.CmdCommand:
			cmd := []string{}
			for _, s := range c.CmdLine {
				cmd = append(cmd, fmt.Sprintf("%q", s))
			}
			cb.WriteString("\n  .cmd([" + strings.Join(cmd, ", ") + "])")
		case *instructions.ExposeCommand:
			for _, port := range c.Ports {
				cb.WriteString("\n  .expose(" + port + ")")
			}
		default:
			logrus.Warnf("unknown instruction %v", c)
		}
	}
	return cb.String(), nil
}

func codegenRun(c *instructions.RunCommand, cb *strings.Builder) error {
	if err := c.Expand(func(word string) (string, error) { return word, nil }); err != nil {
		return err
	}

	cb.WriteString("\n  .run(`" + convertArgs(strings.Join(c.CmdLine, "")) + "`")
	mounts := instructions.GetMounts(c)
	if len(mounts) == 0 {
		cb.WriteString(")")
		return nil
	}

	cb.WriteString(", [")

	for _, mount := range mounts {
		id := ""
		from := ""
		source := ""
		target := ""
		rw := ""
		mode := ""
		uid := ""
		gid := ""
		required := ""
		size := ""
		sharing := ""
		if mount.CacheID != "" {
			id = fmt.Sprintf(`"id": "%s",`, mount.CacheID)
		}
		if mount.From != "" {
			from = fmt.Sprintf(`"from": %s,`, strcase.ToLowerCamel(mount.From))
		}
		if mount.Source != "" {
			source = fmt.Sprintf(`"source": "%s",`, mount.Source)
		}
		if mount.Target != "" {
			target = fmt.Sprintf(`"target": "%s",`, mount.Target)
		}
		if !mount.ReadOnly {
			rw = fmt.Sprintf(`"readOnly": %t,`, mount.ReadOnly)
		}
		if mount.Mode != nil {
			mode = fmt.Sprintf(`"mode": %d,`, *mount.Mode)
		}
		if mount.GID != nil {
			gid = fmt.Sprintf(`"gid": %d,`, *mount.Mode)
		}
		if mount.UID != nil {
			gid = fmt.Sprintf(`"uid": %d,`, *mount.Mode)
		}
		if mount.Required {
			gid = fmt.Sprintf(`"required": %t,`, mount.Required)
		}
		if mount.SizeLimit != 0 {
			size = fmt.Sprintf(`"size": %d,`, mount.SizeLimit)
		}
		if mount.CacheSharing != "" {
			sharing = fmt.Sprintf(`"sharing": "%s",`, mount.CacheSharing)
		}

		switch mount.Type {
		case instructions.MountTypeBind:
			cb.WriteString(fmt.Sprintf("new BindMount({ %s %s %s %s }),", from, source, target, rw))
		case instructions.MountTypeCache:
			cb.WriteString(fmt.Sprintf("new CacheRunMount({ %s %s %s %s %s %s %s %s %s }),", id, from, target, uid, gid, source, sharing, rw, mode))
		case instructions.MountTypeTmpfs:
			cb.WriteString(fmt.Sprintf("new TmpfsRunMount({ %s %s }),", target, size))
		case instructions.MountTypeSecret:
			cb.WriteString(fmt.Sprintf("new SecretRunMount({ %s %s %s %s %s %s }),", id, required, target, mode, uid, gid))
		case instructions.MountTypeSSH:
			cb.WriteString(fmt.Sprintf("new SSHRunMount({ %s %s %s %s %s %s }),", id, required, target, mode, uid, gid))
		}
	}

	cb.WriteString("])")

	return nil
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

func getImports(stage instructions.Stage) ([]string, error) {
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
			if err := c.Expand(func(w string) (string, error) { return w, nil }); err != nil {
				return nil, err
			}
			mounts := instructions.GetMounts(c)
			for _, mount := range mounts {
				if mount.From != "" {
					imports = append(imports, mount.From)
				}
			}
		}
	}

	return unique(imports), nil
}

func getMounts(stage instructions.Stage) []string {
	imports := []string{}
	for _, command := range stage.Commands {
		switch c := command.(type) {
		case *instructions.RunCommand:
			c.Expand(func(word string) (string, error) { return word, nil }) // nolint

			mounts := instructions.GetMounts(c)
			for _, m := range mounts {
				switch m.Type {
				case instructions.MountTypeBind:
					imports = append(imports, "BindMount")
				case instructions.MountTypeCache:
					imports = append(imports, "CacheRunMount")
				case instructions.MountTypeTmpfs:
					imports = append(imports, "TmpfsRunMount")
				case instructions.MountTypeSecret:
					imports = append(imports, "SecretRunMount")
				case instructions.MountTypeSSH:
					imports = append(imports, "SSHRunMount")
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
