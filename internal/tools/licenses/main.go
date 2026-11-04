// Command licenses writes the third-party notices of the tapyr binary: the
// license and NOTICE files of every module linked into ./cmd/tapyr and of
// the Go standard library, then the notices of the web UI bundle when
// given (ui/dist/licenses.txt, written by the UI build). The releases ship
// the result next to the binary, as the licenses of these dependencies
// (Apache-2.0 section 4(d) for the NOTICE files) require. It fails when a
// module has no license file. Run it from the repository root: make
// licenses.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// noticeFile matches the license and notice files of a module.
var noticeFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents)([.-][a-z0-9.-]*)?$`)

type module struct {
	Path, Version, Dir string
	Main               bool
}

func main() {
	out := flag.String("out", "THIRD_PARTY_LICENSES", "output file")
	ui := flag.String("ui", "", "notices of the web UI bundle to append (ui/dist/licenses.txt)")
	flag.Parse()
	if err := generate(context.Background(), *out, *ui); err != nil {
		fmt.Fprintln(os.Stderr, "licenses:", err)
		os.Exit(1)
	}
}

func generate(ctx context.Context, out, ui string) error {
	mods, err := linkedModules(ctx)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	b.WriteString("Third-party notices of tapyr\n\n" +
		"The tapyr binary includes the following software. Each section holds the\n" +
		"license and notice files of a Go module, of the Go standard library, and of\n" +
		"the packages bundled in the web UI.\n")
	goroot, goVersion, err := goEnv(ctx)
	if err != nil {
		return err
	}
	mods = append([]module{{Path: "Go standard library", Version: goVersion, Dir: goroot}}, mods...)
	for _, m := range mods {
		files, err := noticeFiles(m.Dir)
		if err != nil {
			return fmt.Errorf("%s: %w", m.Path, err)
		}
		if len(files) == 0 {
			return fmt.Errorf("%s %s: no license file in %s", m.Path, m.Version, m.Dir)
		}
		for _, f := range files {
			data, err := os.ReadFile(filepath.Join(m.Dir, f))
			if err != nil {
				return err
			}
			fmt.Fprintf(&b, "\n%s\n%s %s: %s\n%s\n\n", strings.Repeat("=", 78), m.Path, m.Version, f, strings.Repeat("=", 78))
			b.Write(bytes.TrimSpace(data))
			b.WriteString("\n")
		}
	}
	if ui != "" {
		data, err := os.ReadFile(ui)
		if err != nil {
			return fmt.Errorf("notices of the web UI: %w", err)
		}
		b.WriteString("\n")
		b.Write(data)
	}
	// Public notices, readable by every user of the image.
	return os.WriteFile(out, b.Bytes(), 0o644) //nolint:gosec // G306: the notices are public.
}

// linkedModules returns the modules of the packages linked into
// ./cmd/tapyr, the main module excepted, sorted by path.
func linkedModules(ctx context.Context) ([]module, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-json=Module", "./cmd/tapyr")
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	seen := map[string]module{}
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var p struct{ Module *module }
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		if p.Module != nil && !p.Module.Main {
			seen[p.Module.Path] = *p.Module
		}
	}
	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	slices.SortFunc(mods, func(a, b module) int { return strings.Compare(a.Path, b.Path) })
	return mods, nil
}

// noticeFiles lists the license and notice files at the root of dir.
func noticeFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && noticeFile.MatchString(e.Name()) {
			files = append(files, e.Name())
		}
	}
	return files, nil
}

// goEnv returns the GOROOT and the version of the Go toolchain.
func goEnv(ctx context.Context) (root, version string, err error) {
	out, err := exec.CommandContext(ctx, "go", "env", "GOROOT", "GOVERSION").Output()
	if err != nil {
		return "", "", fmt.Errorf("go env: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", "", fmt.Errorf("go env: unexpected output %q", out)
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
}
