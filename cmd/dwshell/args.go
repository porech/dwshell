package main

import "strings"

// valueFlags names every flag that consumes a following token as its value.
//
// This is what tells a value apart from a positional: given `--account a@b
// list`, only this registry says that `a@b` belongs to `--account` and `list`
// is the command. A value flag missing from here would make dwshell take the
// value for a command name and quietly do the wrong thing, so a test walks the
// package's own source and holds this list to the flags actually registered.
var valueFlags = map[string]bool{
	"account": true,
	"c":       true,
	"config":  true,
	"term":    true,
	"timeout": true,
	"user":    true,
}

// partitionArgs splits arguments into flags (each with its value) and
// positionals, in their original relative order, so that flags may be written
// anywhere: `dwshell list --account a@b` and `dwshell --account a@b list` are
// the same command.
//
// Everything after a bare "--" is positional, which is how a path or an agent
// name that begins with a dash can still be passed.
func partitionArgs(args []string) (flags, positionals []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			return flags, positionals
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			// --flag=value carries its value; a bare value flag takes the next
			// token, which must not then be read as a positional.
			if !strings.Contains(a, "=") && valueFlags[trimDashes(a)] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return flags, positionals
}
