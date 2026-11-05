// Command schemagen writes the JSON Schemas generated from the configuration
// Go types into a directory, and the configuration reference generated from
// them. Run it from the repository root: make schema.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/kentrow/tapyr/internal/config"
)

func main() {
	out := flag.String("out", "schemas/v1alpha1", "output directory")
	docs := flag.String("docs", "", "directory of the generated configuration reference, none if empty")
	flag.Parse()
	if err := generate(*out, *docs); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func generate(out, docs string) error {
	files, err := config.GenerateSchemas("github.com/kentrow/tapyr", "internal/config")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(out, name), files[name], 0o600); err != nil {
			return err
		}
		fmt.Println("wrote", filepath.Join(out, name))
	}
	if docs == "" {
		return nil
	}
	return writeReference(files, docs)
}
