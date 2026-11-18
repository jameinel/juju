// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/juju/utils/v4"
)

// syncOutput preflights ownership and file sets before making changes.
// Check mode never creates directories or repairs files. Write mode deletes
// only stale files carrying this generator's ownership header.
func syncOutput(dir string, out output, check bool) error {
	if err := rejectSymlinkDirs(dir); err != nil {
		return err
	}
	for _, name := range sortedKeys(out) {
		parts := strings.Split(name, "/")
		if len(parts) != 3 || parts[0] != "controller" && parts[0] != "model" ||
			parts[1] != "sql" && parts[1] != "overrides" ||
			!strings.HasSuffix(parts[2], ".sql") || path.Clean(name) != name ||
			strings.ContainsAny(name, "\\\x00") || !strings.HasPrefix(out[name], generatedHeader) {
			return fmt.Errorf("invalid generated output %q", name)
		}
	}
	existing := make(map[string]string)
	// Scan just the generator-owned subdirectories. Other namespace files
	// (documentation, embeds, Go constructors) are not generator output.
	for _, ns := range []string{"controller", "model"} {
		for _, sub := range []string{"sql", "overrides"} {
			base := filepath.Join(dir, ns, sub)
			if err := rejectSymlinkDirs(base); err != nil {
				return err
			}
			entries, err := os.ReadDir(base)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			for _, entry := range entries {
				name := path.Join(ns, sub, entry.Name())
				if !entry.Type().IsRegular() {
					return fmt.Errorf("unexpected non-regular output %q", name)
				}
				if sub == "overrides" && strings.HasSuffix(entry.Name(), ".diff") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(base, entry.Name()))
				if err != nil {
					return err
				}
				if !strings.HasSuffix(entry.Name(), ".sql") || !strings.HasPrefix(string(data), generatedHeader) {
					return fmt.Errorf("refusing to overwrite or remove unowned file %q", name)
				}
				existing[name] = string(data)
			}
		}
	}
	if check {
		for _, name := range sortedKeys(out) {
			got, exists := existing[name]
			if !exists {
				return fmt.Errorf("missing generated file %s", name)
			}
			if got != out[name] {
				return fmt.Errorf("generated file %s is out of date", name)
			}
		}
		for _, name := range sortedKeys(existing) {
			if _, expected := out[name]; !expected {
				return fmt.Errorf("extra generated file %s", name)
			}
		}
		return nil
	}
	for _, name := range sortedKeys(out) {
		if existing[name] == out[name] {
			continue
		}
		filename := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			return err
		}
		if err := utils.AtomicWriteFile(filename, []byte(out[name]), 0o644); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(existing) {
		if _, expected := out[name]; !expected {
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
				return err
			}
		}
	}
	return nil
}

func rejectSymlinkDirs(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for {
		info, err := os.Lstat(dir)
		if os.IsNotExist(err) {
			// An absent child can still have an existing symlink parent.
		} else if err != nil {
			return err
		} else if !info.IsDir() {
			return fmt.Errorf("not a real directory: %s", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}
