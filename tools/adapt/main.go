// Command adapt writes the ADAPT-based schemas of openapi.yaml from the pinned
// ADAPT files in adapt/ and the deviations declared in adapt/deviations.yaml.
//
// Each generated schema replaces the schema of the same name in
// components/schemas, so the layout of openapi.yaml is kept. The command fails
// on every ADAPT change deviations.yaml does not account for: a property neither
// kept nor excluded, an exclusion or relaxation that no longer applies, a
// requirement dropped without being declared, and a type that no longer
// matches. See adapt/deviations.yaml for its format.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "fail if openapi.yaml is not up to date, without writing it")
	flag.Parse()

	if err := run(*root, *check); err != nil {
		fmt.Fprintln(os.Stderr, "adapt:", err)
		os.Exit(1)
	}
}

func run(root string, check bool) error {
	dir := filepath.Join(root, "adapt")
	lock, err := readLock(dir)
	if err != nil {
		return err
	}
	std, err := loadADAPT(dir, lock)
	if err != nil {
		return err
	}
	prof, err := readProfile(filepath.Join(dir, "deviations.yaml"))
	if err != nil {
		return err
	}

	specPath := filepath.Join(root, "openapi.yaml")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	existing, err := readComponents(spec)
	if err != nil {
		return err
	}

	schemas, notes, errs := generate(prof, std, existing)
	for _, n := range notes {
		fmt.Fprintln(os.Stderr, "adapt: note:", n)
	}
	if len(errs) > 0 {
		return errors.New("adapt/deviations.yaml does not match ADAPT " + lock.Tag + ":\n  " + strings.Join(errs, "\n  "))
	}

	out, err := splice(spec, schemas)
	if err != nil {
		return err
	}
	if err := verify(out, schemas); err != nil {
		return err
	}

	if bytes.Equal(out, spec) {
		return nil
	}
	if check {
		return errors.New("openapi.yaml is out of date with adapt/; run tools/adapt/generate.sh")
	}
	return os.WriteFile(specPath, out, 0o644)
}
