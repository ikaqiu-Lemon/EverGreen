package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestStringContainsVersionAndToolchain(t *testing.T) {
	got := String()
	for _, want := range []string{"eg ", Version, Commit, Date, runtime.Version()} {
		if !strings.Contains(got, want) {
			t.Fatalf("String() = %q，缺少 %q", got, want)
		}
	}
}

func TestDefaultsNotEmpty(t *testing.T) {
	if Version == "" || Commit == "" || Date == "" {
		t.Fatalf("版本字段不得为空：Version=%q Commit=%q Date=%q", Version, Commit, Date)
	}
}
