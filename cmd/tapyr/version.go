package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kentrow/tapyr/internal/buildinfo"
)

func newVersionCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, revision and Go version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			info := buildinfo.Get()
			_, err := fmt.Fprintf(e.stdout, "tapyr %s\n  revision: %s\n  go:       %s\n  platform: %s\n",
				info.Version, info.Revision, info.GoVersion, info.Platform)
			return err
		},
	}
}
