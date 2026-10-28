// md-orphans finds Markdown documents without incoming documentation links.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
)

var version = "dev"

type stringsFlag []string

func (s *stringsFlag) String() string     { return strings.Join(*s, ", ") }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("md-orphans", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var roots, excludes stringsFlag
	flags.Var(&roots, "root", "entry-point document allowed to have no incoming link (repeatable)")
	flags.Var(&excludes, "exclude", "exclude a repository-relative glob (repeatable; supports **)")
	configPath := flags.String("config", "", "config path relative to repository (default .md-orphans.toml)")
	noConfig := flags.Bool("no-config", false, "ignore the configuration file")
	jsonOutput := flags.Bool("json", false, "emit a JSON array")
	reachable := flags.Bool("reachable", false, "require every document to be reachable from an entry point")
	showVersion := flags.Bool("version", false, "print version")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: md-orphans [options] [path inside repository]\n\nFind Markdown documents without incoming links. Options:")
		flags.PrintDefaults()
	}
	// Accept options before or after the path, matching the original Python CLI.
	ordered, err := orderArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "md-orphans:", err)
		return 2
	}
	if err := flags.Parse(ordered); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		v := version
		if info, ok := debug.ReadBuildInfo(); ok && v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		fmt.Fprintln(stdout, "md-orphans", v)
		return 0
	}
	fail := func(err error) int { fmt.Fprintln(stderr, "md-orphans:", err); return 2 }
	if flags.NArg() > 1 {
		return fail(errors.New("expected at most one repository path"))
	}
	if *noConfig && *configPath != "" {
		return fail(errors.New("--config and --no-config are mutually exclusive"))
	}
	start := "."
	if flags.NArg() == 1 {
		start = flags.Arg(0)
	}
	cmd := exec.Command("git", "-C", start, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return fail(fmt.Errorf("cannot locate Git repository at %q: %w", start, err))
	}
	root := strings.TrimSuffix(strings.TrimSuffix(string(out), "\n"), "\r")
	config := Config{}
	if !*noConfig {
		name := *configPath
		if name == "" {
			name = ".md-orphans.toml"
		}
		if !filepath.IsAbs(name) {
			name = filepath.Join(root, name)
		}
		config, err = loadConfig(name, *configPath != "")
		if err != nil {
			return fail(err)
		}
	}
	config.Roots = append(config.Roots, roots...)
	config.Exclude = append(config.Exclude, excludes...)
	if len(config.Roots) == 0 {
		config.Roots = []string{"README.md"}
	}
	results, err := scan(root, config, *reachable)
	if err != nil {
		return fail(err)
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(results)
	} else {
		for _, result := range results {
			reason := "unlinked"
			if *reachable {
				reason = "unreachable"
			}
			_, err = fmt.Fprintf(stdout, "%s: %s", result.Path, reason)
			if err != nil {
				break
			}
			if result.SuggestedParent != nil {
				_, err = fmt.Fprintf(stdout, "; add a reference from %s", *result.SuggestedParent)
			}
			if err != nil {
				break
			}
			_, err = fmt.Fprintln(stdout)
			if err != nil {
				break
			}
		}
		if len(results) == 0 {
			_, err = fmt.Fprintln(stdout, "No unlinked Markdown files.")
		}
	}
	if err != nil {
		return fail(err)
	}
	if len(results) > 0 {
		return 1
	}
	return 0
}

// orderArgs only rearranges CLI arguments; Markdown is parsed by Goldmark.
func orderArgs(args []string) ([]string, error) {
	var options, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		options = append(options, arg)
		switch strings.TrimLeft(arg, "-") {
		case "root", "exclude", "config":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("%s needs a value", arg)
			}
			options = append(options, args[i])
		}
	}
	return append(append(options, "--"), positional...), nil
}
