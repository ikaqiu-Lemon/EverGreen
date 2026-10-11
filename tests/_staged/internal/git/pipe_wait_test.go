package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGitExitDoesNotWaitForInheritedOutputDescriptors(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nsleep 3 &\nprintf 'stdout-before-exit\\n'\nprintf 'stderr-before-exit\\n' >&2\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	out, stderr, err := execGit(t.TempDir(), "probe")
	if time.Since(start) > 2*time.Second || err != nil || string(out) != "stdout-before-exit\n" || string(stderr) != "stderr-before-exit\n" {
		t.Fatalf("exit/output changed: elapsed=%s stdout=%q stderr=%q err=%v", time.Since(start), out, stderr, err)
	}
}

func TestGitOutputDescriptorExitStatus(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'failure-output\\n'\nprintf 'failure-reason\\n' >&2\nexit 91\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, stderr, err := execGit(t.TempDir(), "probe")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 91 || string(out) != "failure-output\n" || string(stderr) != "failure-reason\n" {
		t.Fatalf("failure/output changed: stdout=%q stderr=%q err=%v", out, stderr, err)
	}
}
