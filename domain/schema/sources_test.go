// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package schema

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/juju/tc"

	coreschema "github.com/juju/juju/core/database/schema"
	"github.com/juju/juju/core/semversion"
	coreversion "github.com/juju/juju/core/version"
	coretesting "github.com/juju/juju/internal/testing"
)

type patchSourcesSuite struct {
	coretesting.BaseSuite
}

func TestPatchSourcesSuite(t *testing.T) {
	tc.Run(t, &patchSourcesSuite{})
}

func (s *patchSourcesSuite) TestCompleteSources(c *tc.C) {
	for _, test := range []struct {
		name    string
		fs      ReadFileDirFS
		sources func(semversion.Number) []PatchSource
		ddl     func(semversion.Number) *coreschema.Schema
	}{
		{"controller", controllerSchemaDir, ControllerPatchSourcesForVersion, ControllerDDLForVersion},
		{"model", modelSchemaDir, ModelPatchSourcesForVersion, ModelDDLForVersion},
	} {
		version := coreversion.Current
		sources := test.sources(version)
		patches := test.ddl(version).Patches()
		c.Assert(sources, tc.HasLen, len(patches))

		dir := path.Join("dqlite", test.name, "sql")
		entries, err := test.fs.ReadDir(dir)
		c.Assert(err, tc.ErrorIsNil)
		var files []string
		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasSuffix(entry.Name(), postPatchFileSuffix) {
				files = append(files, path.Join(dir, entry.Name()))
			}
		}
		c.Check(len(sources) > len(files), tc.IsTrue)

		seen := make(map[string]bool)
		for i, source := range sources {
			c.Check(source.Patch, tc.DeepEquals, patches[i])
			c.Check(seen[source.Name], tc.IsFalse, tc.Commentf("%s", source.Name))
			seen[source.Name] = true
			if strings.HasSuffix(source.Name, ".sql") {
				data, err := test.fs.ReadFile(source.Name)
				c.Assert(err, tc.ErrorIsNil)
				c.Check(coreschema.Stmt(source.Patch), tc.Equals, string(data))
			} else {
				c.Check(source.Name, tc.Equals, fmt.Sprintf("%s.go#patch-%04d", test.name, i))
			}
			if i < len(files) {
				c.Check(source.Name, tc.Equals, files[i])
			}
		}
	}
}

func (s *patchSourcesSuite) TestVersionSelectedPostPatchesFollowGoPatches(c *tc.C) {
	current := coreversion.Current
	current.Tag = ""
	current.Build = 0
	previous := current
	current.Patch++
	future := current
	future.Patch++
	registry := []versionedPostPatchFiles{{
		version: current,
		files:   []string{"0003-later.PATCH.sql", "0001-earlier.PATCH.sql"},
	}, {
		version: previous,
		files:   []string{"0004-previous.PATCH.sql"},
	}, {
		version: future,
		files:   []string{"0002-future.PATCH.sql"},
	}}

	for _, test := range []struct {
		name    string
		sources func(semversion.Number, ReadFileDirFS, []versionedPostPatchFiles) []PatchSource
	}{
		{"controller", controllerPatchSourcesForVersion},
		{"model", modelPatchSourcesForVersion},
	} {
		dir := path.Join("dqlite", test.name, "sql")
		fixture := fstest.MapFS{
			path.Join(dir, "0000-base.sql"): {
				Data: []byte("-- base bytes\nCREATE TABLE t (value TEXT);\n"),
			},
			path.Join(dir, "0001-earlier.PATCH.sql"): {
				Data: []byte("INSERT INTO t (value) VALUES ('second post-patch');\n"),
			},
			path.Join(dir, "0003-later.PATCH.sql"): {
				Data: []byte("ALTER TABLE t ADD COLUMN n INT;\n"),
			},
			path.Join(dir, "0004-previous.PATCH.sql"): {
				Data: []byte("INSERT INTO t (value) VALUES ('previous version');\n"),
			},
		}
		base := test.sources(previous, fixture, nil)
		c.Assert(len(base) > 1, tc.IsTrue)
		c.Check(base[0].Name, tc.Equals, path.Join(dir, "0000-base.sql"))
		c.Check(base[len(base)-1].Name, tc.Equals,
			fmt.Sprintf("%s.go#patch-%04d", test.name, len(base)-1))

		withPrevious := test.sources(previous, fixture, registry)
		c.Assert(withPrevious, tc.HasLen, len(base)+1)
		c.Check(withPrevious[:len(base)], tc.DeepEquals, base)
		c.Check(withPrevious[len(base)].Name, tc.Equals, path.Join(dir, "0004-previous.PATCH.sql"))

		// A future registered file need not exist for an earlier version.
		withPostPatches := test.sources(current, fixture, registry)
		c.Assert(withPostPatches, tc.HasLen, len(base)+3)
		c.Check(withPostPatches[:len(base)], tc.DeepEquals, base)
		c.Check(withPostPatches[len(base)].Name, tc.Equals, path.Join(dir, "0003-later.PATCH.sql"))
		c.Check(withPostPatches[len(base)+1].Name, tc.Equals, path.Join(dir, "0001-earlier.PATCH.sql"))
		c.Check(withPostPatches[len(base)+2].Name, tc.Equals, path.Join(dir, "0004-previous.PATCH.sql"))
		for _, source := range withPostPatches[len(base):] {
			data, err := fixture.ReadFile(source.Name)
			c.Assert(err, tc.ErrorIsNil)
			c.Check(coreschema.Stmt(source.Patch), tc.Equals, string(data))
		}

		fixture[path.Join(dir, "0002-future.PATCH.sql")] = &fstest.MapFile{
			Data: []byte("UPDATE t SET n = 2;\n"),
		}
		withFuture := test.sources(future, fixture, registry)
		c.Assert(withFuture, tc.HasLen, len(withPostPatches)+1)
		c.Check(withFuture[:len(withPostPatches)], tc.DeepEquals, withPostPatches)
		c.Check(withFuture[len(withPostPatches)].Name, tc.Equals, path.Join(dir, "0002-future.PATCH.sql"))
	}
}

func (s *patchSourcesSuite) TestRejectDifferentMajorMinor(c *tc.C) {
	for _, test := range []struct {
		name    string
		sources func(semversion.Number) []PatchSource
		ddl     func(semversion.Number) *coreschema.Schema
	}{
		{"controller", ControllerPatchSourcesForVersion, ControllerDDLForVersion},
		{"model", ModelPatchSourcesForVersion, ModelDDLForVersion},
	} {
		wrongMajor := coreversion.Current
		wrongMajor.Major++
		wrongMinor := coreversion.Current
		wrongMinor.Minor++
		for _, version := range []semversion.Number{wrongMajor, wrongMinor} {
			message := fmt.Sprintf("Cannot return the %s DDL for a different major.minor version", test.name)
			c.Check(func() { test.sources(version) }, tc.PanicMatches, message)
			c.Check(func() { test.ddl(version) }, tc.PanicMatches, message)
		}
	}
}

func (s *patchSourcesSuite) TestMissingSelectedPostPatch(c *tc.C) {
	version := coreversion.Current
	registry := []versionedPostPatchFiles{{
		version: version,
		files:   []string{"missing.PATCH.sql"},
	}}
	_, err := readPostPatchesForVersion(fstest.MapFS{}, "sql", registry, version)
	c.Assert(err, tc.ErrorIs, fs.ErrNotExist)
	c.Check(err, tc.ErrorMatches, ".*sql/missing.PATCH.sql.*")
}

func (s *patchSourcesSuite) TestMissingBaseDirectory(c *tc.C) {
	_, err := readPatches(fstest.MapFS{}, "missing")
	c.Assert(err, tc.ErrorIs, fs.ErrNotExist)
}

func (s *patchSourcesSuite) TestGoPatchSourcesPreservePatches(c *tc.C) {
	base := PatchSource{Name: "sql/0000-base.sql", Patch: coreschema.MakePatch("SELECT 1;\n")}
	patch := coreschema.MakePatch("INSERT INTO t VALUES (?);\n", "bound value")
	calls := 0
	sources := appendGoPatchSources([]PatchSource{base}, "model.go", []func() coreschema.Patch{
		func() coreschema.Patch {
			calls++
			return patch
		},
	})
	c.Assert(sources, tc.HasLen, 2)
	c.Check(calls, tc.Equals, 1)
	c.Check(sources[0], tc.DeepEquals, base)
	c.Check(sources[1].Name, tc.Equals, "model.go#patch-0001")
	c.Check(sources[1].Patch, tc.DeepEquals, patch)
}
