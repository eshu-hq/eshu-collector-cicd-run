// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coreModulePath is the Eshu core module. The published SDK modules live in
// the standalone eshu-sdk repository (github.com/eshu-hq/eshu-sdk), a
// different prefix.
const coreModulePath = "github.com/eshu-hq/eshu/go"

// TestImportBoundaryRejectsCoreAndCrossCollectorImports is the CI gate that
// keeps a standalone collector repository standalone: no Eshu internal
// implementation imports and no cross-collector implementation imports. Only
// the published SDK modules plus the standard library and yaml are allowed.
// The self-import prefix is derived from go.mod so renaming the module can
// never desync this allowlist.
func TestImportBoundaryRejectsCoreAndCrossCollectorImports(t *testing.T) {
	t.Parallel()

	violations, err := importViolations(".", allowedImportPrefixes(modulePath(t)))
	if err != nil {
		t.Fatalf("importViolations() error = %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("forbidden imports:\n%s", strings.Join(violations, "\n"))
	}
}

func allowedImportPrefixes(module string) []string {
	return []string{
		module,
		"github.com/eshu-hq/eshu-sdk/collector",
		"github.com/eshu-hq/eshu-sdk/factschema",
		"gopkg.in/yaml.v3",
	}
}

// importViolations parses every .go file under root and reports each import
// outside allowedPrefixes. Every directory is scanned, scripts/ and nested
// testdata/ included, so a forbidden import cannot hide in a helper tree. Only
// .git and the top-level testdata/ directory (fixture data, whose .go.txt
// files are not compiled) are skipped.
func importViolations(root string, allowedPrefixes []string) ([]string, error) {
	var violations []string
	walk := func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if rel == ".git" || rel == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == "C" {
				violations = append(violations, path+": cgo import is not allowed")
				continue
			}
			if isStandardOrRelative(importPath) || hasAllowedPrefix(importPath, allowedPrefixes) {
				continue
			}
			violations = append(violations, path+": import "+importPath)
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, err
	}
	return violations, nil
}

func hasAllowedPrefix(importPath string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
			return true
		}
	}
	return false
}

// TestImportBoundaryScansEveryDirectoryButTopLevelTestdata proves the walk on
// a synthetic tree: scripts/, nested testdata/, and internal/ are scanned,
// while top-level testdata/ and .git are not.
func TestImportBoundaryScansEveryDirectoryButTopLevelTestdata(t *testing.T) {
	t.Parallel()

	const bad = "package x\n\nimport _ \"github.com/eshu-hq/eshu/go/internal/facts\"\n"
	root := t.TempDir()
	for path, wantFlagged := range map[string]bool{
		"scripts/tool/main.go":           true,
		"scripts/deep/er/main.go":        true,
		"internal/x/testdata/helper.go":  true,
		"testdata/nested/testdata/zz.go": false,
		"testdata/golden/generator.go":   false,
		".git/hooks/x.go":                false,
		"main.go":                        true,
		"cmd/collector/main.go":          true,
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		violations, err := importViolations(root, allowedImportPrefixes("example.invalid/mod"))
		if err != nil {
			t.Fatal(err)
		}
		flagged := false
		for _, violation := range violations {
			if strings.HasPrefix(violation, full+":") {
				flagged = true
			}
		}
		if flagged != wantFlagged {
			t.Errorf("%s: flagged = %t, want %t", path, flagged, wantFlagged)
		}
	}
}

// TestGoModRequiresNoEshuCoreModule keeps a forbidden dependency from hiding
// behind an unused import: go.mod must not require anything under the Eshu
// core module path, and must not use a replace directive.
func TestGoModRequiresNoEshuCoreModule(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("os.ReadFile(go.mod) error = %v", err)
	}
	if bad := forbiddenGoModEntries(string(raw)); len(bad) > 0 {
		t.Fatalf("go.mod has forbidden entries:\n%s", strings.Join(bad, "\n"))
	}
}

// TestForbiddenGoModEntriesDetectsEveryForm proves the go.mod check on
// synthetic go.mod text, so go.mod itself is never edited to test it.
func TestForbiddenGoModEntriesDetectsEveryForm(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		goMod string
		want  []string
	}{
		"clean": {
			goMod: "module x\n\nrequire (\n\tgithub.com/eshu-hq/eshu-sdk/collector v0.1.0-rc.3\n\tgithub.com/eshu-hq/eshu-sdk/factschema v0.1.0-rc.5\n\tgopkg.in/yaml.v3 v3.0.1\n)\n",
		},
		"core in a block": {
			goMod: "module x\n\nrequire (\n\t" + coreModulePath + " v0.0.1\n)\n",
			want:  []string{coreModulePath},
		},
		"core subpath single line": {
			goMod: "module x\nrequire " + coreModulePath + "/internal v0.0.1\n",
			want:  []string{coreModulePath + "/internal"},
		},
		"core indirect with comment": {
			goMod: "module x\nrequire (\n\t" + coreModulePath + " v0.0.1 // indirect\n)\n",
			want:  []string{coreModulePath},
		},
		"similar prefix is not core": {
			goMod: "module x\nrequire github.com/eshu-hq/eshu/gopher v1.0.0\n",
		},
		"core only in a comment": {
			goMod: "module x\n// " + coreModulePath + " is forbidden\n",
		},
		"replace to core": {
			goMod: "module x\nreplace example.com/a => " + coreModulePath + " v0.0.1\n",
			want:  []string{coreModulePath, "replace directive"},
		},
		"replace to a local path": {
			goMod: "module x\nreplace example.com/a => ../a\n",
			want:  []string{"replace directive"},
		},
		"replace block": {
			goMod: "module x\nreplace (\n\texample.com/a => ../a\n)\n",
			want:  []string{"replace directive"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := forbiddenGoModEntries(test.goMod)
			if strings.Join(got, "|") != strings.Join(test.want, "|") {
				t.Fatalf("forbiddenGoModEntries() = %v, want %v", got, test.want)
			}
		})
	}
}

// forbiddenGoModEntries returns every module path in goMod that is the Eshu
// core module or lives under it, plus one "replace directive" entry per
// replace line, since a published-module-only dependency graph has no use for
// one. It scans each non-comment field, so a require in a block, a single-line
// require, an indirect require, and a replace target are all caught.
func forbiddenGoModEntries(goMod string) []string {
	var found []string
	for _, line := range strings.Split(goMod, "\n") {
		if before, _, ok := strings.Cut(line, "//"); ok {
			line = before
		}
		fields := strings.Fields(line)
		for _, field := range fields {
			if field == coreModulePath || strings.HasPrefix(field, coreModulePath+"/") {
				found = append(found, field)
			}
		}
		// "replace x => y" and the "replace (" block opener both start with it.
		if len(fields) > 0 && fields[0] == "replace" {
			found = append(found, "replace directive")
		}
	}
	return found
}

// modulePath reads the repository's own module path from go.mod so the
// self-import allowlist entry tracks renames automatically.
func modulePath(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("os.ReadFile(go.mod) error = %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			if strings.TrimSpace(path) == "" {
				break
			}
			return strings.TrimSpace(path)
		}
	}
	t.Fatal("go.mod declares no module path")
	return ""
}

func isStandardOrRelative(importPath string) bool {
	if strings.HasPrefix(importPath, ".") {
		return true
	}
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}
