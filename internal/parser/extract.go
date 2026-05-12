package parser

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/Temerai/twig/internal/types"
)

// Extractor parses source files using tree-sitter and extracts nodes and edges.
type Extractor struct {
	parser *sitter.Parser
}

// NewExtractor creates a new Extractor.
func NewExtractor() *Extractor {
	return &Extractor{
		parser: sitter.NewParser(),
	}
}

// Extract parses the given source code using the specified language and returns
// the discovered nodes (functions, classes, imports) and edges (CALLS, IMPORTS).
func (e *Extractor) Extract(source []byte, lang *sitter.Language, langName string, filePath string) ([]types.Node, []types.Edge) {
	e.parser.SetLanguage(lang)

	tree, err := e.parser.ParseCtx(context.Background(), nil, source)
	if err != nil || tree == nil {
		return nil, nil
	}
	defer tree.Close()

	root := tree.RootNode()

	var nodes []types.Node
	var edges []types.Edge

	// Walk the AST recursively.
	walkState := &walkContext{
		source:   source,
		filePath: filePath,
		langName: langName,
	}
	walkNode(root, walkState)

	nodes = append(nodes, walkState.nodes...)
	edges = append(edges, walkState.edges...)

	return nodes, edges
}

// walkContext accumulates extraction results during tree walking.
type walkContext struct {
	source   []byte
	filePath string
	langName string
	nodes    []types.Node
	edges    []types.Edge
	// stack tracks the enclosing class/struct and function for context.
	classStack    []string
	functionStack []string
}

// currentClass returns the innermost enclosing class/struct name, or "".
func (w *walkContext) currentClass() string {
	if len(w.classStack) > 0 {
		return w.classStack[len(w.classStack)-1]
	}
	return ""
}

// currentFunction returns the innermost enclosing function node ID, or "".
func (w *walkContext) currentFunction() string {
	if len(w.functionStack) > 0 {
		return w.functionStack[len(w.functionStack)-1]
	}
	return ""
}

// nodeID builds a graph node ID from file path and name.
func nodeID(filePath, name string) string {
	return fmt.Sprintf("%s:%s", filePath, name)
}

// firstLine returns the first line of text from the source range of a node.
func firstLine(source []byte, node *sitter.Node) string {
	text := node.Content(source)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return strings.TrimSpace(text[:idx])
	}
	return strings.TrimSpace(text)
}

// lineRange returns a "start-end" string for the node's line span (1-based).
func lineRange(node *sitter.Node) string {
	start := node.StartPoint().Row + 1
	end := node.EndPoint().Row + 1
	return fmt.Sprintf("%d-%d", start, end)
}

// findChildByFieldName finds a child node by its field name.
func findChildByFieldName(node *sitter.Node, name string) *sitter.Node {
	return node.ChildByFieldName(name)
}

// findChildByType returns the first direct child with the given type.
func findChildByType(node *sitter.Node, typeName string) *sitter.Node {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == typeName {
			return child
		}
	}
	return nil
}

// extractIdentifier tries to extract a name from a node by looking for
// identifier or name children, or the node's own content if it is an identifier.
func extractIdentifier(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	// Check by field name first.
	if n := findChildByFieldName(node, "name"); n != nil {
		return n.Content(source)
	}
	// Check common identifier child types.
	for _, t := range []string{"identifier", "name", "type_identifier", "property_identifier"} {
		if c := findChildByType(node, t); c != nil {
			return c.Content(source)
		}
	}
	// If the node itself is an identifier, return its content.
	if node.Type() == "identifier" || node.Type() == "name" || node.Type() == "type_identifier" {
		return node.Content(source)
	}
	return ""
}

// walkNode recursively processes a tree-sitter node, dispatching to
// language-specific extraction based on node type.
func walkNode(node *sitter.Node, ctx *walkContext) {
	if node == nil {
		return
	}
	nodeType := node.Type()

	switch ctx.langName {
	case "go":
		walkGo(node, nodeType, ctx)
	case "python":
		walkPython(node, nodeType, ctx)
	case "javascript":
		walkJavaScript(node, nodeType, ctx)
	case "typescript":
		walkTypeScript(node, nodeType, ctx)
	case "java":
		walkJava(node, nodeType, ctx)
	case "csharp":
		walkCSharp(node, nodeType, ctx)
	}
}

// --- Go ---

var goBuiltinTypes = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true, "uint": true, "uint8": true,
	"uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"any": true, "comparable": true,
}

// emitUsesEdgesFromFunc emits USES edges for type references in function parameters and return types.
func emitUsesEdgesFromFunc(node *sitter.Node, funcID string, ctx *walkContext) {
	// Walk all children looking for parameter lists and result types.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "parameter_list":
			for j := 0; j < int(child.ChildCount()); j++ {
				param := child.Child(j)
				switch param.Type() {
				case "parameter_declaration":
					// type is the last child (e.g., "x Foo" → Foo is last child)
					typeNode := param.Child(int(param.ChildCount()) - 1)
					if typeNode != nil {
						emitUsesForTypeNode(typeNode, funcID, ctx)
					}
				case "variadic_parameter_declaration":
					// variadic: "args ...Foo" — type is last child
					typeNode := param.Child(int(param.ChildCount()) - 1)
					if typeNode != nil {
						emitUsesForTypeNode(typeNode, funcID, ctx)
					}
				}
			}
		case "result":
			// Return types: can be a single type or a parameter_list
			if child.ChildCount() == 0 {
				continue
			}
			firstChild := child.Child(0)
			if firstChild.Type() == "parameter_list" {
				for j := 0; j < int(firstChild.ChildCount()); j++ {
					param := firstChild.Child(j)
					if param.Type() == "parameter_declaration" {
						typeNode := param.Child(int(param.ChildCount()) - 1)
						if typeNode != nil {
							emitUsesForTypeNode(typeNode, funcID, ctx)
						}
					}
				}
			} else {
				emitUsesForTypeNode(firstChild, funcID, ctx)
			}
		}
	}
}

// emitUsesEdgesFromStruct emits USES edges for type references in struct fields.
func emitUsesEdgesFromStruct(structTypeNode *sitter.Node, structID string, ctx *walkContext) {
	for i := 0; i < int(structTypeNode.ChildCount()); i++ {
		child := structTypeNode.Child(i)
		if child.Type() != "field_declaration_list" {
			continue
		}
		for j := 0; j < int(child.ChildCount()); j++ {
			field := child.Child(j)
			switch field.Type() {
			case "field_declaration":
				// Named field: last child is the type (e.g., "Name Foo" → Foo)
				typeNode := field.Child(int(field.ChildCount()) - 1)
				if typeNode != nil {
					emitUsesForTypeNode(typeNode, structID, ctx)
				}
			case "type_identifier":
				// Embedded/promoted field: "type Foo struct { Bar }" — Bar is directly here
				typeName := field.Content(ctx.source)
				if typeName != "" && !goBuiltinTypes[typeName] {
					ctx.edges = append(ctx.edges, types.Edge{
						Src:  structID,
						Dst:  typeName,
						Kind: "USES",
					})
				}
			}
		}
	}
}

// emitUsesForTypeNode extracts a type name from a type node and emits a USES edge.
func emitUsesForTypeNode(typeNode *sitter.Node, srcID string, ctx *walkContext) {
	if typeNode == nil {
		return
	}
	var typeName string
	switch typeNode.Type() {
	case "type_identifier":
		typeName = typeNode.Content(ctx.source)
	case "pointer_type":
		// *Foo — recurse into the base type
		if typeNode.ChildCount() > 0 {
			emitUsesForTypeNode(typeNode.Child(int(typeNode.ChildCount())-1), srcID, ctx)
		}
		return
	case "slice_type", "array_type", "map_type", "channel_type":
		// Walk all children to find type identifiers
		for i := 0; i < int(typeNode.ChildCount()); i++ {
			emitUsesForTypeNode(typeNode.Child(i), srcID, ctx)
		}
		return
	case "qualified_type":
		// pkg.Type — use the last child (the type name after the dot)
		if typeNode.ChildCount() > 0 {
			last := typeNode.Child(int(typeNode.ChildCount()) - 1)
			typeName = last.Content(ctx.source)
		}
	default:
		return
	}
	if typeName != "" && !goBuiltinTypes[typeName] {
		ctx.edges = append(ctx.edges, types.Edge{
			Src:  srcID,
			Dst:  typeName,
			Kind: "USES",
		})
	}
}

// --- Java USES helpers ---

// javaTypeName returns the user-defined type name from a Java type node,
// or "" for primitives or unrecognised nodes.
func javaTypeName(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	switch node.Type() {
	case "type_identifier":
		return node.Content(source)
	case "generic_type":
		for i := 0; i < int(node.ChildCount()); i++ {
			if node.Child(i).Type() == "type_identifier" {
				return node.Child(i).Content(source)
			}
		}
	case "array_type":
		if node.ChildCount() > 0 {
			return javaTypeName(node.Child(0), source)
		}
	case "scoped_type_identifier":
		for i := int(node.ChildCount()) - 1; i >= 0; i-- {
			c := node.Child(i)
			if c.Type() == "type_identifier" || c.Type() == "identifier" {
				return c.Content(source)
			}
		}
	// integral_type, floating_point_type, boolean_type, void_type → skip
	}
	return ""
}

// emitJavaUsesFromMethod emits USES edges for a Java method's return type and parameter types.
func emitJavaUsesFromMethod(node *sitter.Node, methodID string, ctx *walkContext) {
	if t := findChildByFieldName(node, "type"); t != nil {
		if name := javaTypeName(t, ctx.source); name != "" {
			ctx.edges = append(ctx.edges, types.Edge{Src: methodID, Dst: name, Kind: "USES"})
		}
	}
	params := findChildByType(node, "formal_parameters")
	if params == nil {
		return
	}
	for i := 0; i < int(params.ChildCount()); i++ {
		p := params.Child(i)
		if p.Type() == "formal_parameter" || p.Type() == "spread_parameter" {
			if t := findChildByFieldName(p, "type"); t != nil {
				if name := javaTypeName(t, ctx.source); name != "" {
					ctx.edges = append(ctx.edges, types.Edge{Src: methodID, Dst: name, Kind: "USES"})
				}
			}
		}
	}
}

// emitJavaUsesFromClassFields emits USES edges for field types in a Java class body.
func emitJavaUsesFromClassFields(classNode *sitter.Node, classID string, ctx *walkContext) {
	body := findChildByType(classNode, "class_body")
	if body == nil {
		return
	}
	for i := 0; i < int(body.ChildCount()); i++ {
		member := body.Child(i)
		if member.Type() == "field_declaration" {
			if t := findChildByFieldName(member, "type"); t != nil {
				if name := javaTypeName(t, ctx.source); name != "" {
					ctx.edges = append(ctx.edges, types.Edge{Src: classID, Dst: name, Kind: "USES"})
				}
			}
		}
	}
}

// --- C# USES helpers ---

// csTypeName returns the user-defined type name from a C# type node,
// or "" for primitives or unrecognised nodes.
func csTypeName(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	switch node.Type() {
	case "identifier":
		return node.Content(source)
	case "generic_name":
		for i := 0; i < int(node.ChildCount()); i++ {
			if node.Child(i).Type() == "identifier" {
				return node.Child(i).Content(source)
			}
		}
	case "nullable_type", "array_type":
		if node.ChildCount() > 0 {
			return csTypeName(node.Child(0), source)
		}
	case "qualified_name":
		for i := int(node.ChildCount()) - 1; i >= 0; i-- {
			if node.Child(i).Type() == "identifier" {
				return node.Child(i).Content(source)
			}
		}
	// predefined_type (void, int, string, bool...) → skip
	}
	return ""
}

// emitCSUsesFromMethod emits USES edges for a C# method's return type and parameter types.
func emitCSUsesFromMethod(node *sitter.Node, methodID string, ctx *walkContext) {
	retType := findChildByFieldName(node, "returns")
	if retType == nil {
		retType = findChildByFieldName(node, "type")
	}
	if retType != nil {
		if name := csTypeName(retType, ctx.source); name != "" {
			ctx.edges = append(ctx.edges, types.Edge{Src: methodID, Dst: name, Kind: "USES"})
		}
	}
	params := findChildByType(node, "parameter_list")
	if params == nil {
		return
	}
	for i := 0; i < int(params.ChildCount()); i++ {
		p := params.Child(i)
		if p.Type() == "parameter" {
			if t := findChildByFieldName(p, "type"); t != nil {
				if name := csTypeName(t, ctx.source); name != "" {
					ctx.edges = append(ctx.edges, types.Edge{Src: methodID, Dst: name, Kind: "USES"})
				}
			}
		}
	}
}

// emitCSUsesFromTypeFields emits USES edges for field types in a C# class/struct/interface body.
func emitCSUsesFromTypeFields(typeNode *sitter.Node, typeID string, ctx *walkContext) {
	declList := findChildByType(typeNode, "declaration_list")
	if declList == nil {
		return
	}
	for i := 0; i < int(declList.ChildCount()); i++ {
		member := declList.Child(i)
		if member.Type() == "field_declaration" {
			t := findChildByFieldName(member, "type")
			if t == nil {
				if varDecl := findChildByType(member, "variable_declaration"); varDecl != nil {
					t = findChildByFieldName(varDecl, "type")
				}
			}
			if t != nil {
				if name := csTypeName(t, ctx.source); name != "" {
					ctx.edges = append(ctx.edges, types.Edge{Src: typeID, Dst: name, Kind: "USES"})
				}
			}
		}
	}
}

func walkGo(node *sitter.Node, nodeType string, ctx *walkContext) {
	switch nodeType {
	case "function_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		id := nodeID(ctx.filePath, name)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "function",
			Name:      name,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		emitUsesEdgesFromFunc(node, id, ctx)
		ctx.functionStack = append(ctx.functionStack, id)
		walkChildren(node, ctx)
		ctx.functionStack = ctx.functionStack[:len(ctx.functionStack)-1]
		return

	case "method_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		// Try to get the receiver type for methods.
		receiver := ""
		if params := findChildByFieldName(node, "receiver"); params != nil {
			// Walk to find the type identifier.
			receiver = extractReceiverType(params, ctx.source)
		}
		qualName := name
		if receiver != "" {
			qualName = receiver + "." + name
		}
		id := nodeID(ctx.filePath, qualName)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "method",
			Name:      qualName,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		emitUsesEdgesFromFunc(node, id, ctx)
		ctx.functionStack = append(ctx.functionStack, id)
		walkChildren(node, ctx)
		ctx.functionStack = ctx.functionStack[:len(ctx.functionStack)-1]
		return

	case "type_declaration":
		// In Go, type_declaration contains type_spec children.
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "type_spec" {
				typeName := extractIdentifier(child, ctx.source)
				if typeName == "" {
					continue
				}
				kind := "struct"
				// Check if it's a struct or interface.
				if body := findChildByFieldName(child, "type"); body != nil {
					switch body.Type() {
					case "interface_type":
						kind = "interface"
					case "struct_type":
						kind = "struct"
					default:
						kind = "type"
					}
				}
				id := nodeID(ctx.filePath, typeName)
				ctx.nodes = append(ctx.nodes, types.Node{
					ID:        id,
					File:      ctx.filePath,
					Language:  ctx.langName,
					Kind:      kind,
					Name:      typeName,
					Signature: firstLine(ctx.source, child),
					Lines:     lineRange(child),
				})
				if body := findChildByFieldName(child, "type"); body != nil && body.Type() == "struct_type" {
					emitUsesEdgesFromStruct(body, id, ctx)
				}
				ctx.classStack = append(ctx.classStack, typeName)
				walkChildren(child, ctx)
				ctx.classStack = ctx.classStack[:len(ctx.classStack)-1]
			}
		}
		return

	case "import_declaration":
		// Walk import specs to get paths.
		walkImportDecl(node, ctx)
		return

	case "call_expression":
		calledName := extractCallName(node, ctx.source)
		if calledName != "" && ctx.currentFunction() != "" {
			ctx.edges = append(ctx.edges, types.Edge{
				Src:  ctx.currentFunction(),
				Dst:  calledName,
				Kind: "CALLS",
			})
		}
		walkChildren(node, ctx)
		return

	case "composite_literal":
		if ctx.currentFunction() != "" && node.ChildCount() > 0 {
			first := node.Child(0)
			if first.Type() == "type_identifier" || first.Type() == "qualified_type" {
				emitUsesForTypeNode(first, ctx.currentFunction(), ctx)
			}
		}
		walkChildren(node, ctx)
		return

	case "type_assertion_expression":
		if ctx.currentFunction() != "" {
			for i := 0; i < int(node.ChildCount()); i++ {
				child := node.Child(i)
				if child.Type() == "type_identifier" {
					emitUsesForTypeNode(child, ctx.currentFunction(), ctx)
				}
			}
		}
		walkChildren(node, ctx)
		return

	case "type_case":
		if ctx.currentFunction() != "" {
			for i := 0; i < int(node.ChildCount()); i++ {
				child := node.Child(i)
				if child.Type() == "type_identifier" {
					emitUsesForTypeNode(child, ctx.currentFunction(), ctx)
				}
			}
		}
		walkChildren(node, ctx)
		return
	}

	// Default: walk children.
	walkChildren(node, ctx)
}

func extractReceiverType(node *sitter.Node, source []byte) string {
	// Walk to find type_identifier or pointer_type -> type_identifier.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "type_identifier" {
			return child.Content(source)
		}
		if child.Type() == "pointer_type" {
			if ti := findChildByType(child, "type_identifier"); ti != nil {
				return ti.Content(source)
			}
		}
		if child.Type() == "parameter_declaration" {
			return extractReceiverType(child, source)
		}
	}
	return ""
}

func walkImportDecl(node *sitter.Node, ctx *walkContext) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "import_spec":
			path := findChildByFieldName(child, "path")
			if path != nil {
				importPath := strings.Trim(path.Content(ctx.source), `"`)
				ctx.edges = append(ctx.edges, types.Edge{
					Src:  ctx.filePath,
					Dst:  importPath,
					Kind: "IMPORTS",
				})
			}
		case "import_spec_list":
			walkImportDecl(child, ctx)
		case "interpreted_string_literal":
			importPath := strings.Trim(child.Content(ctx.source), `"`)
			ctx.edges = append(ctx.edges, types.Edge{
				Src:  ctx.filePath,
				Dst:  importPath,
				Kind: "IMPORTS",
			})
		}
	}
}

// --- Python ---

func walkPython(node *sitter.Node, nodeType string, ctx *walkContext) {
	switch nodeType {
	case "function_definition":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		kind := "function"
		qualName := name
		if cls := ctx.currentClass(); cls != "" {
			qualName = cls + "." + name
			kind = "method"
		}
		id := nodeID(ctx.filePath, qualName)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      kind,
			Name:      qualName,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		ctx.functionStack = append(ctx.functionStack, id)
		walkChildren(node, ctx)
		ctx.functionStack = ctx.functionStack[:len(ctx.functionStack)-1]
		return

	case "class_definition":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		id := nodeID(ctx.filePath, name)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "class",
			Name:      name,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		ctx.classStack = append(ctx.classStack, name)
		walkChildren(node, ctx)
		ctx.classStack = ctx.classStack[:len(ctx.classStack)-1]
		return

	case "import_statement", "import_from_statement":
		importText := extractImportText(node, ctx.source)
		if importText != "" {
			ctx.edges = append(ctx.edges, types.Edge{
				Src:  ctx.filePath,
				Dst:  importText,
				Kind: "IMPORTS",
			})
		}
		return

	case "call":
		calledName := extractCallName(node, ctx.source)
		if calledName != "" && ctx.currentFunction() != "" {
			ctx.edges = append(ctx.edges, types.Edge{
				Src:  ctx.currentFunction(),
				Dst:  calledName,
				Kind: "CALLS",
			})
		}
		walkChildren(node, ctx)
		return
	}

	walkChildren(node, ctx)
}

// --- JavaScript ---

func walkJavaScript(node *sitter.Node, nodeType string, ctx *walkContext) {
	switch nodeType {
	case "function_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		id := nodeID(ctx.filePath, name)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "function",
			Name:      name,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		ctx.functionStack = append(ctx.functionStack, id)
		walkChildren(node, ctx)
		ctx.functionStack = ctx.functionStack[:len(ctx.functionStack)-1]
		return

	case "method_definition":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		kind := "method"
		qualName := name
		if cls := ctx.currentClass(); cls != "" {
			qualName = cls + "." + name
		}
		id := nodeID(ctx.filePath, qualName)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      kind,
			Name:      qualName,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		ctx.functionStack = append(ctx.functionStack, id)
		walkChildren(node, ctx)
		ctx.functionStack = ctx.functionStack[:len(ctx.functionStack)-1]
		return

	case "lexical_declaration", "variable_declaration":
		// Check for arrow functions: const foo = () => { ... }
		handleArrowFunctionDecl(node, ctx)
		return

	case "class_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		id := nodeID(ctx.filePath, name)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "class",
			Name:      name,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		ctx.classStack = append(ctx.classStack, name)
		walkChildren(node, ctx)
		ctx.classStack = ctx.classStack[:len(ctx.classStack)-1]
		return

	case "import_statement":
		importText := extractImportSource(node, ctx.source)
		if importText != "" {
			ctx.edges = append(ctx.edges, types.Edge{
				Src:  ctx.filePath,
				Dst:  importText,
				Kind: "IMPORTS",
			})
		}
		return

	case "call_expression":
		calledName := extractCallName(node, ctx.source)
		if calledName != "" && ctx.currentFunction() != "" {
			ctx.edges = append(ctx.edges, types.Edge{
				Src:  ctx.currentFunction(),
				Dst:  calledName,
				Kind: "CALLS",
			})
		}
		walkChildren(node, ctx)
		return
	}

	walkChildren(node, ctx)
}

// --- TypeScript ---

func walkTypeScript(node *sitter.Node, nodeType string, ctx *walkContext) {
	switch nodeType {
	case "interface_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		id := nodeID(ctx.filePath, name)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "interface",
			Name:      name,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		ctx.classStack = append(ctx.classStack, name)
		walkChildren(node, ctx)
		ctx.classStack = ctx.classStack[:len(ctx.classStack)-1]
		return
	default:
		// TypeScript shares most node types with JavaScript.
		walkJavaScript(node, nodeType, ctx)
		return
	}
}

// --- Java ---

func walkJava(node *sitter.Node, nodeType string, ctx *walkContext) {
	switch nodeType {
	case "method_declaration", "constructor_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		kind := "method"
		if nodeType == "constructor_declaration" {
			kind = "constructor"
		}
		qualName := name
		if cls := ctx.currentClass(); cls != "" {
			qualName = cls + "." + name
		}
		id := nodeID(ctx.filePath, qualName)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      kind,
			Name:      qualName,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		emitJavaUsesFromMethod(node, id, ctx)
		ctx.functionStack = append(ctx.functionStack, id)
		walkChildren(node, ctx)
		ctx.functionStack = ctx.functionStack[:len(ctx.functionStack)-1]
		return

	case "class_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		id := nodeID(ctx.filePath, name)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "class",
			Name:      name,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		emitJavaUsesFromClassFields(node, id, ctx)
		ctx.classStack = append(ctx.classStack, name)
		walkChildren(node, ctx)
		ctx.classStack = ctx.classStack[:len(ctx.classStack)-1]
		return

	case "interface_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		id := nodeID(ctx.filePath, name)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "interface",
			Name:      name,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		ctx.classStack = append(ctx.classStack, name)
		walkChildren(node, ctx)
		ctx.classStack = ctx.classStack[:len(ctx.classStack)-1]
		return

	case "import_declaration":
		// Java: import com.example.Foo;
		importText := extractJavaImport(node, ctx.source)
		if importText != "" {
			ctx.edges = append(ctx.edges, types.Edge{
				Src:  ctx.filePath,
				Dst:  importText,
				Kind: "IMPORTS",
			})
		}
		return

	case "method_invocation":
		calledName := extractCallName(node, ctx.source)
		if calledName != "" && ctx.currentFunction() != "" {
			ctx.edges = append(ctx.edges, types.Edge{
				Src:  ctx.currentFunction(),
				Dst:  calledName,
				Kind: "CALLS",
			})
		}
		walkChildren(node, ctx)
		return
	}

	walkChildren(node, ctx)
}

// --- C# ---

func walkCSharp(node *sitter.Node, nodeType string, ctx *walkContext) {
	switch nodeType {
	case "method_declaration", "constructor_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		kind := "method"
		if nodeType == "constructor_declaration" {
			kind = "constructor"
		}
		qualName := name
		if cls := ctx.currentClass(); cls != "" {
			qualName = cls + "." + name
		}
		id := nodeID(ctx.filePath, qualName)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      kind,
			Name:      qualName,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		emitCSUsesFromMethod(node, id, ctx)
		ctx.functionStack = append(ctx.functionStack, id)
		walkChildren(node, ctx)
		ctx.functionStack = ctx.functionStack[:len(ctx.functionStack)-1]
		return

	case "class_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		id := nodeID(ctx.filePath, name)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "class",
			Name:      name,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		emitCSUsesFromTypeFields(node, id, ctx)
		ctx.classStack = append(ctx.classStack, name)
		walkChildren(node, ctx)
		ctx.classStack = ctx.classStack[:len(ctx.classStack)-1]
		return

	case "interface_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		id := nodeID(ctx.filePath, name)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "interface",
			Name:      name,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		emitCSUsesFromTypeFields(node, id, ctx)
		ctx.classStack = append(ctx.classStack, name)
		walkChildren(node, ctx)
		ctx.classStack = ctx.classStack[:len(ctx.classStack)-1]
		return

	case "struct_declaration":
		name := extractIdentifier(node, ctx.source)
		if name == "" {
			break
		}
		id := nodeID(ctx.filePath, name)
		ctx.nodes = append(ctx.nodes, types.Node{
			ID:        id,
			File:      ctx.filePath,
			Language:  ctx.langName,
			Kind:      "struct",
			Name:      name,
			Signature: firstLine(ctx.source, node),
			Lines:     lineRange(node),
		})
		emitCSUsesFromTypeFields(node, id, ctx)
		ctx.classStack = append(ctx.classStack, name)
		walkChildren(node, ctx)
		ctx.classStack = ctx.classStack[:len(ctx.classStack)-1]
		return

	case "using_directive":
		importText := extractUsingDirective(node, ctx.source)
		if importText != "" {
			ctx.edges = append(ctx.edges, types.Edge{
				Src:  ctx.filePath,
				Dst:  importText,
				Kind: "IMPORTS",
			})
		}
		return

	case "invocation_expression":
		calledName := extractCallName(node, ctx.source)
		if calledName != "" && ctx.currentFunction() != "" {
			ctx.edges = append(ctx.edges, types.Edge{
				Src:  ctx.currentFunction(),
				Dst:  calledName,
				Kind: "CALLS",
			})
		}
		walkChildren(node, ctx)
		return
	}

	walkChildren(node, ctx)
}

// --- Helper functions ---

// walkChildren iterates over all children of a node and walks each one.
func walkChildren(node *sitter.Node, ctx *walkContext) {
	for i := 0; i < int(node.ChildCount()); i++ {
		walkNode(node.Child(i), ctx)
	}
}

// handleArrowFunctionDecl checks for arrow function assignments:
// const foo = (...) => { ... }
func handleArrowFunctionDecl(node *sitter.Node, ctx *walkContext) {
	found := false
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			nameNode := findChildByFieldName(child, "name")
			valueNode := findChildByFieldName(child, "value")
			if nameNode != nil && valueNode != nil && valueNode.Type() == "arrow_function" {
				name := nameNode.Content(ctx.source)
				if name == "" {
					continue
				}
				found = true
				id := nodeID(ctx.filePath, name)
				ctx.nodes = append(ctx.nodes, types.Node{
					ID:        id,
					File:      ctx.filePath,
					Language:  ctx.langName,
					Kind:      "function",
					Name:      name,
					Signature: firstLine(ctx.source, child),
					Lines:     lineRange(child),
				})
				ctx.functionStack = append(ctx.functionStack, id)
				walkChildren(valueNode, ctx)
				ctx.functionStack = ctx.functionStack[:len(ctx.functionStack)-1]
			}
		}
	}
	if !found {
		walkChildren(node, ctx)
	}
}

// extractCallName extracts the called function/method name from a call expression.
func extractCallName(node *sitter.Node, source []byte) string {
	// Try the "function" field first (Go, JS, TS call_expression).
	fn := findChildByFieldName(node, "function")
	if fn != nil {
		return extractCallTarget(fn, source)
	}
	// Try the "name" field (Java method_invocation).
	nameNode := findChildByFieldName(node, "name")
	if nameNode != nil {
		return nameNode.Content(source)
	}
	// Fallback: first child.
	if node.ChildCount() > 0 {
		return extractCallTarget(node.Child(0), source)
	}
	return ""
}

// extractCallTarget gets the function name from a call target node,
// handling member expressions like obj.method().
func extractCallTarget(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	switch node.Type() {
	case "identifier", "name":
		return node.Content(source)
	case "member_expression", "member_access_expression":
		object := findChildByFieldName(node, "object")
		prop := findChildByFieldName(node, "property")
		if prop == nil {
			prop = findChildByFieldName(node, "name")
		}
		if object != nil && prop != nil && object.Type() == "identifier" {
			return object.Content(source) + "." + prop.Content(source)
		}
		if prop != nil {
			return prop.Content(source)
		}
		return node.Content(source)
	case "selector_expression":
		operand := findChildByFieldName(node, "operand")
		field := findChildByFieldName(node, "field")
		if operand != nil && field != nil && operand.Type() == "identifier" {
			return operand.Content(source) + "." + field.Content(source)
		}
		if field != nil {
			return field.Content(source)
		}
		return node.Content(source)
	case "scoped_identifier":
		// Java/C# scoped names.
		nameNode := findChildByFieldName(node, "name")
		if nameNode != nil {
			return nameNode.Content(source)
		}
		return node.Content(source)
	default:
		// For complex expressions, try identifier child.
		if id := findChildByType(node, "identifier"); id != nil {
			return id.Content(source)
		}
		return node.Content(source)
	}
}

// extractImportText extracts the module name from a Python import statement.
func extractImportText(node *sitter.Node, source []byte) string {
	// import foo / from foo import bar
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "dotted_name" {
			return child.Content(source)
		}
	}
	// Fallback: strip the keyword.
	text := node.Content(source)
	text = strings.TrimPrefix(text, "from ")
	text = strings.TrimPrefix(text, "import ")
	if idx := strings.Index(text, " "); idx > 0 {
		return text[:idx]
	}
	return strings.TrimSpace(text)
}

// extractImportSource extracts the module path from a JS/TS import statement.
func extractImportSource(node *sitter.Node, source []byte) string {
	s := findChildByFieldName(node, "source")
	if s != nil {
		return strings.Trim(s.Content(source), `"'`)
	}
	// Fallback: find a string node.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "string" {
			return strings.Trim(child.Content(source), `"'`)
		}
	}
	return ""
}

// extractJavaImport extracts the package path from a Java import declaration.
func extractJavaImport(node *sitter.Node, source []byte) string {
	// Walk to find scoped_identifier or identifier.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "scoped_identifier" || child.Type() == "identifier" {
			return child.Content(source)
		}
	}
	text := strings.TrimPrefix(strings.TrimSpace(node.Content(source)), "import ")
	text = strings.TrimSuffix(text, ";")
	return strings.TrimSpace(text)
}

// extractUsingDirective extracts the namespace from a C# using directive.
func extractUsingDirective(node *sitter.Node, source []byte) string {
	// Look for a qualified_name, identifier, or name child.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "qualified_name", "identifier", "name":
			return child.Content(source)
		}
	}
	text := strings.TrimPrefix(strings.TrimSpace(node.Content(source)), "using ")
	text = strings.TrimSuffix(text, ";")
	return strings.TrimSpace(text)
}
