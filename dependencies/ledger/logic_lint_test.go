package ledger_test

// Spec §10.2 ("Logic layer is deterministic"): no time.Now(), no rand, no
// globals in logic/. The seam packages clock/ and idgen/ are the only
// places that legitimately reach for the wall clock and the OS CSPRNG;
// everything else in logic/ takes a Clock or IDGen as a parameter.
//
// The test walks logic/**/*.go (excluding _test.go) and asserts:
//   - no file imports math/rand
//   - no file imports crypto/rand outside logic/idgen
//   - no file calls time.Now() outside logic/clock
//
// AST-based, not regex — comments and string literals can mention "time.Now"
// without being a violation.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// allowed identifies the two seam files where the spec permits direct use
// of the wall clock and the OS CSPRNG. Paths are relative to the logic/
// root the walk starts from.
var (
	allowedTimeNowFiles  = map[string]bool{"clock/clock.go": true}
	allowedCryptoRandPkg = map[string]bool{"idgen/idgen.go": true}
)

func TestLint_LogicLayerDeterminism(t *testing.T) {
	t.Parallel()

	const logicRoot = "../../logic"
	fset := token.NewFileSet()

	err := filepath.WalkDir(logicRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(logicRoot, path)
		if err != nil {
			return err
		}

		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}

		// 1. Forbidden imports.
		for _, imp := range f.Imports {
			val := strings.Trim(imp.Path.Value, `"`)
			switch val {
			case "math/rand":
				t.Errorf("%s imports %q (spec §10.2: logic/ must be deterministic; use logic/idgen for randomness)", rel, val)
			case "crypto/rand":
				if !allowedCryptoRandPkg[rel] {
					t.Errorf("%s imports %q (spec §10.2: only logic/idgen may reach for the OS CSPRNG; logic/ depends on idgen.IDGen instead)", rel, val)
				}
			}
		}

		// 2. Forbidden calls: time.Now()
		// AST shape: *ast.CallExpr where Fun is a SelectorExpr whose X is an
		// Ident "time" and Sel is "Now". Comments and string literals don't
		// match because parser only puts identifier-shaped tokens into
		// SelectorExpr nodes.
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkg.Name == "time" && sel.Sel.Name == "Now" {
				if !allowedTimeNowFiles[rel] {
					pos := fset.Position(call.Pos())
					t.Errorf("%s:%d calls time.Now() (spec §10.2: logic/ must take a clock.Clock; only logic/clock may call time.Now)",
						rel, pos.Line)
				}
			}
			return true
		})

		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", logicRoot, err)
	}
}
