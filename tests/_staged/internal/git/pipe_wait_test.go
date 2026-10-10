package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGitExitDoesNotWaitForInheritedOutputDescriptors(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(executable, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EG_GIT_PIPE_HELPER", "parent")
	start := time.Now()
	out, stderr, err := execGit(t.TempDir(), "-test.run=^TestGitOutputDescriptorHelper$")
	if time.Since(start) > 2*time.Second || err != nil || string(out) != "stdout-before-exit\n" || string(stderr) != "stderr-before-exit\n" {
		t.Fatalf("exit/output changed: elapsed=%s stdout=%q stderr=%q err=%v", time.Since(start), out, stderr, err)
	}
}

func TestGitOutputDescriptorHelper(t *testing.T) {
	switch os.Getenv("EG_GIT_PIPE_HELPER") {
	case "parent":
		cmd := exec.Command(os.Args[0], "-test.run=^TestGitOutputDescriptorHelper$")
		cmd.Env = append(os.Environ(), "EG_GIT_PIPE_HELPER=child")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			os.Exit(91)
		}
		_, _ = os.Stdout.WriteString("stdout-before-exit\n")
		_, _ = os.Stderr.WriteString("stderr-before-exit\n")
		os.Exit(0)
	case "child":
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}
}
