package winmenu

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A function that takes ...uintptr and is handed uintptr(unsafe.Pointer(&local)) must say
// //go:uintptrescapes, or the local is not kept alive and in place for the call (see
// stack_windows_test.go). windows.LazyProc.Call and syscall.SyscallN already say so; this makes sure
// the wrappers in this package do too, including ones added later.
func TestUintptrWrappersKeepTheirArgumentsInPlace(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !takesVariadicUintptr(fn) {
				continue
			}
			checked++
			marked := false
			if fn.Doc != nil {
				for _, c := range fn.Doc.List {
					if strings.TrimSpace(c.Text) == "//go:uintptrescapes" {
						marked = true
					}
				}
			}
			if !marked {
				t.Errorf("%s: func %s takes ...uintptr but is not marked //go:uintptrescapes", name, fn.Name.Name)
			}
		}
	}
	if _, err := os.Stat("text_windows.go"); err == nil && checked == 0 {
		t.Error("found no ...uintptr wrapper to check: has comObject.call been renamed?")
	}
}

func takesVariadicUintptr(fn *ast.FuncDecl) bool {
	for _, p := range fn.Type.Params.List {
		if e, ok := p.Type.(*ast.Ellipsis); ok {
			if id, ok := e.Elt.(*ast.Ident); ok && id.Name == "uintptr" {
				return true
			}
		}
	}
	return false
}
