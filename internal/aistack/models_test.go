package aistack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestNodeStatusModels(t *testing.T) {
	status := NodeStatus{
		Stored: map[string]json.RawMessage{
			"hf.co/unsloth/Ministral-3-8B-Instruct-2512-GGUF:Q4_K_M": nil,
			"hf.co/ornith-ai/Ornith-1.5-9B-GGUF:BF16":                nil,
		},
	}
	got := status.Models()
	want := []string{
		"hf.co/ornith-ai/Ornith-1.5-9B-GGUF:BF16",
		"hf.co/unsloth/Ministral-3-8B-Instruct-2512-GGUF:Q4_K_M",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Models() = %v, want %v", got, want)
	}
}

func TestFetchNodeStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"memory":17179869184,"loaded":{},"stored":{"unsloth/Qwen2.5-7B-Instruct-GGUF:Q4_K_M":{}}}`))
	}))
	defer srv.Close()

	origNodeURL := nodeURL
	nodeURL = srv.URL
	defer func() { nodeURL = origNodeURL }()

	status, err := FetchNodeStatus(context.Background())
	if err != nil {
		t.Fatalf("FetchNodeStatus error: %v", err)
	}
	if len(status.Stored) != 1 {
		t.Errorf("Stored length = %d, want 1", len(status.Stored))
	}
}

func TestConfigureActiveModel(t *testing.T) {
	h := newHost(t)
	unit, _ := RenderUnit(h.exe)
	path, _ := UnitPath()
	if err := writeAtomic(path, unit); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"memory":17179869184,"loaded":{},"stored":{"hf.co/unsloth/Qwen2.5-7B-Instruct-GGUF:Q4_K_M":{}}}`))
	}))
	defer srv.Close()
	origNodeURL := nodeURL
	nodeURL = srv.URL
	defer func() { nodeURL = origNodeURL }()

	err := ConfigureActiveModel(context.Background(), "hf.co/unsloth/Qwen2.5-7B-Instruct-GGUF:Q4_K_M")
	if err != nil {
		t.Fatalf("ConfigureActiveModel failed: %v", err)
	}

	wantCall := "llmman config set aliases.bluefin-active hf.co/unsloth/Qwen2.5-7B-Instruct-GGUF:Q4_K_M"
	var foundConfig bool
	for _, call := range h.calls {
		if call == wantCall {
			foundConfig = true
			break
		}
	}
	if !foundConfig {
		t.Errorf("expected call %q in %v", wantCall, h.calls)
	}
}
