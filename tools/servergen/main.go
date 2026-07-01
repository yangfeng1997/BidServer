package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type cliOptions struct {
	Name         string
	Pkg          string
	Kind         string
	RegisterEnv  string
	DryRun       bool
	Force        bool
}

func run(args []string) error {
	var opts cliOptions
	fs := flag.NewFlagSet("servergen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.Name, "name", "", "service name, used for cmd and env registration")
	fs.StringVar(&opts.Pkg, "pkg", "", "server package name, defaults to name trimmed by svr suffix")
	fs.StringVar(&opts.Kind, "kind", "standard", "service kind: standard or sidecar")
	fs.StringVar(&opts.RegisterEnv, "register-env", "", "comma-separated env names to register into svr_list")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "print plan without writing files")
	fs.BoolVar(&opts.Force, "force", false, "overwrite generated shell files")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(opts.Name) == "" {
		return errors.New("--name is required")
	}

	gen, err := NewGenerator(GeneratorConfig{
		Name:        strings.TrimSpace(opts.Name),
		Pkg:         strings.TrimSpace(opts.Pkg),
		Kind:        strings.TrimSpace(opts.Kind),
		RegisterEnv: splitCSV(opts.RegisterEnv),
		DryRun:      opts.DryRun,
		Force:       opts.Force,
	})
	if err != nil {
		return err
	}

	plan, err := gen.Plan()
	if err != nil {
		return err
	}
	gen.PrintPlan(plan)
	if opts.DryRun {
		return nil
	}
	return gen.Write(plan)
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}
