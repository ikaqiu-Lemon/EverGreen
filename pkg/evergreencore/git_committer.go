package evergreencore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type LocalGitCommitter struct {
	root string
}

func NewLocalGitCommitter(root string) (*LocalGitCommitter, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, stderr, err := runGit(context.Background(), absolute, nil, "rev-parse", "--show-toplevel"); err != nil {
		return nil, fmt.Errorf("authority root is not a Git worktree: %s: %w", strings.TrimSpace(string(stderr)), err)
	}
	return &LocalGitCommitter{root: absolute}, nil
}

func (c *LocalGitCommitter) Head(ctx context.Context) (string, error) {
	stdout, _, err := runGit(ctx, c.root, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(stdout)), nil
}

func (c *LocalGitCommitter) Commit(ctx context.Context, request CommitRequest) (string, error) {
	if existing, err := c.findOperation(ctx, request.OperationID, request.PlanHash); err != nil || existing != "" {
		return existing, err
	}
	head, err := c.Head(ctx)
	if err != nil {
		return "", err
	}
	if request.ExpectedParent != "" && head != request.ExpectedParent {
		return "", fmt.Errorf("Git parent changed: current=%s expected=%s", head, request.ExpectedParent)
	}
	paths := append([]string(nil), request.Paths...)
	sort.Strings(paths)
	if len(paths) == 0 {
		return "", errors.New("Git commit path set is empty")
	}
	for _, path := range paths {
		if err := validateAuthorityPath(path); err != nil {
			return "", fmt.Errorf("invalid Git path %q: %w", path, err)
		}
	}
	if err := c.rejectExternalStaged(ctx, paths); err != nil {
		return "", err
	}

	gitDirOut, stderr, err := runGit(ctx, c.root, nil, "rev-parse", "--git-dir")
	if err != nil {
		return "", fmt.Errorf("resolve Git directory: %s: %w", strings.TrimSpace(string(stderr)), err)
	}
	gitDir := strings.TrimSpace(string(gitDirOut))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(c.root, gitDir)
	}
	indexPath := filepath.Join(gitDir, "evergreen-index-"+request.OperationID)
	_ = os.Remove(indexPath)
	_ = os.Remove(indexPath + ".lock")
	defer os.Remove(indexPath)
	defer os.Remove(indexPath + ".lock")
	env := []string{"GIT_INDEX_FILE=" + indexPath}

	if head != "" {
		if _, stderr, err = runGit(ctx, c.root, env, "read-tree", head); err != nil {
			return "", fmt.Errorf("prepare isolated Git index: %s: %w", strings.TrimSpace(string(stderr)), err)
		}
	}
	addArgs := append([]string{"add", "--"}, paths...)
	if _, stderr, err = runGit(ctx, c.root, env, addArgs...); err != nil {
		return "", fmt.Errorf("stage operation paths in isolated index: %s: %w", strings.TrimSpace(string(stderr)), err)
	}
	if _, _, err = runGit(ctx, c.root, env, "diff", "--cached", "--quiet"); err == nil {
		return "", errors.New("operation produced no Git diff")
	}

	subject := fmt.Sprintf("evergreen: %s (%s)", request.Command, request.OperationID)
	body := strings.Join([]string{
		"Evergreen-Operation-ID: " + request.OperationID,
		"Evergreen-Principal: " + string(request.Principal.Type) + ":" + request.Principal.ID,
		"Evergreen-Auth-Source: " + request.Principal.AuthSource,
		"Evergreen-Request-Reason: " + request.Principal.RequestReason,
		"Evergreen-Plan-Hash: " + request.PlanHash,
	}, "\n")
	if _, stderr, err = runGit(ctx, c.root, env, "commit", "--no-verify", "-m", subject, "-m", body); err != nil {
		return "", fmt.Errorf("commit operation: %s: %w", strings.TrimSpace(string(stderr)), err)
	}
	resetArgs := append([]string{"reset", "-q", "HEAD", "--"}, paths...)
	if _, stderr, err = runGit(ctx, c.root, nil, resetArgs...); err != nil {
		return "", fmt.Errorf("refresh real Git index after operation commit: %s: %w", strings.TrimSpace(string(stderr)), err)
	}
	sha, err := c.Head(ctx)
	if err != nil {
		return "", err
	}
	if sha == "" {
		return "", errors.New("Git commit succeeded without a HEAD")
	}
	return sha, nil
}

func (c *LocalGitCommitter) rejectExternalStaged(ctx context.Context, operationPaths []string) error {
	stdout, stderr, err := runGit(ctx, c.root, nil, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return fmt.Errorf("inspect staged paths: %s: %w", strings.TrimSpace(string(stderr)), err)
	}
	allowed := make(map[string]struct{}, len(operationPaths))
	for _, path := range operationPaths {
		allowed[filepath.ToSlash(path)] = struct{}{}
	}
	var external []string
	for _, raw := range bytes.Split(stdout, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		path := filepath.ToSlash(string(raw))
		if _, ok := allowed[path]; !ok {
			external = append(external, path)
		}
	}
	if len(external) > 0 {
		sort.Strings(external)
		return fmt.Errorf("refusing to commit while unrelated paths are staged: %s", strings.Join(external, ", "))
	}
	return nil
}

func (c *LocalGitCommitter) findOperation(ctx context.Context, operationID, planHash string) (string, error) {
	pattern := "Evergreen-Operation-ID: " + operationID
	stdout, _, err := runGit(ctx, c.root, nil,
		"log", "--all", "--fixed-strings", "--grep="+pattern, "--format=%H", "-n", "8")
	if err != nil {
		return "", nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(stdout)), "\n") {
		sha := strings.TrimSpace(line)
		if sha == "" {
			continue
		}
		body, stderr, showErr := runGit(ctx, c.root, nil, "show", "-s", "--format=%B", sha)
		if showErr != nil {
			return "", fmt.Errorf("inspect existing operation commit: %s: %w", strings.TrimSpace(string(stderr)), showErr)
		}
		lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
		hasOperation := false
		existingPlan := ""
		for _, trailer := range lines {
			switch {
			case trailer == pattern:
				hasOperation = true
			case strings.HasPrefix(trailer, "Evergreen-Plan-Hash: "):
				existingPlan = strings.TrimPrefix(trailer, "Evergreen-Plan-Hash: ")
			}
		}
		if !hasOperation {
			continue
		}
		if existingPlan != planHash {
			return "", validationError(
				CodeOperationIDReused,
				"operation_id",
				fmt.Sprintf("operation ID %q already committed with plan %s", operationID, existingPlan),
			)
		}
		return sha, nil
	}
	return "", nil
}

func runGit(ctx context.Context, root string, extraEnv []string, args ...string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = root
	command.Env = append(os.Environ(), extraEnv...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
