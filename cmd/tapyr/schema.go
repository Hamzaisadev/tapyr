package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/schemas"
)

func newSchemaCmd(e *env) *cobra.Command {
	var kind, out string
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print or write the JSON Schemas of the configuration",
		Long: "Print the JSON Schema accepting any document kind (tapyr.json), or the\n" +
			"schema of one kind with --kind. With --out, write the schemas to a directory.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			files, err := schemaFiles(kind)
			if err != nil {
				return &exitError{code: exitFailure, err: err}
			}
			if out == "" {
				name := config.CombinedSchemaFile
				if kind != "" {
					name = files[0]
				}
				b, err := fs.ReadFile(schemas.V1alpha1, "v1alpha1/"+name)
				if err != nil {
					return err
				}
				_, err = e.stdout.Write(b)
				return err
			}
			if err := os.MkdirAll(out, 0o750); err != nil {
				return err
			}
			for _, name := range files {
				b, err := fs.ReadFile(schemas.V1alpha1, "v1alpha1/"+name)
				if err != nil {
					return err
				}
				path := filepath.Join(out, name)
				if err := os.WriteFile(path, b, 0o600); err != nil {
					return err
				}
				fmt.Fprintln(e.stdout, "wrote", path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "kind to print or write ("+strings.Join(kindNames(), ", ")+")")
	cmd.Flags().StringVar(&out, "out", "", "directory to write the schemas to")
	return cmd
}

// kindNames returns the names of the kinds with a schema.
func kindNames() []string {
	names := make([]string, len(config.AllKinds()))
	for i, k := range config.AllKinds() {
		names[i] = string(k)
	}
	return names
}

// schemaFiles returns the schema file of kind, or every schema file.
func schemaFiles(kind string) ([]string, error) {
	if kind == "" {
		files := []string{config.CombinedSchemaFile}
		for _, k := range config.AllKinds() {
			files = append(files, config.SchemaFile(k))
		}
		return files, nil
	}
	for _, k := range config.AllKinds() {
		if strings.EqualFold(kind, string(k)) {
			return []string{config.SchemaFile(k)}, nil
		}
	}
	return nil, fmt.Errorf("unknown kind %q (want one of %s)", kind, strings.Join(kindNames(), ", "))
}
