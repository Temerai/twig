package parser

import (
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	typescript "github.com/smacker/go-tree-sitter/typescript/typescript"
)

// GrammarRegistry maps file extensions to tree-sitter grammars and language names.
type GrammarRegistry struct {
	languages map[string]*sitter.Language
	names     map[string]string
}

// NewGrammarRegistry creates a GrammarRegistry populated with all supported languages.
func NewGrammarRegistry() *GrammarRegistry {
	r := &GrammarRegistry{
		languages: make(map[string]*sitter.Language),
		names:     make(map[string]string),
	}

	r.register(".cs", csharp.GetLanguage(), "csharp")
	r.register(".java", java.GetLanguage(), "java")
	r.register(".go", golang.GetLanguage(), "go")
	r.register(".py", python.GetLanguage(), "python")
	r.register(".js", javascript.GetLanguage(), "javascript")
	r.register(".mjs", javascript.GetLanguage(), "javascript")
	r.register(".ts", typescript.GetLanguage(), "typescript")

	return r
}

func (r *GrammarRegistry) register(ext string, lang *sitter.Language, name string) {
	r.languages[ext] = lang
	r.names[ext] = name
}

// GetLanguage returns the tree-sitter language, its name, and whether the extension is supported.
func (r *GrammarRegistry) GetLanguage(ext string) (*sitter.Language, string, bool) {
	lang, ok := r.languages[ext]
	if !ok {
		return nil, "", false
	}
	return lang, r.names[ext], true
}

// SupportedExtensions returns all file extensions the registry can handle.
func (r *GrammarRegistry) SupportedExtensions() []string {
	exts := make([]string, 0, len(r.languages))
	for ext := range r.languages {
		exts = append(exts, ext)
	}
	return exts
}
