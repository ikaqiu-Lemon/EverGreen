//go:build fts5 && (sqlcipher || libsqlcipher)

package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/conf"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

const (
	encryptedDoctorPhaseEnv = "SIYUAN_TEST_EG_ENCRYPTED_DOCTOR_PHASE"
	encryptedDoctorRootEnv  = "SIYUAN_TEST_EG_ENCRYPTED_DOCTOR_ROOT"
	encryptedDoctorPassword = "evergreen-encrypted-doctor-password"
	encryptedDoctorChildID  = "20261013120001-child"
)

// TestEvergreenEncryptedDoctorRepairsSQLCipherAcrossRestart also exposes explicit
// seed and doctor phases so an external backup/restore can be inserted between them.
func TestEvergreenEncryptedDoctorRepairsSQLCipherAcrossRestart(t *testing.T) {
	phase := os.Getenv(encryptedDoctorPhaseEnv)
	root := os.Getenv(encryptedDoctorRootEnv)
	switch phase {
	case "seed":
		if root == "" {
			t.Fatal("encrypted doctor seed requires an explicit workspace root")
		}
		seedEncryptedDoctorWorkspace(t, root)
		return
	case "doctor":
		if root == "" {
			t.Fatal("encrypted doctor phase requires an explicit workspace root")
		}
		doctorEncryptedWorkspace(t, root)
		return
	case "":
	default:
		t.Fatalf("unknown encrypted doctor phase %q", phase)
	}

	root = t.TempDir()
	runEncryptedDoctorPhase(t, root, "seed")
	runEncryptedDoctorPhase(t, root, "doctor")
}

func runEncryptedDoctorPhase(t *testing.T, root, phase string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0],
		"-test.run=^TestEvergreenEncryptedDoctorRepairsSQLCipherAcrossRestart$",
		"-test.timeout=170s", "-test.v")
	command.Env = append(os.Environ(),
		encryptedDoctorPhaseEnv+"="+phase,
		encryptedDoctorRootEnv+"="+root)
	output, err := command.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("encrypted doctor %s phase timed out:\n%s", phase, output)
	}
	if err != nil {
		t.Fatalf("encrypted doctor %s phase failed: %v\n%s", phase, err, output)
	}
	t.Logf("encrypted doctor %s phase:\n%s", phase, output)
}

func seedEncryptedDoctorWorkspace(t *testing.T, root string) {
	t.Helper()
	requireEmptyEncryptedDoctorRoot(t, root)
	prepareEncryptedDoctorWorkspace(t, root, false)
	initEncryptedDoctorDatabases()
	boxID := ""
	closed := false
	cleanup := func() {
		if closed {
			return
		}
		_ = kernelEvergreen.CloseDefaultRuntime()
		if boxID != "" && IsBoxUnlocked(boxID) {
			LockBox(boxID)
		}
		sql.CloseDatabase()
		closed = true
	}
	t.Cleanup(cleanup)

	if err := EnableEncryptedNotebook(encryptedDoctorPassword); err != nil {
		t.Fatalf("enable encrypted notebooks: %v", err)
	}
	var err error
	boxID, err = CreateEncryptedBox("Encrypted doctor", encryptedDoctorPassword)
	if err != nil {
		t.Fatalf("create encrypted notebook: %v", err)
	}
	relative := boxID + "/" + boxID + ".sy"
	session, err := (filesys.EvergreenDocumentStorage{
		Root: util.DataDir, Boxes: []string{boxID},
	}).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plain, err := fs.ReadFile(session.Filesystem(), relative)
	if err != nil {
		session.Close()
		t.Fatal(err)
	}
	plain = encryptedDoctorClaim(t, plain, boxID)
	stored, err := session.Encode(relative, plain)
	session.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !util.IsCiphertext(stored) {
		t.Fatal("managed encrypted authority was stored as plaintext")
	}
	if err = os.WriteFile(filepath.Join(util.DataDir, filepath.FromSlash(relative)), stored, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(util.DataDir, ".gitignore"),
		[]byte("/.siyuan/evergreen/writer.lease\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	gitEncryptedDoctor(t, "init", "-q")
	gitEncryptedDoctor(t, "config", "user.name", "Evergreen Encrypted Doctor")
	gitEncryptedDoctor(t, "config", "user.email", "evergreen-encrypted-doctor@example.invalid")
	gitEncryptedDoctor(t, "add", "-A")
	gitEncryptedDoctor(t, "commit", "-q", "-m", "Seed encrypted authority")

	scope, err := filesys.WithEvergreenNotebookScope(context.Background(), []string{boxID})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := EvergreenRuntime(scope)
	if err != nil {
		t.Fatal(err)
	}
	report, err := DoctorEvergreenWorkspace(scope, runtime, true)
	if err != nil || !report.DerivedAfter.Healthy || !report.Parity {
		t.Fatalf("seed doctor rebuild = %+v err=%v", report, err)
	}
	if err = kernelEvergreen.CloseDefaultRuntime(); err != nil {
		t.Fatal(err)
	}
	LockBox(boxID)
	sql.CloseDatabase()
	closed = true

	gitEncryptedDoctor(t, "add", "-A")
	if gitEncryptedDoctor(t, "diff", "--cached", "--quiet") == "exit:1" {
		gitEncryptedDoctor(t, "commit", "-q", "-m", "Initialize Evergreen runtime")
	}
	if status := gitEncryptedDoctor(t, "status", "--porcelain"); status != "" {
		t.Fatalf("seed workspace is dirty:\n%s", status)
	}
	t.Logf("EVERGREEN_ENCRYPTED_DOCTOR_SEED root=%s data=%s box=%s cipher=%s git=%s",
		root, util.DataDir, boxID,
		encryptedDoctorFileDigest(t, filepath.Join(util.DataDir, filepath.FromSlash(relative))),
		gitEncryptedDoctor(t, "rev-parse", "HEAD"))
}

func doctorEncryptedWorkspace(t *testing.T, root string) {
	t.Helper()
	prepareEncryptedDoctorWorkspace(t, root, true)
	initEncryptedDoctorDatabases()
	boxID := ""
	closed := false
	cleanup := func() {
		if closed {
			return
		}
		_ = kernelEvergreen.CloseDefaultRuntime()
		if boxID != "" && IsBoxUnlocked(boxID) {
			LockBox(boxID)
		}
		sql.CloseDatabase()
		closed = true
	}
	t.Cleanup(cleanup)

	boxIDs, err := listAllEncryptedBoxIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(boxIDs) != 1 {
		t.Fatalf("expected one encrypted notebook, got %v", boxIDs)
	}
	boxID = boxIDs[0]
	boxEncryption, err := GetBoxEncryption(boxID)
	if err != nil {
		t.Fatal(err)
	}
	if err = UnlockBox(boxID, encryptedDoctorPassword, boxEncryption); err != nil {
		t.Fatalf("unlock restored encrypted notebook: %v", err)
	}

	relative := boxID + "/" + boxID + ".sy"
	cipherPath := filepath.Join(util.DataDir, filepath.FromSlash(relative))
	cipherBefore := encryptedDoctorFileDigest(t, cipherPath)
	configBefore := encryptedDoctorConfigDigest(t, root, boxID)
	headBefore := gitEncryptedDoctor(t, "rev-parse", "HEAD")
	scope, err := filesys.WithEvergreenNotebookScope(context.Background(), []string{boxID})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := EvergreenRuntime(scope)
	if err != nil {
		t.Fatal(err)
	}

	initial, err := DoctorEvergreenWorkspace(scope, runtime, false)
	if !core.HasDiagnostic(err, core.CodeDerivedStale) || initial.DerivedBefore.Healthy {
		t.Fatalf("empty restored projection was not stale: %+v err=%v", initial, err)
	}
	initial, err = DoctorEvergreenWorkspace(scope, runtime, true)
	if err != nil || initial.DerivedBefore.Healthy || !initial.DerivedAfter.Healthy || !initial.Parity {
		t.Fatalf("restored projection rebuild = %+v err=%v", initial, err)
	}
	assertEncryptedDoctorSQLCipher(t, util.EncryptedDBPath(boxID))
	assertEncryptedDoctorSQLCipher(t, util.EncryptedBlockTreeDBPath(boxID))
	archiveBefore, err := runtime.CanonicalExport(scope)
	if err != nil {
		t.Fatal(err)
	}
	archiveDigest, err := core.ArchiveDigest(archiveBefore)
	if err != nil {
		t.Fatal(err)
	}

	database := sql.GetEncryptedDB(boxID)
	if database == nil {
		t.Fatal("encrypted content database is not open")
	}
	if _, err = database.Exec("DELETE FROM blocks WHERE root_id = ?", boxID); err != nil {
		t.Fatal(err)
	}
	if err = treenode.RemoveAuthorityBlockTree(boxID, boxID); err != nil {
		t.Fatal(err)
	}
	corrupt, err := DoctorEvergreenWorkspace(scope, runtime, false)
	if !core.HasDiagnostic(err, core.CodeDerivedStale) || corrupt.DerivedBefore.Healthy {
		t.Fatalf("corrupt encrypted projection was not stale: %+v err=%v", corrupt, err)
	}
	repaired, err := DoctorEvergreenWorkspace(scope, runtime, true)
	if err != nil || repaired.DerivedBefore.Healthy || !repaired.DerivedAfter.Healthy || !repaired.Parity {
		t.Fatalf("corrupt encrypted projection rebuild = %+v err=%v", repaired, err)
	}
	var sqlRows, ftsRows int
	if err = database.QueryRow("SELECT count(*) FROM blocks WHERE root_id = ?", boxID).Scan(&sqlRows); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow("SELECT count(*) FROM blocks_fts WHERE root_id = ?", boxID).Scan(&ftsRows); err != nil {
		t.Fatal(err)
	}
	if sqlRows != repaired.DerivedAfter.ExpectedBlocks || ftsRows != repaired.DerivedAfter.ExpectedBlocks {
		t.Fatalf("repaired encrypted rows: SQL=%d FTS=%d expected=%d",
			sqlRows, ftsRows, repaired.DerivedAfter.ExpectedBlocks)
	}
	if block := treenode.GetBlockTreeInBox(encryptedDoctorChildID, boxID); block == nil || block.RootID != boxID {
		t.Fatalf("encrypted blocktree projection missing after repair: %+v", block)
	}
	archiveAfter, err := runtime.CanonicalExport(scope)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := core.DiffCanonical(archiveBefore, archiveAfter)
	if err != nil || len(diff) != 0 {
		t.Fatalf("doctor changed encrypted authority: diff=%+v err=%v", diff, err)
	}
	if got := encryptedDoctorFileDigest(t, cipherPath); got != cipherBefore {
		t.Fatalf("doctor changed authority ciphertext: before=%s after=%s", cipherBefore, got)
	}
	if got := encryptedDoctorConfigDigest(t, root, boxID); got != configBefore {
		t.Fatalf("doctor changed encryption configuration: before=%s after=%s", configBefore, got)
	}
	if got := gitEncryptedDoctor(t, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("doctor changed Git HEAD: before=%s after=%s", headBefore, got)
	}

	if err = kernelEvergreen.CloseDefaultRuntime(); err != nil {
		t.Fatal(err)
	}
	LockBox(boxID)
	sql.CloseDatabase()
	closed = true
	if status := gitEncryptedDoctor(t, "status", "--porcelain", "--untracked-files=no"); status != "" {
		t.Fatalf("doctor changed tracked Git content:\n%s", status)
	}
	t.Logf("EVERGREEN_ENCRYPTED_DOCTOR_PASS root=%s data=%s box=%s authority=%s cipher=%s config=%s git=%s documents=%d blocks=%d",
		root, util.DataDir, boxID, archiveDigest, cipherBefore, configBefore, headBefore,
		repaired.DerivedAfter.ExpectedDocuments, repaired.DerivedAfter.ExpectedBlocks)
}

func prepareEncryptedDoctorWorkspace(t *testing.T, root string, load bool) {
	t.Helper()
	var err error
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	util.WorkspaceDir = root
	util.DataDir = filepath.Join(root, "data")
	util.TempDir = filepath.Join(root, "temp")
	util.ConfDir = filepath.Join(root, "conf")
	util.HistoryDir = filepath.Join(root, "history")
	util.RepoDir = filepath.Join(root, "repo")
	util.QueueDir = filepath.Join(util.TempDir, "queue")
	util.DBPath = filepath.Join(util.TempDir, util.DBName)
	util.HistoryDBPath = filepath.Join(util.TempDir, "history.db")
	util.AssetContentDBPath = filepath.Join(util.TempDir, "asset_content.db")
	util.BlockTreeDBPath = filepath.Join(util.TempDir, "blocktree.db")
	for _, directory := range []string{
		util.DataDir, util.TempDir, util.ConfDir, util.HistoryDir, util.RepoDir,
	} {
		if err = os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if !load {
		for _, directory := range []string{
			filepath.Join(util.DataDir, "assets"),
			filepath.Join(util.DataDir, filepath.FromSlash(core.RuntimeDirName), core.TransactionsDirName),
		} {
			if err = os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}

	util.Lang = "en"
	util.StatusBarCfg = util.NewStatusBar(false)
	util.WorkingDir, err = filepath.Abs(filepath.Join("..", "..", "app"))
	if err != nil {
		t.Fatal(err)
	}
	initLang()
	app := NewAppConf()
	app.Lang = "en"
	app.FileTree = conf.NewFileTree()
	*app.FileTree.BoxDocEnabled = true
	app.NotebookCrypto = conf.NewNotebookCrypto()
	app.Sync = conf.NewSync()
	app.Editor = conf.NewEditor()
	app.Export = conf.NewExport()
	app.Search = conf.NewSearch()
	if load {
		configPath := filepath.Join(util.ConfDir, "conf.json")
		data, readErr := os.ReadFile(configPath)
		switch {
		case readErr == nil:
			if err = json.Unmarshal(data, app); err != nil {
				t.Fatalf("load restored host config: %v", err)
			}
		case !os.IsNotExist(readErr):
			t.Fatal(readErr)
		}
	}
	Conf = app
	if load && !NotebookCryptoEnabled() {
		restoreNotebookCryptoConfigFromBackup()
	}
	if load && !NotebookCryptoEnabled() {
		t.Fatal("restored workspace did not recover encrypted notebook key configuration")
	}
	util.SetBooted()
}

func initEncryptedDoctorDatabases() {
	sql.InitDatabase(true)
	sql.InitHistoryDatabase(true)
	sql.InitAssetContentDatabase(true)
}

func requireEmptyEncryptedDoctorRoot(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		if err = os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("encrypted doctor seed root must be empty: %s", root)
	}
}

func encryptedDoctorClaim(t *testing.T, raw []byte, documentID string) []byte {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["Children"] = json.RawMessage(`[{
		"ID":"` + encryptedDoctorChildID + `",
		"Type":"NodeParagraph",
		"Properties":{"id":"` + encryptedDoctorChildID + `"},
		"Children":[{"Type":"NodeText","Data":"Encrypted doctor projection needle"}]
	}]`)
	document["ID"] = json.RawMessage(fmt.Sprintf("%q", documentID))
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = core.EncodeSY(raw, &core.DocumentEnvelope{
		Spec: core.DocumentSpec,
		Entity: core.Entity{
			LogicalID: "c-encrypted-doctor", EntityType: core.EntityClaim,
			Schema: core.ClaimSchema, SemanticRevision: 1,
		},
		Claim: &core.Claim{
			ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			Status: "active", KindData: json.RawMessage(`{}`),
		},
		Relations: core.Relations{Outgoing: []core.TypedEdge{}},
	}, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertEncryptedDoctorSQLCipher(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < len("SQLite format 3\x00") {
		t.Fatalf("encrypted database is unexpectedly short: %s", path)
	}
	if bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
		t.Fatalf("encrypted database has a plaintext SQLite header: %s", path)
	}
}

func encryptedDoctorFileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func encryptedDoctorConfigDigest(t *testing.T, root, boxID string) string {
	t.Helper()
	paths := []string{
		filepath.Join(root, "conf", "conf.json"),
		filepath.Join(root, "data", ".siyuan", "data-crypto-backup.json"),
		filepath.Join(root, "data", boxID, ".siyuan", "conf.json"),
		filepath.Join(root, "data", boxID, ".siyuan", notebookCryptoBackupFilename),
	}
	hash := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read encryption configuration %s: %v", path, err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write([]byte(filepath.ToSlash(relative)))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func gitEncryptedDoctor(t *testing.T, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", util.DataDir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		if len(args) == 3 && args[0] == "diff" && args[1] == "--cached" && args[2] == "--quiet" {
			if exit := new(exec.ExitError); errors.As(err, &exit) && exit.ExitCode() == 1 {
				return "exit:1"
			}
		}
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
