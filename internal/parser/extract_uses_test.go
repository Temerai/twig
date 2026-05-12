package parser

import (
	"testing"
)

func TestExtractUSESEdges(t *testing.T) {
	source := []byte(`package main

type Foo struct {
	Name string
}

type Bar struct {
	F Foo
}

func doSomething(f Foo) Bar {
	return Bar{F: f}
}

func returnPointer() *Foo {
	return &Foo{}
}
`)

	ext := NewExtractor()
	reg := NewGrammarRegistry()
	lang, langName, ok := reg.GetLanguage(".go")
	if !ok {
		t.Fatal("Go language not registered")
	}

	nodes, edges := ext.Extract(source, lang, langName, "test.go")

	// Check nodes were created
	nodeNames := make(map[string]bool)
	for _, n := range nodes {
		nodeNames[n.Name] = true
	}
	for _, expected := range []string{"Foo", "Bar", "doSomething", "returnPointer"} {
		if !nodeNames[expected] {
			t.Errorf("expected node %q not found", expected)
		}
	}

	// Check USES edges
	type edgeKey struct {
		src, dst, kind string
	}
	edgeSet := make(map[edgeKey]bool)
	for _, e := range edges {
		edgeSet[edgeKey{e.Src, e.Dst, e.Kind}] = true
	}

	// Bar struct should USES Foo (field type)
	if !edgeSet[edgeKey{"test.go:Bar", "Foo", "USES"}] {
		t.Error("expected USES edge from Bar to Foo (struct field)")
	}

	// doSomething should USES Foo (param) and Bar (return)
	if !edgeSet[edgeKey{"test.go:doSomething", "Foo", "USES"}] {
		t.Error("expected USES edge from doSomething to Foo (parameter)")
	}
	if !edgeSet[edgeKey{"test.go:doSomething", "Bar", "USES"}] {
		t.Error("expected USES edge from doSomething to Bar (return type)")
	}

	// returnPointer should USES Foo (return pointer type)
	if !edgeSet[edgeKey{"test.go:returnPointer", "Foo", "USES"}] {
		t.Error("expected USES edge from returnPointer to Foo (pointer return)")
	}

	// No USES edges for builtin types (string)
	for k := range edgeSet {
		if k.kind == "USES" && k.dst == "string" {
			t.Error("should not have USES edge for builtin type 'string'")
		}
	}

	// Print all edges for debugging
	t.Logf("Total edges: %d", len(edges))
	for _, e := range edges {
		t.Logf("  %s -> %s [%s]", e.Src, e.Dst, e.Kind)
	}
}

func TestVariadicParameterUSES(t *testing.T) {
	source := []byte(`package main

type Foo struct{}

func handle(args ...Foo) {}
`)
	ext := NewExtractor()
	reg := NewGrammarRegistry()
	lang, langName, ok := reg.GetLanguage(".go")
	if !ok {
		t.Fatal("Go language not registered")
	}

	_, edges := ext.Extract(source, lang, langName, "test.go")

	type edgeKey struct{ src, dst, kind string }
	edgeSet := make(map[edgeKey]bool)
	for _, e := range edges {
		edgeSet[edgeKey{e.Src, e.Dst, e.Kind}] = true
	}

	if !edgeSet[edgeKey{"test.go:handle", "Foo", "USES"}] {
		t.Error("expected USES edge from handle to Foo (variadic parameter)")
	}
}

func TestEmbeddedStructFieldUSES(t *testing.T) {
	source := []byte(`package main

type Foo struct{}

type Bar struct {
	Foo
}
`)
	ext := NewExtractor()
	reg := NewGrammarRegistry()
	lang, langName, ok := reg.GetLanguage(".go")
	if !ok {
		t.Fatal("Go language not registered")
	}

	_, edges := ext.Extract(source, lang, langName, "test.go")

	type edgeKey struct{ src, dst, kind string }
	edgeSet := make(map[edgeKey]bool)
	for _, e := range edges {
		edgeSet[edgeKey{e.Src, e.Dst, e.Kind}] = true
	}

	if !edgeSet[edgeKey{"test.go:Bar", "Foo", "USES"}] {
		t.Error("expected USES edge from Bar to Foo (embedded struct field)")
	}
}

func TestCompositeLiteralUSES(t *testing.T) {
	source := []byte(`package main

type Foo struct{ X int }

func build() {
	_ = Foo{X: 1}
}
`)
	ext := NewExtractor()
	reg := NewGrammarRegistry()
	lang, langName, ok := reg.GetLanguage(".go")
	if !ok {
		t.Fatal("Go language not registered")
	}

	_, edges := ext.Extract(source, lang, langName, "test.go")

	type edgeKey struct{ src, dst, kind string }
	edgeSet := make(map[edgeKey]bool)
	for _, e := range edges {
		edgeSet[edgeKey{e.Src, e.Dst, e.Kind}] = true
	}

	if !edgeSet[edgeKey{"test.go:build", "Foo", "USES"}] {
		t.Error("expected USES edge from build to Foo (composite literal)")
	}
}
