package registry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"text/template"

	"gopkg.in/yaml.v3"
)

// PromptTemplate holds a versioned prompt definition loaded from a YAML file.
type PromptTemplate struct {
	Version      int    `yaml:"version"`
	Task         string `yaml:"task"`
	Model        string `yaml:"model"`
	System       string `yaml:"system"`
	UserTemplate string `yaml:"user_template"`
}

// renderData is the data struct passed into the user template.
type renderData struct {
	Input string
}

// Render executes the UserTemplate with the given input and returns the system
// prompt (unchanged) and the rendered user message.
func (pt *PromptTemplate) Render(input string) (system string, user string, err error) {
	tmpl, err := template.New("user").Parse(pt.UserTemplate)
	if err != nil {
		return "", "", fmt.Errorf("parsing user template for %s v%d: %w", pt.Task, pt.Version, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, renderData{Input: input}); err != nil {
		return "", "", fmt.Errorf("rendering user template for %s v%d: %w", pt.Task, pt.Version, err)
	}

	return pt.System, buf.String(), nil
}

// PromptMeta contains lightweight metadata about a registered template.
type PromptMeta struct {
	Task    string
	Version int
	Model   string
}

// Registry stores prompt templates indexed by task type and version.
type Registry struct {
	templates map[string]map[int]*PromptTemplate // task -> version -> template
	dir       string
}

// filenamePattern matches prompt YAML files named {task_type}_v{N}.yaml.
var filenamePattern = regexp.MustCompile(`^(.+)_v(\d+)\.yaml$`)

// NewRegistry scans dir for YAML files matching {task_type}_v{N}.yaml, parses
// each one, and returns a populated Registry.
func NewRegistry(dir string) (*Registry, error) {
	r := &Registry{
		templates: make(map[string]map[int]*PromptTemplate),
		dir:       dir,
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading prompt directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		matches := filenamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}

		taskType := matches[1]
		version, err := strconv.Atoi(matches[2])
		if err != nil {
			continue // should not happen given the regex, but be safe
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", entry.Name(), err)
		}

		var pt PromptTemplate
		if err := yaml.Unmarshal(data, &pt); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}

		// Validate that filename metadata matches file contents.
		if pt.Task != taskType {
			return nil, fmt.Errorf("%s: task field %q does not match filename task %q", entry.Name(), pt.Task, taskType)
		}
		if pt.Version != version {
			return nil, fmt.Errorf("%s: version field %d does not match filename version %d", entry.Name(), pt.Version, version)
		}

		if r.templates[taskType] == nil {
			r.templates[taskType] = make(map[int]*PromptTemplate)
		}
		r.templates[taskType][version] = &pt
	}

	return r, nil
}

// Get returns the prompt template for the given task type and version.
func (r *Registry) Get(taskType string, version int) (*PromptTemplate, error) {
	versions, ok := r.templates[taskType]
	if !ok {
		return nil, fmt.Errorf("no templates registered for task %q", taskType)
	}

	pt, ok := versions[version]
	if !ok {
		return nil, fmt.Errorf("version %d not found for task %q", version, taskType)
	}

	return pt, nil
}

// Latest returns the prompt template with the highest version number for the
// given task type.
func (r *Registry) Latest(taskType string) (*PromptTemplate, error) {
	versions, ok := r.templates[taskType]
	if !ok {
		return nil, fmt.Errorf("no templates registered for task %q", taskType)
	}

	maxVersion := 0
	for v := range versions {
		if v > maxVersion {
			maxVersion = v
		}
	}

	return versions[maxVersion], nil
}

// List returns metadata for every registered template, sorted by task then version.
func (r *Registry) List() []PromptMeta {
	var metas []PromptMeta

	for _, versions := range r.templates {
		for _, pt := range versions {
			metas = append(metas, PromptMeta{
				Task:    pt.Task,
				Version: pt.Version,
				Model:   pt.Model,
			})
		}
	}

	sort.Slice(metas, func(i, j int) bool {
		if metas[i].Task != metas[j].Task {
			return metas[i].Task < metas[j].Task
		}
		return metas[i].Version < metas[j].Version
	})

	return metas
}
