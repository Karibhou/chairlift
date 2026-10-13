package aistack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/projectbluefin/chairlift/internal/dryrun"
)

// NodeStatus is the parsed response from /llmman/node.
type NodeStatus struct {
	Stored map[string]json.RawMessage `json:"stored"`
}

// Models returns the downloaded model references in sorted order.
func (n NodeStatus) Models() []string {
	return slices.Sorted(maps.Keys(n.Stored))
}

// SameModelRef reports whether two model references name the same model,
// treating the hf.co/ registry prefix as optional: llmman may store a model
// with or without it while the alias carries the other form.
func SameModelRef(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.TrimPrefix(a, hfPrefix) == strings.TrimPrefix(b, hfPrefix)
}

const hfPrefix = "hf.co/"

// FetchNodeStatus queries /llmman/node for available models and memory.
func FetchNodeStatus(ctx context.Context) (NodeStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nodeURL, nil)
	if err != nil {
		return NodeStatus{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return NodeStatus{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return NodeStatus{}, fmt.Errorf("HTTP %s from node endpoint", resp.Status)
	}
	var node NodeStatus
	if err := json.NewDecoder(resp.Body).Decode(&node); err != nil {
		return NodeStatus{}, err
	}
	return node, nil
}

// ConfigureActiveModel maps the bluefin-active alias to modelRef and restarts
// the local model daemon so it serves the new selection.
func ConfigureActiveModel(ctx context.Context, modelRef string) error {
	if dryrun.Enabled() {
		log.Printf("[DRY-RUN] would set llmman alias %s to %s and restart %s", ActiveModelAlias, modelRef, ServiceName)
		return nil
	}
	exe := Executable()
	if exe == "" {
		return errors.New("llmman executable not found")
	}
	if _, err := updateServiceUnit(ctx); err != nil {
		return fmt.Errorf("updating the local-only model service: %w", err)
	}
	_, err := run(ctx, exe, "config", "set", "aliases."+ActiveModelAlias, modelRef)
	if err != nil {
		return fmt.Errorf("llmman config set alias: %w", err)
	}
	if err := restartService(ctx); err != nil {
		return fmt.Errorf("restarting the model server for the selected alias: %w", err)
	}
	if !WaitHealthy(ctx, time.Minute) {
		return errors.New("the model server did not become ready after selecting the model")
	}
	return nil
}
