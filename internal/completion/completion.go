package completion

import (
	"fmt"
	"strings"
)

const Command = "completion"

var Shells = []string{"bash", "zsh", "fish"}

type Flag struct {
	Name   string
	Usage  string
	Bool   bool
	Values []string
}

func Script(shell, binary string, flags []Flag) (string, error) {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "bash":
		return bash(binary, flags), nil
	case "zsh":
		return zsh(binary, flags), nil
	case "fish":
		return fish(binary, flags), nil
	default:
		return "", fmt.Errorf("unknown shell %q (want %s)", shell, strings.Join(Shells, ", "))
	}
}

func (f Flag) token() string {
	if len(f.Name) == 1 {
		return "-" + f.Name
	}
	return "--" + f.Name
}

func (f Flag) dashForms() []string {
	if len(f.Name) == 1 {
		return []string{"-" + f.Name}
	}
	return []string{"-" + f.Name, "--" + f.Name}
}

type valueGroup struct {
	forms  []string
	values []string
}

func valueGroups(flags []Flag) []valueGroup {
	var out []valueGroup
	at := map[string]int{}
	for _, f := range flags {
		if len(f.Values) == 0 {
			continue
		}
		key := strings.Join(f.Values, " ")
		if i, ok := at[key]; ok {
			out[i].forms = append(out[i].forms, f.dashForms()...)
			continue
		}
		at[key] = len(out)
		out = append(out, valueGroup{forms: f.dashForms(), values: f.Values})
	}
	return out
}

func tokens(flags []Flag) string {
	list := make([]string, 0, len(flags))
	for _, f := range flags {
		list = append(list, f.token())
	}
	return strings.Join(list, " ")
}

func ident(binary string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			return r
		default:
			return '_'
		}
	}, binary)
}

func bash(binary string, flags []Flag) string {
	fn := "_" + ident(binary)

	var b strings.Builder
	fmt.Fprintf(&b, "# bash completion for %s\n\n%s() {\n", binary, fn)
	b.WriteString("    local cur prev\n")
	b.WriteString("    cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("    prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n\n")

	fmt.Fprintf(&b, "    if [[ ${COMP_CWORD} -eq 2 && ${COMP_WORDS[1]} == %s ]]; then\n", Command)
	fmt.Fprintf(&b, "        COMPREPLY=($(compgen -W %q -- \"${cur}\"))\n", strings.Join(Shells, " "))
	b.WriteString("        return 0\n    fi\n\n")

	if groups := valueGroups(flags); len(groups) > 0 {
		b.WriteString("    case \"${prev}\" in\n")
		for _, g := range groups {
			fmt.Fprintf(&b, "        %s)\n", strings.Join(g.forms, "|"))
			fmt.Fprintf(&b, "            COMPREPLY=($(compgen -W %q -- \"${cur}\"))\n", strings.Join(g.values, " "))
			b.WriteString("            return 0\n            ;;\n")
		}
		b.WriteString("    esac\n\n")
	}

	b.WriteString("    if [[ \"${cur}\" == -* ]]; then\n")
	fmt.Fprintf(&b, "        COMPREPLY=($(compgen -W %q -- \"${cur}\"))\n", tokens(flags))
	b.WriteString("        return 0\n    fi\n\n")

	b.WriteString("    if [[ ${COMP_CWORD} -eq 1 ]]; then\n")
	fmt.Fprintf(&b, "        COMPREPLY=($(compgen -W %q -- \"${cur}\"))\n", Command)
	b.WriteString("        return 0\n    fi\n\n")
	b.WriteString("    COMPREPLY=()\n    return 0\n}\n\n")
	fmt.Fprintf(&b, "complete -F %s %s\n", fn, binary)
	return b.String()
}

func zsh(binary string, flags []Flag) string {
	fn := "_" + ident(binary)

	var b strings.Builder
	fmt.Fprintf(&b, "#compdef %s\n\n%s() {\n", binary, fn)
	b.WriteString("    local -a flags\n    flags=(\n")
	for _, f := range flags {
		fmt.Fprintf(&b, "        %s\n", zshSpec(f))
	}
	b.WriteString("    )\n\n")

	fmt.Fprintf(&b, "    if [[ ${words[2]-} == %s ]]; then\n", Command)
	fmt.Fprintf(&b, "        _arguments '1:command:(%s)' '2:shell:(%s)'\n", Command, strings.Join(Shells, " "))
	b.WriteString("        return\n    fi\n\n")

	fmt.Fprintf(&b, "    _arguments -s $flags '1: :%s_first' '*:domain:_hosts'\n}\n\n", fn)
	fmt.Fprintf(&b, "%s_first() {\n", fn)
	fmt.Fprintf(&b, "    _alternative 'commands:command:(%s)' 'domains:domain:_hosts'\n}\n\n", Command)
	fmt.Fprintf(&b, "%s \"$@\"\n", fn)
	return b.String()
}

func zshSpec(f Flag) string {
	spec := f.token() + "[" + zshDesc(f.Usage) + "]"
	if !f.Bool {
		arg := f.Name
		if len(arg) == 1 {
			arg = "value"
		}
		spec += ":" + arg + ":"
		if len(f.Values) > 0 {
			spec += "(" + strings.Join(f.Values, " ") + ")"
		}
	}
	return "'" + strings.ReplaceAll(spec, "'", `'\''`) + "'"
}

func zshDesc(usage string) string {
	r := strings.NewReplacer("[", "(", "]", ")", ":", " -", "\n", " ")
	return strings.TrimSpace(r.Replace(usage))
}

func fish(binary string, flags []Flag) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# fish completion for %s\n\n", binary)
	fmt.Fprintf(&b, "complete -c %s -f\n", binary)
	fmt.Fprintf(&b, "complete -c %s -n __fish_use_subcommand -a %s -d %s\n",
		binary, Command, fishQuote("print a shell completion script"))
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_subcommand_from %s' -a %s -d shell\n",
		binary, Command, fishQuote(strings.Join(Shells, " ")))

	for _, f := range flags {
		line := "complete -c " + binary
		if len(f.Name) == 1 {
			line += " -s " + f.Name
		} else {
			line += " -l " + f.Name + " -o " + f.Name
		}
		if !f.Bool {
			line += " -x"
			if len(f.Values) > 0 {
				line += " -a " + fishQuote(strings.Join(f.Values, " "))
			}
		}
		if f.Usage != "" {
			line += " -d " + fishQuote(f.Usage)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func fishQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + strings.ReplaceAll(s, "\n", " ") + "'"
}
