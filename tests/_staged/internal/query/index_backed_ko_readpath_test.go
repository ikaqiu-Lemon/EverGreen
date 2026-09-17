package query

// [T-006-B2c Phase 3] 本文件承载 k/o 关系读路径中**依赖 internal/index 包**的断言：
// 四后端矩阵（healthy / missing / stale / corrupt）的逐项降级码，以及索引后端读路径的零
// 副作用负控。架构边界（cmd/eg 的 TestStage4IndexPackageBoundary）只放行 backend* /
// index_backed* / degrade* 前缀的 query 测试文件 import internal/index，故与
// relation_ko_readpath_test.go（扫描底座、不 import index）分置于此 `index_backed` 前缀文件。
//
// 复用 relation_ko_readpath_test.go 的语料（korVault / korMakeStale / korFroms /
// korReasonOf / itoa）与 backend_test / index_backed_opinion_test 的脚手架
// （bkShow / osShow / bkRel / bkoBuildIndexWithOpinions / bkDropIndex / bkCorruptIndex），
// 绝不伪造 DTO 掩盖链路。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// —— ③ 四后端矩阵：healthy / missing / stale / corrupt 业务结果逐字等价 ——

// korProjection 是一次读的业务投影（供跨后端逐字比对；不含降级诊断——那是 backend 的事）。
type korProjection struct {
	CardJSON    string
	CardMissing string
	OpnJSON     string
	OpnMissing  string
	RelInFroms  string
	RelInReason string
}

func korProject(t *testing.T, root string, wantIndex bool) (korProjection, [][]Diagnostic) {
	t.Helper()
	beta, err := bkShow(root, model.CardID(korBetaID))
	if err != nil {
		t.Fatalf("card show beta：%v", err)
	}
	pori, err := osShow(root, korPoriID)
	if err != nil {
		t.Fatalf("opinion show pori：%v", err)
	}
	rv, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korBetaID)})
	if err != nil {
		t.Fatalf("rel beta：%v", err)
	}
	if got := beta.backend.UseIndex(); got != wantIndex {
		t.Fatalf("card show 后端与预期不符：useIndex=%v want=%v（%s）",
			got, wantIndex, beta.backend.Message)
	}
	if got := pori.backend.UseIndex(); got != wantIndex {
		t.Fatalf("opinion show 后端与预期不符：useIndex=%v want=%v", got, wantIndex)
	}
	if got := rv.backend.UseIndex(); got != wantIndex {
		t.Fatalf("rel 后端与预期不符：useIndex=%v want=%v", got, wantIndex)
	}
	cb, _ := json.Marshal(beta.Card)
	ob, _ := json.Marshal(pori.Opinion)
	return korProjection{
		CardJSON:    string(cb),
		CardMissing: strings.Join(beta.MissingTargets, ","),
		OpnJSON:     string(ob),
		OpnMissing:  strings.Join(pori.MissingTargets, ","),
		RelInFroms:  strings.Join(korFroms(rv.Data.RelationsIn), ","),
		RelInReason: korReasonOf(rv.Data.RelationsIn, korPoriID),
	}, [][]Diagnostic{beta.Diagnostics, pori.Diagnostics, rv.Diagnostics}
}

// korAssertDegradeCodes 逐项钉一条读命令的降级诊断码：
//   - wantW == ""（healthy）：W22/W23/W24/Q5 全 0（健康 ⇒ 无码 ⇒ 无 Q5）；
//   - wantW == W23（missing）/ W22（stale）/ W24（corrupt）：**恰**该 W 码一条 + **恰**一条 Q5，
//     其余两个 W 码均为 0（合同 §6.3：降级必有且仅有一条原因码，与 Q5 同现）。
func korAssertDegradeCodes(t *testing.T, label string, diags []Diagnostic, wantW string) {
	t.Helper()
	for _, w := range []string{index.CodeIndexStale, index.CodeIndexMissing, index.CodeIndexCorrupt} {
		want := 0
		if w == wantW {
			want = 1
		}
		if got := countCode(diags, w); got != want {
			t.Fatalf("%s：降级码 %s 期望 %d 条，实际 %d（%v）", label, w, want, got, codesOf(diags))
		}
	}
	wantQ5 := 0
	if wantW != "" {
		wantQ5 = 1
	}
	if got := countCode(diags, CodeQ5); got != wantQ5 {
		t.Fatalf("%s：Q5 期望 %d 条，实际 %d（%v）", label, wantQ5, got, codesOf(diags))
	}
}

func TestReadPathKOFourBackendEquivalence(t *testing.T) {
	labels := []string{"card show", "opinion show", "rel"}
	// 基线：healthy 索引（含观点）。三条读命令都走索引后端，W22/W23/W24/Q5 全 0。
	root := korVault(t)
	bkoBuildIndexWithOpinions(t, root)
	healthy, hdiags := korProject(t, root, true)
	for i, d := range hdiags {
		korAssertDegradeCodes(t, "healthy/"+labels[i], d, "")
	}

	// missing / corrupt / stale：各自在**新建的 healthy vault** 上破坏，再取投影（应降级为扫描）。
	// 逐项断言降级码：missing 恰 W23+Q5、stale 恰 W22+Q5、corrupt 恰 W24+Q5；业务投影与 healthy 逐字等价。
	for _, c := range []struct {
		name   string
		wantW  string
		break_ func(*testing.T, string)
	}{
		{"missing", index.CodeIndexMissing, bkDropIndex},
		{"corrupt", index.CodeIndexCorrupt, bkCorruptIndex},
		{"stale", index.CodeIndexStale, korMakeStale},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := korVault(t)
			bkoBuildIndexWithOpinions(t, r)
			c.break_(t, r)
			got, gdiags := korProject(t, r, false)
			for i, d := range gdiags {
				korAssertDegradeCodes(t, c.name+"/"+labels[i], d, c.wantW)
			}
			if got != healthy {
				t.Fatalf("%s 降级后业务投影与 healthy 不等价：\nhealthy=%+v\n%s=%+v",
					c.name, healthy, c.name, got)
			}
		})
	}
}

// —— ④′ 索引后端读路径零副作用（与扫描负控 TestReadPathKOZeroSideEffects 互补）——

// TestReadPathKOIndexedReadZeroSideEffects —— **索引后端**读路径的零副作用负控（与扫描负控
// 互补）：先建 healthy 索引，对**完整 vault 的权威产物**（`.index/eg.db` 的逐字节内容
// 与 mtime、全部 Markdown 源文件、以及可能存在的 `.git`）做读前快照；随后跑遍四组合读路径
// （三条读命令都必须走索引后端），再做读后快照，要求逐字相等——证明 indexed read 既不改权威
// Markdown、不动 `.git`、也不回写索引主库一个字节。
//
// 关于 `-wal` / `-shm`：`journal_mode=WAL` 打开只读连接时 SQLite 会初始化读侧 WAL 机制，
// 可能落一个 **0 帧的空 `-wal`** 与共享内存 `-shm` 边车——这是索引契约（schema.go 保留名单
// 明确「WAL 的必然副产物，不算污染」）允许的引擎产物，不是对 vault 的写。因此权威快照排除这两个
// 边车，另用 korWALFrameBytes 断言 `-wal` **零数据帧**（读没有向索引写入任何提交）。
func TestReadPathKOIndexedReadZeroSideEffects(t *testing.T) {
	root := korVault(t)
	bkoBuildIndexWithOpinions(t, root)

	// 前置：应已建好 .index 主库。
	if _, err := os.Stat(index.DBPath(root)); err != nil {
		t.Fatalf("前置：应已建好 .index/eg.db，实际 %v", err)
	}
	before := korSnapshotAuthoritative(t, root)

	beta, err := bkShow(root, model.CardID(korBetaID))
	if err != nil {
		t.Fatalf("card show beta：%v", err)
	}
	if !beta.backend.UseIndex() {
		t.Fatalf("前置：healthy 索引下 card show 应走索引后端，实际 %s（%s）",
			beta.backend.Kind, beta.backend.Message)
	}
	pori, err := osShow(root, korPoriID)
	if err != nil {
		t.Fatalf("opinion show pori：%v", err)
	}
	if !pori.backend.UseIndex() {
		t.Fatalf("前置：healthy 索引下 opinion show 应走索引后端，实际 %s", pori.backend.Kind)
	}
	rv, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korPoriID)})
	if err != nil {
		t.Fatalf("rel o-pori：%v", err)
	}
	if !rv.backend.UseIndex() {
		t.Fatalf("前置：healthy 索引下 rel 应走索引后端，实际 %s", rv.backend.Kind)
	}
	// 再补一条焦点为卡的 rel，覆盖索引后端「按 dst_id 反查来源 + 回权威取 reason」这条路径。
	if _, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korBetaID)}); err != nil {
		t.Fatalf("rel k-beta：%v", err)
	}

	after := korSnapshotAuthoritative(t, root)
	if before != after {
		t.Fatalf("索引后端读路径改动了 vault 权威产物（含 .index/eg.db、Markdown、.git）——零副作用被破坏：\nbefore=%s\nafter =%s",
			before, after)
	}
	// 读不得向索引写入任何数据帧：只读打开落下的 `-wal`（若有）必须是 0 字节（零帧）。
	if n := korWALFrameBytes(t, root); n != 0 {
		t.Fatalf("索引只读打开不得写入 WAL 帧，实际 -wal 有 %d 字节数据（读路径疑似回写索引）", n)
	}
}

// korSnapshotAuthoritative 只快照 vault 的**权威产物**：跳过 `.index/eg.db-wal` 与
// `.index/eg.db-shm` 这两个 WAL 引擎边车（它们是只读打开的合法副产物、非 vault 写），
// 其余（`.index/eg.db`、Markdown、`.git`）逐字节 + mtime 折成确定性指纹串。
func korSnapshotAuthoritative(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, "-wal") || strings.HasSuffix(p, "-shm") {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		sum := sha256.Sum256(raw)
		lines = append(lines, filepath.ToSlash(rel)+":"+
			itoa(info.Size())+":"+itoa(info.ModTime().UnixNano())+":"+hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatalf("快照 vault 权威产物失败：%v", err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// korWALFrameBytes 返回 `.index/eg.db-wal` 的字节数（不存在记 0）：WAL 文件为 0 字节即「无
// 数据帧」，据此反证只读打开没有向索引写入任何提交。
func korWALFrameBytes(t *testing.T, root string) int64 {
	t.Helper()
	info, err := os.Stat(index.DBPath(root) + "-wal")
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("stat -wal 失败：%v", err)
	}
	return info.Size()
}
