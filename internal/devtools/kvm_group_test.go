package devtools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeKVMGroups(t *testing.T, getent, id string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"getent": getent, "id": id} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestNSLKVMGroupMemberUsesSessionGroups(t *testing.T) {
	for _, tc := range []struct {
		name, getent, id string
		want             bool
		wantErr          string
	}{
		{"member", "echo 'kvm:x:36:'\n", "echo '1000 36'\n", true, ""},
		{"not a member despite readable device", "echo 'kvm:x:36:'\n", "echo '1000 37'\n", false, ""},
		{"missing group", "exit 2\n", "echo '1000'\n", false, "look up kvm group"},
		{"malformed group", "echo 'kvm:x::'\n", "echo '1000'\n", false, "invalid kvm group entry"},
		{"group read failed", "echo 'kvm:x:36:'\n", "exit 2\n", false, "read session groups"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeKVMGroups(t, tc.getent, tc.id)
			got, err := NSLKVMGroupMember()
			if got != tc.want || (tc.wantErr == "" && err != nil) || (tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr))) {
				t.Fatalf("membership = %v, %v; want %v, %q", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestNSLEnableRequestsAccessWithoutKVMGroup(t *testing.T) {
	if !NSLHostSupported() {
		t.Skip("nsl requires Linux amd64")
	}
	fakeKVMGroups(t, "echo 'kvm:x:36:'\n", "echo '1000 37'\n")
	previousAvailable, previousEnable := nslKVMAvailable, nslEnableKVMAccess
	t.Cleanup(func() { nslKVMAvailable, nslEnableKVMAccess = previousAvailable, previousEnable })
	nslKVMAvailable = func() (bool, bool) { return true, true }
	called := false
	nslEnableKVMAccess = func(context.Context) error { called = true; return nil }
	var stages []string
	err := SetWSL(context.Background(), BackendNSL, true, func(stage string) { stages = append(stages, stage) })
	if !called || !errors.Is(err, ErrNewLogin) || len(stages) != 1 || stages[0] != "Asking for virtual machine access…" {
		t.Fatalf("helper called = %v, stages = %q, error = %v", called, stages, err)
	}
	denied := errors.New("authorization denied")
	nslEnableKVMAccess = func(context.Context) error { return denied }
	if err := SetWSL(context.Background(), BackendNSL, true, nil); !errors.Is(err, denied) {
		t.Fatalf("denied authorization returned %v, want %v", err, denied)
	}
}
