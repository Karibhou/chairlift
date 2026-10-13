package aistack

import (
	"context"
	"encoding/json"
	"errors"
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

	setCall := "llmman config set aliases.bluefin-active hf.co/unsloth/Qwen2.5-7B-Instruct-GGUF:Q4_K_M"
	restartCall := "systemctl --user restart " + ServiceName
	setAt, restartAt := slices.Index(h.calls, setCall), slices.Index(h.calls, restartCall)
	if setAt < 0 {
		t.Fatalf("expected call %q in %v", setCall, h.calls)
	}
	if restartAt < setAt {
		t.Errorf("restart did not follow the alias write: %v", h.calls)
	}
}

func TestConfigureActiveModelSetFailureSkipsRestart(t *testing.T) {
	h := newHost(t)
	unit, _ := RenderUnit(h.exe)
	path, _ := UnitPath()
	if err := writeAtomic(path, unit); err != nil {
		t.Fatal(err)
	}
	ref := "hf.co/unsloth/Qwen2.5-7B-Instruct-GGUF:Q4_K_M"
	h.fail["llmman config set aliases.bluefin-active "+ref] = errors.New("config locked")

	if err := ConfigureActiveModel(context.Background(), ref); err == nil {
		t.Fatal("ConfigureActiveModel succeeded with a failed alias write")
	}
	if slices.Contains(h.calls, "systemctl --user restart "+ServiceName) {
		t.Errorf("restarted after a failed alias write: %v", h.calls)
	}
}

func TestSameModelRef(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"unsloth/Qwen:Q4", "unsloth/Qwen:Q4", true},
		{"unsloth/Qwen:Q4", "hf.co/unsloth/Qwen:Q4", true},
		{"hf.co/unsloth/Qwen:Q4", "unsloth/Qwen:Q4", true},
		{"hf.co/unsloth/Qwen:Q4", "hf.co/unsloth/Qwen:Q4", true},
		{"unsloth/Qwen:Q4", "unsloth/Qwen:Q8", false},
		{"unsloth/Qwen:Q4", "", false},
		{"", "", false},
	} {
		if got := SameModelRef(tc.a, tc.b); got != tc.want {
			t.Errorf("SameModelRef(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
