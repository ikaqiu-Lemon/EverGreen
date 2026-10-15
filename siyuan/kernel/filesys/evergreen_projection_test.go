// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package filesys

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenProjectionNeverRepairsAuthority(t *testing.T) {
	box, parent, p, raw := setupStalePathTest(t)
	abs := writeStalePathFixture(t, box, p, raw)
	if _, err := LoadTreeProjectionByData(raw, box, p, util.NewLute()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing parent should fail the projection: %v", err)
	}
	parentAbs := filepath.Join(util.DataDir, box, parent+".sy")
	if _, err := os.Stat(parentAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("projection created a missing parent: %v", err)
	}
	corrupt := []byte(`{"Properties":`)
	writeStalePathFixture(t, box, parent+".sy", corrupt)
	if _, err := LoadTreeProjectionByData(raw, box, p, util.NewLute()); err == nil {
		t.Fatal("corrupt parent should fail the projection")
	}
	for path, expected := range map[string][]byte{abs: raw, parentAbs: corrupt} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, expected) {
			t.Fatalf("projection changed authority %s: %v", path, err)
		}
	}
}
