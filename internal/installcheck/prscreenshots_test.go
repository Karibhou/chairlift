package installcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// prScreenshotsWorkflow is the subset of pr-screenshots.yml this gate reads.
type prScreenshotsWorkflow struct {
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		If          string            `yaml:"if"`
		Needs       any               `yaml:"needs"`
		Permissions map[string]string `yaml:"permissions"`
		Steps       []struct {
			Name string            `yaml:"name"`
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// TestPRScreenshotsNeverRunsPullRequestCodeBesideAWriteToken holds the
// pull request screenshot workflow to its security shape. It runs on a
// comment, so anyone could ask; it pushes, so a write token exists. The
// shape that keeps the two apart:
//   - only a comment from someone whose repository permission is write or
//     above starts it, checked through the API, not the association;
//   - every job's GITHUB_TOKEN is read-only, and the top level grants nothing;
//   - capture, the one job that runs pull request code (`make screenshots`),
//     never sees the app token, keeps no checkout credentials, and saves no
//     Go cache into the default branch's cache scope;
//   - a protected head branch is never a push target;
//   - commit, the one job that mints the app token, runs no make, go, or
//     script from the checkout.
func TestPRScreenshotsNeverRunsPullRequestCodeBesideAWriteToken(t *testing.T) {
	path := filepath.Join(".github", "workflows", "pr-screenshots.yml")
	text := readRepoFile(t, path)
	var workflow prScreenshotsWorkflow
	if err := yaml.Unmarshal([]byte(text), &workflow); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	if len(workflow.Permissions) != 0 {
		t.Errorf("top-level permissions = %v, want none", workflow.Permissions)
	}
	for name, job := range workflow.Jobs {
		for scope, access := range job.Permissions {
			if access != "read" {
				t.Errorf("job %s grants %s: %s; every write goes through the app token", name, scope, access)
			}
		}
	}

	resolve, ok := workflow.Jobs["resolve"]
	if !ok {
		t.Fatal("pr-screenshots.yml has no resolve job")
	}
	for _, want := range []string{"github.event.comment.author_association", "github.event.issue.pull_request"} {
		if !strings.Contains(resolve.If, want) {
			t.Errorf("resolve job's condition does not check %s", want)
		}
	}
	// An association is not a permission: MEMBER is any member of the
	// owning organization. The resolve step must ask for the commenter's
	// permission on this repository and accept only write or above.
	checkedPermission := false
	for _, step := range resolve.Steps {
		if strings.Contains(step.Run, "/collaborators/$COMMENTER/permission") &&
			strings.Contains(step.Run, "admin|maintain|write) ;;") {
			checkedPermission = true
		}
	}
	if !checkedPermission {
		t.Error("resolve job does not check the commenter's repository permission")
	}
	refusedProtected := false
	for _, step := range resolve.Steps {
		if strings.Contains(step.Run, "--jq .protected") && strings.Contains(step.Run, `"$protected" != "false"`) {
			refusedProtected = true
		}
	}
	if !refusedProtected {
		t.Error("resolve job does not refuse a protected head branch; the app token may bypass protection")
	}

	capture, ok := workflow.Jobs["capture"]
	if !ok {
		t.Fatal("pr-screenshots.yml has no capture job")
	}
	ranScreenshots := false
	for _, step := range capture.Steps {
		if strings.Contains(step.Run, "make screenshots") {
			ranScreenshots = true
		}
		if strings.Contains(step.Uses, "create-github-app-token") || strings.Contains(step.Run+strings.Join(mapValues(step.With), " "), "app-token") {
			t.Errorf("capture step %q touches the app token; capture runs pull request code", step.Name)
		}
		if strings.HasPrefix(step.Uses, "actions/checkout@") && step.With["persist-credentials"] != "false" {
			t.Errorf("capture's checkout keeps credentials; set persist-credentials: false")
		}
		// An issue_comment run has the default branch's cache scope; a
		// cache saved after pull request code ran would reach test.yml and
		// release.yml.
		if strings.HasPrefix(step.Uses, "actions/setup-go@") && step.With["cache"] != "false" {
			t.Errorf("capture's setup-go caches; set cache: false")
		}
	}
	if !ranScreenshots {
		t.Error("capture job no longer runs make screenshots")
	}

	commit, ok := workflow.Jobs["commit"]
	if !ok {
		t.Fatal("pr-screenshots.yml has no commit job")
	}
	for _, step := range commit.Steps {
		for _, line := range strings.Split(step.Run, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
				continue
			}
			command := fields[0]
			switch {
			case command == "make", command == "go", command == "bash", command == "sh",
				command == "source", command == ".", strings.HasPrefix(command, "./"),
				strings.HasPrefix(command, "scripts/"), strings.HasPrefix(command, "test/"):
				t.Errorf("commit step %q runs %q; the job holding the app token must run no pull request code", step.Name, strings.TrimSpace(line))
			}
		}
	}
}

func mapValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v)
	}
	return out
}
