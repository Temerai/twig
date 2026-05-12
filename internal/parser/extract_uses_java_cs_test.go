package parser

import (
	"testing"

	"github.com/Temerai/twig/internal/types"
)

// extractEdges is a test helper that runs Extract and returns only edges.
func extractEdges(t *testing.T, source []byte, ext string, filePath string) []types.Edge {
	t.Helper()
	ext2 := NewExtractor()
	reg := NewGrammarRegistry()
	lang, langName, ok := reg.GetLanguage(ext)
	if !ok {
		t.Fatalf("language not registered for extension %q", ext)
	}
	_, edges := ext2.Extract(source, lang, langName, filePath)
	return edges
}

// assertUSES checks that a USES edge with the given src and dst exists.
func assertUSES(t *testing.T, edges []types.Edge, src, dst string) {
	t.Helper()
	for _, e := range edges {
		if e.Kind == "USES" && e.Src == src && e.Dst == dst {
			return
		}
	}
	t.Errorf("expected USES edge %s → %s; got edges: %v", src, dst, edges)
}

// --- Java ---

func TestJavaMethodParameterUSES(t *testing.T) {
	source := []byte(`
class Foo {}
class Service {
    public void handle(Foo f) {}
}
`)
	edges := extractEdges(t, source, ".java", "test.java")
	assertUSES(t, edges, "test.java:Service.handle", "Foo")
}

func TestJavaReturnTypeUSES(t *testing.T) {
	source := []byte(`
class Foo {}
class Factory {
    public Foo create() { return new Foo(); }
}
`)
	edges := extractEdges(t, source, ".java", "test.java")
	assertUSES(t, edges, "test.java:Factory.create", "Foo")
}

func TestJavaFieldTypeUSES(t *testing.T) {
	source := []byte(`
class Foo {}
class Container {
    private Foo foo;
}
`)
	edges := extractEdges(t, source, ".java", "test.java")
	assertUSES(t, edges, "test.java:Container", "Foo")
}

func TestJavaPrimitiveNoUSES(t *testing.T) {
	source := []byte(`
class MathOps {
    public int add(int a, int b) { return a + b; }
}
`)
	edges := extractEdges(t, source, ".java", "test.java")
	for _, e := range edges {
		if e.Kind == "USES" {
			t.Errorf("expected no USES edges for primitive types, got %+v", e)
		}
	}
}

// --- C# ---

func TestCSMethodParameterUSES(t *testing.T) {
	source := []byte(`
class Foo {}
class Service {
    public void Handle(Foo f) {}
}
`)
	edges := extractEdges(t, source, ".cs", "test.cs")
	assertUSES(t, edges, "test.cs:Service.Handle", "Foo")
}

func TestCSReturnTypeUSES(t *testing.T) {
	source := []byte(`
class Foo {}
class Factory {
    public Foo Create() { return new Foo(); }
}
`)
	edges := extractEdges(t, source, ".cs", "test.cs")
	assertUSES(t, edges, "test.cs:Factory.Create", "Foo")
}

func TestCSFieldTypeUSES(t *testing.T) {
	source := []byte(`
class Foo {}
class Container {
    private Foo _foo;
}
`)
	edges := extractEdges(t, source, ".cs", "test.cs")
	assertUSES(t, edges, "test.cs:Container", "Foo")
}

func TestCSPredefinedTypeNoUSES(t *testing.T) {
	source := []byte(`
class MathOps {
    public int Add(int a, int b) { return a + b; }
}
`)
	edges := extractEdges(t, source, ".cs", "test.cs")
	for _, e := range edges {
		if e.Kind == "USES" {
			t.Errorf("expected no USES edges for predefined types, got %+v", e)
		}
	}
}
