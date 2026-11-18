// Copyright 2025 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package schema

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/juju/juju/core/database/schema"
	"github.com/juju/juju/core/semversion"
)

const (
	postPatchFileSuffix = ".PATCH.sql"
)

// PatchSource identifies a patch in the complete, ordered domain schema.
type PatchSource struct {
	// Name is the schema-relative SQL file path, or the registering Go file
	// followed by "#patch-NNNN", where NNNN is the zero-based patch position.
	// The latter identifies a registration, not a source line or trigger.
	Name  string
	Patch schema.Patch
}

type versionedPostPatchFiles struct {
	version semversion.Number
	files   []string
}

// ReadFileDirFS is the interface implemented by a file system that
// provides an optimized implementation of [ReadFile] and [ReadDir].
type ReadFileDirFS interface {
	fs.ReadFileFS
	fs.ReadDirFS
}

// readPatches reads all the patch files in the given baseDir from the provided
// file system. We exclude any post patch files, which are identified by their
// file name suffix .PATCH.sql.
func readPatches(fs ReadFileDirFS, baseDir string) ([]PatchSource, error) {
	entries, err := fs.ReadDir(baseDir)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}

	slices.Sort(names)

	var patches []PatchSource
	for _, name := range names {
		if strings.HasSuffix(name, postPatchFileSuffix) {
			continue
		}

		data, err := fs.ReadFile(filepath.Join(baseDir, name))
		if err != nil {
			return nil, err
		}

		patches = append(patches, PatchSource{
			Name:  filepath.ToSlash(filepath.Join(baseDir, name)),
			Patch: schema.MakePatch(string(data)),
		})
	}
	return patches, nil
}

func appendGoPatchSources(sources []PatchSource, file string, patches []func() schema.Patch) []PatchSource {
	for _, patch := range patches {
		sources = append(sources, PatchSource{
			Name:  fmt.Sprintf("%s#patch-%04d", file, len(sources)),
			Patch: patch(),
		})
	}
	return sources
}

func readPostPatchesForVersion(
	fs fs.ReadFileFS,
	baseDir string,
	registry []versionedPostPatchFiles,
	version semversion.Number,
) ([]PatchSource, error) {
	var files []string
	for _, postPatch := range registry {
		if postPatch.version.Compare(version) <= 0 {
			files = append(files, postPatch.files...)
		}
	}
	return readPostPatches(fs, baseDir, files)
}

func postPatchFiles(baseDir string, registry []versionedPostPatchFiles) []string {
	var files []string
	for _, entry := range registry {
		for _, name := range entry.files {
			files = append(files, filepath.ToSlash(filepath.Join(baseDir, name)))
		}
	}
	return files
}

// readPostPatches reads the specified post patch files from the given baseDir.
func readPostPatches(fs fs.ReadFileFS, baseDir string, postPatchFiles []string) ([]PatchSource, error) {
	patches := make([]PatchSource, len(postPatchFiles))
	for i, name := range postPatchFiles {
		data, err := fs.ReadFile(filepath.Join(baseDir, name))
		if err != nil {
			return nil, err
		}

		patches[i] = PatchSource{
			Name:  filepath.ToSlash(filepath.Join(baseDir, name)),
			Patch: schema.MakePatch(string(data)),
		}
	}
	return patches, nil
}
