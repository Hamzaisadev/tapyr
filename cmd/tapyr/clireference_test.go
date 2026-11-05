package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// cliReference is the command line reference, generated from the commands
// (ADR 0010). TestCLIReference keeps it up to date.
const cliReference = "../../docs/reference/cli.md"

// TestCLIReference fails when the command line reference drifts from the
// commands. Fix it with: go test ./cmd/tapyr -run TestCLIReference -update.
func TestCLIReference(t *testing.T) {
	got := renderCLIReference(newRootCmd(&env{lookupEnv: func(string) (string, bool) { return "", false }}))
	if *update {
		if err := os.WriteFile(cliReference, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(cliReference)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s is out of date: go test ./cmd/tapyr -run TestCLIReference -update", cliReference)
	}
}

func renderCLIReference(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString("<!-- Generated from the commands by TestCLIReference (go test ./cmd/tapyr -run TestCLIReference -update). Do not edit. -->\n\n")
	b.WriteString("# Command line\n\n")
	b.WriteString(mdText(root.Long) + "\n\n")
	b.WriteString("Every command reads the configuration from `--config` (files or directories, repeatable) or `TAPYR_CONFIG`.\n\n")
	b.WriteString("## Global flags\n\n")
	flagTable(&b, root.PersistentFlags())
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if !sub.IsAvailableCommand() || sub.Name() == "completion" {
				continue
			}
			if sub.Runnable() {
				commandSection(&b, sub)
			}
			walk(sub)
		}
	}
	walk(root)
	b.WriteString("\n## Shell completion\n\n`tapyr completion bash|zsh|fish|powershell` prints the completion script of a shell; `tapyr completion <shell> --help` explains how to load it.\n")
	return b.String()
}

func commandSection(b *strings.Builder, c *cobra.Command) {
	fmt.Fprintf(b, "\n## %s\n\n", c.CommandPath())
	desc := c.Long
	if desc == "" {
		desc = c.Short
	}
	b.WriteString(mdText(desc) + "\n\n")
	fmt.Fprintf(b, "```text\n%s\n```\n", c.UseLine())
	if c.HasAvailableLocalFlags() {
		b.WriteString("\n")
		flagTable(b, c.LocalNonPersistentFlags())
	}
}

func flagTable(b *strings.Builder, flags *pflag.FlagSet) {
	b.WriteString("| Flag | Default | Description |\n|---|---|---|\n")
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		name := "`--" + f.Name
		if f.Shorthand != "" {
			name = "`-" + f.Shorthand + "`, " + name
		}
		if t := f.Value.Type(); t != "bool" {
			name += " " + strings.TrimSuffix(t, "Array")
		}
		name += "`"
		def := ""
		if f.DefValue != "" && f.DefValue != "[]" && f.DefValue != "false" && f.DefValue != "0" {
			def = "`" + f.DefValue + "`"
		}
		fmt.Fprintf(b, "| %s | %s | %s |\n", name, def, strings.ReplaceAll(mdText(f.Usage), "|", `\|`))
	})
}

// mdText turns help text into one Markdown line, without the characters
// Markdown would take for HTML.
func mdText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(strings.Join(strings.Fields(s), " "))
}
