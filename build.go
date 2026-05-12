//go:build ignore

// build.go — cross-platform build script for twig.
//
// Usage:
//
//	go run build.go [target]
//
// Targets:
//
//	build    Build the twig binary (default)
//	install  Install twig to $GOPATH/bin
//	test     Run all tests
//	vet      Run go vet
//	index    Build then index the current directory
//	smoke    Build, index, and run verification commands
//	clean    Remove build artifacts
package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const tags = "sqlite_fts5"

func main() {
	target := "build"
	if len(os.Args) > 1 {
		target = os.Args[1]
	}

	ensureCC()

	switch target {
	case "build":
		doBuild()
	case "install":
		run("go", "install", "-tags", tags, "./cmd/twig/")
	case "test":
		run("go", "test", "-tags", tags, "-v", "./...")
	case "vet":
		run("go", "vet", "./...")
	case "index":
		doBuild()
		run(binary(), "index", ".")
	case "smoke":
		doSmoke()
	case "clean":
		doClean()
	default:
		fmt.Fprintf(os.Stderr, "unknown target: %s\nusage: go run build.go [build|install|test|vet|index|smoke|clean]\n", target)
		os.Exit(1)
	}
}

func binary() string {
	if runtime.GOOS == "windows" {
		return ".\\twig.exe"
	}
	return "./twig"
}

func doBuild() {
	run("go", "build", "-tags", tags, "-o", binary(), "./cmd/twig/")
}

func doSmoke() {
	run("go", "vet", "./...")
	doBuild()
	fmt.Println("\n--- smoke: index ---")
	run(binary(), "index", ".")
	fmt.Println("\n--- smoke: callers ---")
	run(binary(), "graph", "callers", "NewStore", "--depth", "1")
	fmt.Println("\n--- smoke: impact ---")
	run(binary(), "graph", "impact", "Store")
	fmt.Println("\n--- smoke: stats ---")
	run(binary(), "graph", "stats")
	fmt.Println("\n> all smoke commands completed successfully")
}

func doClean() {
	for _, f := range []string{"twig", "twig.exe", "twig.db", "twig.db-shm", "twig.db-wal"} {
		if err := os.Remove(f); err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "  warning: could not remove %s: %v\n", f, err)
			}
		} else {
			fmt.Printf("  removed %s\n", f)
		}
	}
}

// ensureCC sets CC=zig cc if CC is unset and zig is available.
func ensureCC() {
	if os.Getenv("CC") != "" {
		return
	}
	if _, err := exec.LookPath("zig"); err == nil {
		os.Setenv("CC", "zig cc")
		fmt.Println("> auto-detected zig, setting CC=\"zig cc\"")
	}
}

func run(name string, args ...string) {
	fmt.Printf("> %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nFAIL: %s %s: %v\n", name, strings.Join(args, " "), err)
		os.Exit(1)
	}
}
