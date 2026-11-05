// Command docsite checks the Markdown documentation of the repository and
// exports the pages of the documentation site docs.tapyr.eu (ADR 0010).
// Run it from the repository root:
//
//	go run ./internal/tools/docsite check
//	go run ./internal/tools/docsite export -ref v0.1.1 -out site -index site.json
//
// check validates docs/site.yml and every relative link of the Markdown
// files, anchors included (make lint). export writes the published pages at
// their site paths with their links rewritten, and the navigation; the site
// repository runs it on a checkout of a release tag.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const usage = `usage:
  docsite check [-root dir]
  docsite export [-root dir] -ref ref -out dir [-index file]`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "check":
		err = runCheck(args)
	case "export":
		err = runExport(args)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "docsite:", err)
		os.Exit(1)
	}
}

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	root := fs.String("root", ".", "repository root")
	_ = fs.Parse(args)
	files, err := markdownFiles(*root)
	if err != nil {
		return err
	}
	problems := check(*root, files)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d problems in the documentation", len(problems))
	}
	fmt.Printf("checked %d Markdown files\n", len(files))
	return nil
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	root := fs.String("root", ".", "repository root")
	ref := fs.String("ref", "", "Git ref the pages come from (a release tag or main), for the links to GitHub")
	out := fs.String("out", "", "output directory, created, must be empty")
	index := fs.String("index", "", "file to write the navigation and the page sources to (JSON)")
	_ = fs.Parse(args)
	if *ref == "" || *out == "" {
		return fmt.Errorf("export needs -ref and -out")
	}
	n, err := export(*root, *ref, *out, *index)
	if err != nil {
		return err
	}
	fmt.Printf("exported %d pages to %s\n", n, *out)
	return nil
}

// markdownFiles lists the Markdown files of the repository that Git tracks
// or would track (untracked files not ignored included).
func markdownFiles(root string) ([]string, error) {
	cmd := exec.CommandContext(context.Background(), "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.md")
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list the Markdown files: %w", err)
	}
	var files []string
	for f := range strings.SplitSeq(string(b), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}
