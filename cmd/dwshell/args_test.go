package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPartitionArgsSeparatesFlagsFromPositionals(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		flags []string
		pos   []string
	}{
		{"flags first", []string{"--account", "a@b", "list"}, []string{"--account", "a@b"}, []string{"list"}},
		{"flags last", []string{"list", "--account", "a@b"}, []string{"--account", "a@b"}, []string{"list"}},
		{"flags around", []string{"-c", "ls", "GHE", "--term", "xterm"},
			[]string{"-c", "ls", "--term", "xterm"}, []string{"GHE"}},
		{"equals form needs no lookahead", []string{"--account=a@b", "list"}, []string{"--account=a@b"}, []string{"list"}},
		{"boolean flags consume nothing", []string{"list", "--json"}, []string{"--json"}, []string{"list"}},
		{"several positionals keep their order", []string{"get", "-r", "a:b", "c"},
			[]string{"-r"}, []string{"get", "a:b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags, pos := partitionArgs(tc.args)
			if !reflect.DeepEqual(flags, tc.flags) {
				t.Errorf("flags = %v, want %v", flags, tc.flags)
			}
			if !reflect.DeepEqual(pos, tc.pos) {
				t.Errorf("positionals = %v, want %v", pos, tc.pos)
			}
		})
	}
}

// Everything after "--" is positional, so a path or command that looks like a
// flag can still be passed.
func TestPartitionArgsStopsAtADoubleDash(t *testing.T) {
	flags, pos := partitionArgs([]string{"rm", "--yes", "--", "-weird-name"})
	if !reflect.DeepEqual(flags, []string{"--yes"}) {
		t.Errorf("flags = %v", flags)
	}
	if !reflect.DeepEqual(pos, []string{"rm", "-weird-name"}) {
		t.Errorf("positionals = %v", pos)
	}
}

// valueFlags decides where a positional begins, so a flag that takes a value
// and is missing from it makes dwshell mistake that value for a command name —
// silently doing the wrong thing rather than failing. This walks the package's
// own source and holds the registry to what is actually registered.
func TestValueFlagsMatchesTheFlagsActuallyRegistered(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{} // flag name -> the FlagSet method that registers it
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			method := sel.Sel.Name
			// Var methods take (ptr, name, default, usage); Bool ones consume no
			// following token, every other kind does.
			if len(method) < 4 || method[len(method)-3:] != "Var" {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			name := lit.Value[1 : len(lit.Value)-1]
			if method == "BoolVar" {
				return true
			}
			found[name] = method
			return true
		})
	}
	if len(found) == 0 {
		t.Fatal("no flags found: the walk is not seeing the source")
	}
	for name, method := range found {
		if !valueFlags[name] {
			t.Errorf("--%s takes a value (%s) but is missing from valueFlags: "+
				"a positional after it would be mistaken for the flag's value", name, method)
		}
	}
	for name := range valueFlags {
		if _, ok := found[name]; !ok {
			t.Errorf("valueFlags lists --%s, which no command registers as a value flag", name)
		}
	}
}
