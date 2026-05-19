package main

import (
	"fmt"
	"path/filepath"

	"github.com/Temerai/twig/internal/graphagent"
	"github.com/Temerai/twig/internal/graphintel"
	"github.com/Temerai/twig/internal/logger"
	"github.com/Temerai/twig/internal/orchestrator"
	"github.com/Temerai/twig/internal/parser"
	"github.com/Temerai/twig/internal/registry"
)

// initComponents creates the full set of runtime components that most commands
// need: store, indexer, graph agent, graph intel, registry, logger, and
// orchestrator. The caller is responsible for closing the store and logger.
type components struct {
	store   *parser.Store
	indexer *parser.Indexer
	agent   *graphagent.GraphAgent
	intel   *graphintel.GraphIntel
	reg     *registry.Registry
	log     *logger.Logger
	orch    *orchestrator.Orchestrator
}

func initComponents() (*components, error) {
	store, err := parser.NewStore(parser.DBPathForRoot(cfg.CodebaseRoot))
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}

	indexer := parser.NewIndexer(store, cfg.CodebaseRoot)
	agent := graphagent.NewGraphAgent(store)
	intel := graphintel.NewGraphIntel(store)

	promptDir := filepath.Join(cfg.CodebaseRoot, "config", "prompts")
	reg, err := registry.NewRegistry(promptDir)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("loading prompt registry: %w", err)
	}

	log, err := logger.NewLogger(parser.LogPathForRoot(cfg.CodebaseRoot))
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("opening logger: %w", err)
	}

	orch := orchestrator.NewOrchestrator(agent, reg, log)

	return &components{
		store:   store,
		indexer: indexer,
		agent:   agent,
		intel:   intel,
		reg:     reg,
		log:     log,
		orch:    orch,
	}, nil
}

func (c *components) Close() {
	if c.log != nil {
		c.log.Close()
	}
	if c.store != nil {
		c.store.Close()
	}
}

type graphComponents struct {
	store *parser.Store
	intel *graphintel.GraphIntel
}

func initGraphComponents() (*graphComponents, error) {
	store, err := parser.NewStore(parser.DBPathForRoot(cfg.CodebaseRoot))
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	intel := graphintel.NewGraphIntel(store)
	return &graphComponents{store: store, intel: intel}, nil
}

func (gc *graphComponents) Close() {
	if gc.store != nil {
		gc.store.Close()
	}
}
