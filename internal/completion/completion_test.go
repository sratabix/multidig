package completion

import (
	"strings"
	"testing"
)

func flags() []Flag {
	return []Flag{
		{Name: "t", Usage: "record types", Values: []string{"A", "MX"}},
		{Name: "types", Usage: "record types", Values: []string{"A", "MX"}},
		{Name: "ipv6", Usage: "also use IPv6 resolvers", Bool: true},
		{Name: "watch", Usage: "auto-refresh interval, e.g. 10s"},
		{Name: "quote", Usage: "the source's checked_at [unreliable]: really"},
	}
}

func TestScriptRejectsUnknownShells(t *testing.T) {
	if _, err := Script("nushell", "multidig", flags()); err == nil {
		t.Fatal("unknown shell was accepted")
	}
}

func TestEveryShellRenders(t *testing.T) {
	for _, shell := range Shells {
		script, err := Script(shell, "multidig", flags())
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		for _, want := range []string{"multidig", "types", "ipv6", "watch", Command, "bash zsh fish"} {
			if !strings.Contains(script, want) {
				t.Errorf("%s script is missing %q", shell, want)
			}
		}
	}
}

func TestBashGroupsFlagsThatShareValues(t *testing.T) {
	script, err := Script("bash", "multidig", flags())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "-t|-types|--types)") {
		t.Errorf("aliases did not share one case arm:\n%s", script)
	}
	if strings.Count(script, "compgen -W \"A MX\"") != 1 {
		t.Errorf("value list was emitted more than once:\n%s", script)
	}
}

func TestZshEscapesDescriptions(t *testing.T) {
	script, err := Script("zsh", "multidig", flags())
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(script, "\n") {
		if strings.Contains(l, "--quote") {
			line = strings.TrimSpace(l)
		}
	}
	if line == "" {
		t.Fatal("the --quote flag is missing")
	}
	want := `'--quote[the source'\''s checked_at (unreliable) - really]:quote:'`
	if line != want {
		t.Errorf("spec = %s, want %s", line, want)
	}
}

func TestZshUsesAReadableValueNameForShortFlags(t *testing.T) {
	script, err := Script("zsh", "multidig", flags())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "'-t[record types]:value:(A MX)'") {
		t.Errorf("short flag spec is off:\n%s", script)
	}
}

func TestFishQuotesDescriptions(t *testing.T) {
	script, err := Script("fish", "multidig", flags())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, `-l quote -o quote -x -d 'the source\'s checked_at [unreliable]: really'`) {
		t.Errorf("description was not quoted:\n%s", script)
	}
	if !strings.Contains(script, "-l ipv6 -o ipv6 -d ") {
		t.Error("a boolean flag was given an argument")
	}
}
