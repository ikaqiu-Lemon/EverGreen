package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `migrate-storage-v3 migrates one manifest-defined Vault snapshot.

Usage:
  go run ./scripts/migrate-storage-v3 --vault <dir> --manifest <file> [--apply]

The default is a read-only dry-run. --apply writes the complete accepted
write-set through the journal v1 transaction implementation. The tool never
calls a model or the network and does not create a Git commit.
`

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdout, os.Stderr))
}

func runMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate-storage-v3", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = io.WriteString(stderr, usage) }
	vault := fs.String("vault", "", "Vault root")
	manifest := fs.String("manifest", "", "migration manifest JSON")
	apply := fs.Bool("apply", false, "apply through journal v1 (default: dry-run)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *vault == "" || *manifest == "" {
		fs.Usage()
		return 2
	}

	report, err := Run(Options{
		VaultRoot:    *vault,
		ManifestPath: *manifest,
		Apply:        *apply,
	})
	if err != nil {
		var reported *RunError
		if errors.As(err, &reported) && reported.Report != nil {
			_ = writeJSON(stdout, reported.Report)
		}
		_, _ = fmt.Fprintf(stderr, "migrate-storage-v3: %v\n", err)
		return 1
	}
	if err := writeJSON(stdout, report); err != nil {
		_, _ = fmt.Fprintf(stderr, "migrate-storage-v3: write report: %v\n", err)
		return 1
	}
	return 0
}

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
