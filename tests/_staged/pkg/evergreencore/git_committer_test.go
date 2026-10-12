package evergreencore

import (
	"context"
	"os"
	"path/filepath"
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

func TestLocalGitCommitterAuditsDeletedPathsRegardlessOfTracking(t *testing.T) {
	for _, tracking := range []string{"tracked", "untracked", "staged creation"} {
		for _, mixed := range []bool{false, true} {
			t.Run(fmtGitDeleteCase(tracking, mixed), func(t *testing.T) {
				root := initGitFixture(t)
				deleted := "box/deleted.sy"
				writeFile(t, root, "box/edited.sy", []byte("before\n"))
				writeFile(t, root, "unrelated.txt", []byte("before\n"))
				if tracking == "tracked" {
					writeFile(t, root, deleted, []byte("deleted before\n"))
				}
				gitCommand(t, root, "add", ".")
				gitCommand(t, root, "commit", "-m", "initial")
				if tracking != "tracked" {
					writeFile(t, root, deleted, []byte("deleted before\n"))
				}
				if tracking == "staged creation" {
					gitCommand(t, root, "add", "--", deleted)
				}
				if err := os.Remove(filepath.Join(root, deleted)); err != nil {
					t.Fatal(err)
				}
				writeFile(t, root, "unrelated.txt", []byte("user dirty\n"))
				paths := []string{deleted}
				if mixed {
					writeFile(t, root, "box/edited.sy", []byte("after\n"))
					paths = append(paths, "box/edited.sy")
				}
				committer, err := NewLocalGitCommitter(root)
				if err != nil {
					t.Fatal(err)
				}
				parent, _ := committer.Head(context.Background())
				request := CommitRequest{
					OperationID: "op-deleted", Principal: Principal{Type: PrincipalUser, ID: "user"},
					Command: NativeEditCommand, PlanHash: "sha256:delete-plan", ExpectedParent: parent,
					Paths: paths, DeletedPaths: []string{deleted},
				}
				sha, err := committer.Commit(context.Background(), request)
				if err != nil || sha == parent || sha == "" {
					t.Fatalf("deleted path could not complete its audit commit: %s %v", sha, err)
				}
				tree := gitCommand(t, root, "ls-tree", "-r", "--name-only", sha)
				if strings.Contains(tree, deleted) {
					t.Fatal("deleted file remained in the committed tree")
				}
				if got := gitCommand(t, root, "show", sha+":box/edited.sy"); got != map[bool]string{true: "after\n", false: "before\n"}[mixed] {
					t.Fatalf("mixed write omitted or invented: %q", got)
				}
				if got := gitCommand(t, root, "show", sha+":unrelated.txt"); got != "before\n" {
					t.Fatal("audit absorbed an unrelated edit")
				}
				if status := gitCommand(t, root, "status", "--porcelain"); !strings.Contains(status, "unrelated.txt") ||
					strings.Contains(status, deleted) || strings.Contains(status, "box/edited.sy") {
					t.Fatalf("real index did not preserve the intended state: %s", status)
				}
				retried, err := committer.Commit(context.Background(), request)
				if err != nil || retried != sha || gitCommand(t, root, "rev-list", "--count", "HEAD") != "2\n" {
					t.Fatalf("deleted path audit duplicated on retry: %s %v", retried, err)
				}
				if message := gitCommand(t, root, "show", "-s", "--format=%B", sha); !strings.Contains(message,
					"Evergreen-Operation-ID: op-deleted") || !strings.Contains(message, "Evergreen-Plan-Hash: sha256:delete-plan") {
					t.Fatal("empty tree change lost its operation audit")
				}
			})
		}
	}
}

func fmtGitDeleteCase(tracking string, mixed bool) string {
	if mixed {
		return tracking + "/mixed"
	}
	return tracking + "/delete only"
}

func TestLocalGitCommitterRejectsInvalidDeletesAndAccidentalEmptyCommits(t *testing.T) {
	for _, mode := range []string{"unchanged", "missing write", "delete outside write set", "delete still exists", "duplicate delete"} {
		t.Run(mode, func(t *testing.T) {
			root := initGitFixture(t)
			writeFile(t, root, "box/a.sy", []byte("before\n"))
			gitCommand(t, root, "add", ".")
			gitCommand(t, root, "commit", "-m", "initial")
			committer, err := NewLocalGitCommitter(root)
			if err != nil {
				t.Fatal(err)
			}
			parent, _ := committer.Head(context.Background())
			request := CommitRequest{
				OperationID: "op-invalid-delete", Principal: Principal{Type: PrincipalUser, ID: "user"},
				Command: NativeEditCommand, PlanHash: "sha256:plan", ExpectedParent: parent, Paths: []string{"box/a.sy"},
			}
			switch mode {
			case "missing write":
				if err := os.Remove(filepath.Join(root, "box/a.sy")); err != nil {
					t.Fatal(err)
				}
			case "delete outside write set":
				request.DeletedPaths = []string{"box/missing.sy"}
			case "delete still exists":
				request.DeletedPaths = []string{"box/a.sy"}
			case "duplicate delete":
				request.DeletedPaths = []string{"box/a.sy", "box/a.sy"}
			}
			if _, err = committer.Commit(context.Background(), request); err == nil {
				t.Fatal("invalid delete or empty write produced an audit commit")
			}
			if head, _ := committer.Head(context.Background()); head != parent {
				t.Fatal("invalid deletion changed HEAD")
			}
		})
	}
}

func initGitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitCommand(t, root, "init", "-q")
	gitCommand(t, root, "config", "user.name", "Evergreen Test")
	gitCommand(t, root, "config", "user.email", "evergreen@example.invalid")
	gitCommand(t, root, "config", "core.hooksPath", ".git/hooks")
	return root
}

func gitCommand(t *testing.T, root string, args ...string) string {
	t.Helper()
	output, stderr, err := runGit(context.Background(), root, nil, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s\n%s", strings.Join(args, " "), err, output, stderr)
	}
	return string(output)
}
