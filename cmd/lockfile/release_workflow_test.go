package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The release workflow is read as text (no YAML parser in the module): its
// actions are pinned by commit SHA, and nothing is built or signed before
// the "tag is on main" guard passed.
func TestReleaseWorkflowGuards(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	uses := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*(\S+)`).FindAllStringSubmatch(s, -1)
	if len(uses) == 0 {
		t.Fatal("no actions found")
	}
	pinned := regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	for _, u := range uses {
		if !pinned.MatchString(u[1]) {
			t.Errorf("action %s is not pinned to a commit SHA", u[1])
		}
	}
	for _, want := range []string{
		"\n  on-main:\n",
		`git merge-base --is-ancestor "$GITHUB_SHA" refs/remotes/origin/main`,
		"\n    needs: on-main\n",
		"\n    needs: [on-main, test]\n",
		"\n  image:\n", "\n    needs: [on-main, image]\n",
		`cosign sign --yes "$IMAGE@$digest"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}
	if strings.Contains(s, "${{ github.ref_name }}") || strings.Contains(s, "${{ github.event") {
		t.Error("release.yml interpolates untrusted context into a script")
	}
}
