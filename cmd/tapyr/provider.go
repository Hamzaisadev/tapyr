package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/provider"
)

func newProviderListCmd(e *env, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List built-in and user provider packages",
		Long: "List the built-in provider packages and, when a configuration is given,\n" +
			"the packages of its providerPaths, in precedence order.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			loader, builtin, err := newLoader()
			if err != nil {
				return err
			}
			var pkgs []*config.Package
			if paths := configPaths(e, flags); len(paths) > 0 {
				if pkgs, err = userPackages(loader, paths); err != nil {
					return err
				}
			}
			pkgs = append(pkgs, builtin...)
			w := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tVERSION\tTEMPLATES\tSIMULATOR\tSTATUS\tORIGIN")
			seen := map[config.Name]bool{}
			for _, p := range pkgs {
				status := "active"
				if seen[p.Provider.Metadata.Name] {
					status = "overridden"
				}
				seen[p.Provider.Metadata.Name] = true
				sim := "no"
				if p.Simulation != nil {
					sim = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", p.Provider.Metadata.Name, p.Provider.Metadata.Version,
					len(p.JobTemplates), sim, status, p.Origin)
			}
			return w.Flush()
		},
	}
}

// userPackages loads the packages of the providerPaths of a configuration.
func userPackages(loader *config.Loader, paths []string) ([]*config.Package, error) {
	cfg, err := loader.Parse(paths)
	if err != nil {
		return nil, &exitError{code: exitInvalidConfig, err: err}
	}
	if cfg.Settings == nil {
		return nil, nil
	}
	var pkgs []*config.Package
	for _, dir := range cfg.Settings.Spec.ProviderPaths {
		found, err := provider.FromPath(loader, dir)
		if err != nil {
			return nil, &exitError{code: exitInvalidConfig, err: err}
		}
		pkgs = append(pkgs, found...)
	}
	return pkgs, nil
}
