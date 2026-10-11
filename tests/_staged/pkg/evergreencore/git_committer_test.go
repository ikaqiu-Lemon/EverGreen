package evergreencore

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestLocalGitCommitterCommitsOnlyOperationPathsAndIsIdempotent(t *testing.T) {
	root := initGitFixture(t)
	writeFile(t, root, "box/a.sy", []byte("before-a\n"))
	writeFile(t, root, "box/b.sy", []byte("before-b\n"))
	writeFile(t, root, "unrelated.txt", []byte("before-unrelated\n"))
	gitCommand(t, root, "add", ".")
	gitCommand(t, root, "commit", "-m", "initial")

	writeFile(t, root, "box/a.sy", []byte("after-a\n"))
	writeFile(t, root, "box/b.sy", []byte("after-b\n"))
	writeFile(t, root, "unrelated.txt", []byte("dirty-unrelated\n"))

	committer, err := NewLocalGitCommitter(root)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := committer.Head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := CommitRequest{
		OperationID: "op-git-real",
		Principal: Principal{
			Type: PrincipalAgent, ID: "agent-1", AuthSource: "api-token",
			RequestReason: "materialize reviewed candidate",
		},
		Command: "claim.update", PlanHash: "sha256:plan",
		ExpectedParent: parent, Paths: []string{"box/b.sy", "box/a.sy"},
	}
	sha, err := committer.Commit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" || sha == parent {
		t.Fatalf("commit sha = %q parent = %q", sha, parent)
	}
	files := strings.Fields(gitCommand(t, root, "show", "--pretty=format:", "--name-only", sha))
	if strings.Join(files, " ") != "box/a.sy box/b.sy" {
		t.Fatalf("committed files = %v", files)
	}
	status := gitCommand(t, root, "status", "--porcelain")
	if !strings.Contains(status, "unrelated.txt") || strings.Contains(status, "box/a.sy") || strings.Contains(status, "box/b.sy") {
		t.Fatalf("unexpected status after selected commit:\n%s", status)
	}
	message := gitCommand(t, root, "show", "-s", "--format=%B", sha)
	for _, want := range []string{
		"Evergreen-Operation-ID: op-git-real",
		"Evergreen-Principal: agent:agent-1",
		"Evergreen-Auth-Source: api-token",
		"Evergreen-Request-Reason: materialize reviewed candidate",
		"Evergreen-Plan-Hash: sha256:plan",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("commit message missing %q:\n%s", want, message)
		}
	}

	retried, err := committer.Commit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if retried != sha {
		t.Fatalf("retry sha = %s, want %s", retried, sha)
	}
	if count := strings.TrimSpace(gitCommand(t, root, "rev-list", "--count", "HEAD")); count != "2" {
		t.Fatalf("commit count after retry = %s, want 2 total", count)
	}
}

func TestLocalGitCommitterRejectsUnrelatedStagedPaths(t *testing.T) {
	root := initGitFixture(t)
	writeFile(t, root, "box/a.sy", []byte("before\n"))
	writeFile(t, root, "unrelated.txt", []byte("before\n"))
	gitCommand(t, root, "add", ".")
	gitCommand(t, root, "commit", "-m", "initial")

	writeFile(t, root, "box/a.sy", []byte("after\n"))
	writeFile(t, root, "unrelated.txt", []byte("staged\n"))
	gitCommand(t, root, "add", "unrelated.txt")
	committer, err := NewLocalGitCommitter(root)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := committer.Head(context.Background())
	_, err = committer.Commit(context.Background(), CommitRequest{
		OperationID: "op-staged", Principal: Principal{Type: PrincipalUser, ID: "u"},
		Command: "claim.update", PlanHash: "sha256:plan", ExpectedParent: parent,
		Paths: []string{"box/a.sy"},
	})
	if err == nil || !strings.Contains(err.Error(), "unrelated paths are staged") {
		t.Fatalf("staged-path error = %v", err)
	}
	if head, _ := committer.Head(context.Background()); head != parent {
		t.Fatalf("HEAD changed on staged-path rejection: %s -> %s", parent, head)
	}
}

func initGitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitCommand(t, root, "init", "-q")
	gitCommand(t, root, "config", "user.name", "Evergreen Test")
	gitCommand(t, root, "config", "user.email", "evergreen@example.invalid")
	return root
}

func gitCommand(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
