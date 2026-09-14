package cli

// [S4] internal/cli/bench.go：`eg bench` 命令在本仓的**唯一**落点
// （M5 索引架构合同 §7 性能指标与门槛口径 A-46 + §8.1 命令表 bench 行；
// M5 · T-evergreen.s1_main_flow-158614-068 阶段 B）。
//
// # 唯一职责（一句话）
//
// 按合同 §7.3 **冻结的采样口径**在当前 vault 上采五个数，原样报出来 ——
// 它是一把尺子，不是一个优化器：不调参、不缓存、不挑样本、不隐瞒慢样本。
//
// # 五条边界（逐条不得越线）
//
//   - **口径由合同定，本命令不得自行改动**：预热 3 轮丢弃 + 计入 50 轮、P95 取第 48 小值
//     （不插值）、构建两指标不预热各测 3 次取中位数、固定 20 词 / 20 id 循环取用 ——
//     这些数字全部落在 `BenchFrozenSpec()` 一处，`TestBenchSpecIsFrozen` 逐格钉住。
//   - **权威零改动**：本命令对当前 vault **零写入**（连 `.index/` 都不写）。构建耗时必须
//     写盘，因此它在 `os.MkdirTemp` 的**临时副本**上做（合同 §8.1 bench 行括号内的原话），
//     副本用完即删；当前 vault 的 `domains/` `sources/` `proposals/` `reviews/` 与 `.index/`
//     一个字节都不碰，也不发 commit、不落 `eg report --last`。
//   - **计时边界是进程墙钟**：三条读指标通过**真实 fork/exec 子进程**采样（含进程启动、
//     参数解析、打开索引、查询、序列化输出），因为「用户与 agent 感知的就是进程时间」
//     （合同 §7.3 计时边界行）。在进程内直接调 handler 会系统性低估，绝不这么做。
//   - **data 恰五键**：`search_p95_ms` / `card_show_p95_ms` / `rel_p95_ms` /
//     `index_build_ms` / `index_incremental_ms`，一个不多一个不少（§7.2）。
//     环境信息（CPU / Go 版本）与门槛建议值只进 `summary` 与诊断区，**不进 data** ——
//     否则就是扩张信封（§8.3 禁令）。
//   - **不含门槛判定**：本命令只报实测值，**不判合格**。门槛是「实测 × 1.5 向上取整到
//     10ms」，由合同 §7 的回填列持有、由 `test/e2e/m5_bench_p95.sh` 断言 ——
//     让被测者自己判自己合格，等于没有门禁。
//
// # 为什么前置要求「索引 healthy」
//
// 合同 §7.3 索引状态行：前三条指标**在 healthy 索引上采样**，降级路径不设门槛、只报告。
// 因此索引不健康时本命令**不采样也不猜**：退 1 并告诉用户先跑 `eg index build`。
// 若容忍在降级路径上采样，采出来的数会与门槛不同源 —— 那种绿是假的。

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query/queryset"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
)

// —— 五个指标键（封闭集合，次序即合同 §7.2 表格行序）——
const (
	BenchKeySearchP95   = "search_p95_ms"
	BenchKeyCardShowP95 = "card_show_p95_ms"
	BenchKeyRelP95      = "rel_p95_ms"
	BenchKeyIndexBuild  = "index_build_ms"
	BenchKeyIndexIncr   = "index_incremental_ms"
)

// BenchMetricKeys 返回 `eg bench --json` 的 data 键集合（**恰 5 个**，次序即输出次序）。
func BenchMetricKeys() []string {
	return []string{
		BenchKeySearchP95, BenchKeyCardShowP95, BenchKeyRelP95,
		BenchKeyIndexBuild, BenchKeyIndexIncr,
	}
}

// BenchSpec 是采样口径。它**不是**可调参数面：CLI 上没有任何 flag 能改动它，
// 只有包内测试可以注入更小的轮数（否则每跑一次单测都要 159 次 fork）。
type BenchSpec struct {
	Warmup    int // 每条读指标先跑几轮丢弃
	Rounds    int // 每条读指标计入统计的轮数
	P95Rank   int // P95 取升序第几个样本（1-based，不插值）
	BuildRuns int // 构建两指标各测几次（取中位数，不预热）
}

// BenchFrozenSpec 是合同 §7.3 冻结的口径（`TestBenchSpecIsFrozen` 逐格钉住）。
//
//	预热 3 轮丢弃 + 计入 50 轮；P95 = ceil(0.95 × 50) = 第 48 小值，不插值；
//	index_build_ms / index_incremental_ms 不预热、各测 3 次取中位数。
func BenchFrozenSpec() BenchSpec { return BenchSpec{Warmup: 3, Rounds: 50, P95Rank: 48, BuildRuns: 3} }

// BenchThreshold 按合同 §7.3 门槛公式把实测值换算成门槛：`ceil(实测 × 1.5 / 10) × 10`。
//
// 本命令**不用**它判合格（那是 e2e 的事），只在 summary 里给出建议回填值，
// 免得回填时有人手算错一位。
func BenchThreshold(measuredMs int) int {
	if measuredMs <= 0 {
		return 10
	}
	return int(math.Ceil(float64(measuredMs)*1.5/10.0)) * 10
}

// BenchError 是采样执行失败（子进程样本失败 / 临时副本建不起来）。
//
// 退出码沿用数值 `4`：合同 §8.1 给 `eg bench` 的退出码集合恰 `{0, 1, 4}` ——
// `1` 给参数非法与前置不满足（零副作用），`4` 给「执行到一半失败」。
// 数值 4 在 M1 合同里的措辞是「Git 提交失败」，M5 起同一数值多了两类合同已指派的成因
// （分页参数错、bench 采样失败）；**措辞漂移**已按 T-…-067 的口径登记，
// 由 T-…-069 统一收口，本文件不擅自重写 M1 的退出码表。
type BenchError struct {
	Msg string
	Err error
}

func (e *BenchError) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + "：" + e.Err.Error()
}

func (e *BenchError) Unwrap() error { return e.Err }

// BenchAuthorityNotice 是「只读采样」这条边界在命令面的固定文案（便于逐字断言）。
const BenchAuthorityNotice = "eg bench 只读采样：当前 vault 的权威 Markdown 与 .index/ 一个字节都不写、" +
	"不发 commit；构建两指标在临时副本上测，副本用完即删"

// BenchSpecNotice 是采样口径的固定文案（人类可读，事实与 BenchFrozenSpec 同源）。
func BenchSpecNotice(spec BenchSpec) string {
	return fmt.Sprintf("采样口径（合同 §7.3 冻结，命令面无参数可调）：读指标预热 %d 轮丢弃 + 计入 %d 轮，"+
		"P95 取升序第 %d 小值不插值；index_build_ms / index_incremental_ms 不预热、各测 %d 次取中位数；"+
		"固定 %d 个关键词与 %d 个 id 循环取用（写死在 internal/query/queryset，不随机）",
		spec.Warmup, spec.Rounds, spec.P95Rank, spec.BuildRuns,
		queryset.KeywordCount, queryset.IDSampleCount)
}

// benchCommand 注册 `eg bench`（合同 §8.1：写 `.index/` = 否、写权威 Markdown = 否）。
//
// 参数面**恰零个命令私有 flag**：`--json` / `--vault` 由 dispatch 统一声明。
// 采样口径不可由命令行改动 —— 能调的门槛不是门槛。
func benchCommand() *Command {
	return &Command{
		Name:     "bench",
		Display:  "bench",
		Summary:  "按合同 §7.3 冻结口径采样五个性能指标（只读；不判合格，只报实测值）",
		Owner:    "T-evergreen.s1_main_flow-158614-068",
		ReadOnly: true,
		Usage: `eg bench [--json]

参数：（无命令私有参数；采样口径由合同 §7.3 冻结，命令面无 flag 可调）

输出 data 恰五键（M5 索引架构合同 §7.2）：
  search_p95_ms          eg search <kw> 端到端 P95（进程墙钟，不含索引构建）
  card_show_p95_ms       eg card show <id> 端到端 P95
  rel_p95_ms             eg rel <id> 端到端 P95
  index_build_ms         全量构建的单次墙钟耗时（3 次取中位数，不预热）
  index_incremental_ms   改动 1 个文件后 eg index sync 的单次墙钟耗时（3 次取中位数）

前置：索引必须 healthy（合同 §7.3 只在健康索引上给门槛；降级路径不设门槛）。
不健康时退 1 并提示先跑 eg index build —— 不在降级路径上采样，避免与门槛不同源。

只读到底：当前 vault 的权威 Markdown 与 .index/ 零写入、零 commit；
构建两指标在临时副本上测量，副本用完即删。
本命令只报实测值、不判合格：门槛 = ceil(实测 × 1.5 / 10) × 10，由合同 §7 回填列与
test/e2e/m5_bench_p95.sh 持有。
退出码：0 | 1 参数非法或前置不满足（零副作用） | 4 采样执行失败（权威恒零改动）
`,
		Validate: func(inv *Invocation) error {
			if len(inv.Args) > 0 {
				return &UsageError{Msg: fmt.Sprintf("eg bench 不收位置参数，多余的：%v", inv.Args)}
			}
			return nil
		},
	}
}

// wireBench 是 `eg bench` 处理函数在本仓的**唯一**挂载点（与 wireCheck / wireIndex 同一手法）：
// 把 Wire 收进本文件，使「性能采样不作为任何命令的前置」这条判据由**文件边界**结构性保证 ——
// `runBench` 这个符号在 internal/cli 里只出现在 bench.go / bench_test.go。
func (r *Root) wireBench() { _ = r.Wire("bench", r.runBench) }

// benchExecutable 解析「用哪一个可执行文件去采样」。
//
// 生产路径恒为 `os.Executable()`（就是当前这个 eg 二进制，采的是它自己的进程时间）；
// 测试可以注入一个预先构建好的 eg 路径 —— 因为 `go test` 里的 `os.Executable()`
// 是**测试二进制**，拿它当 eg 去 fork 会跑起一堆测试而不是命令。
func (r *Root) benchExecutable() (string, error) {
	if r.benchExec != nil {
		return r.benchExec()
	}
	return os.Executable()
}

// benchSpec 返回本次采样口径：生产恒为冻结口径，包内测试可注入更小轮数。
func (r *Root) benchSpec() BenchSpec {
	if r.benchSpecOverride != nil {
		return *r.benchSpecOverride
	}
	return BenchFrozenSpec()
}

// runBench 是 `eg bench` 的唯一入口。
func (r *Root) runBench(inv *Invocation) (*Result, error) {
	root := inv.VaultRoot
	if strings.TrimSpace(root) == "" {
		return nil, &UsageError{Msg: "找不到 vault 根（零副作用）：在 vault 内执行或给 --vault <dir>"}
	}
	spec := r.benchSpec()

	exe, err := r.benchExecutable()
	if err != nil {
		return nil, &BenchError{Msg: "定位 eg 可执行文件失败（未采样、零副作用）", Err: err}
	}

	// —— 前置①：索引必须 healthy（合同 §7.3）。这里用与 `eg index status` **同一套**判定
	// （index.Check），不另写第二套口径。
	cur, err := r.indexCurrent(root, false)
	if err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf("读取权威 Markdown 失败（零副作用）：%v", err)}
	}
	con := index.Check(index.DirPath(root), cur)
	if !con.Fresh() {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"索引不是 healthy+fresh（%s / %s：%s），本命令不在降级路径上采样"+
				"（合同 §7.3 只在健康索引上给门槛）：先跑 eg index build / eg index sync 再重试；零副作用",
			con.Code, con.Reason, con.Message)}
	}

	// —— 前置②：确定查询集。关键词恒取固定 20 个；id 取固定采样点与在册卡的交集，
	// 不足则按**字典序**补齐（确定性补齐，绝不随机挑）。
	ids, err := benchSampleIDs(root)
	if err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf("枚举在册卡失败（零副作用）：%v", err)}
	}
	if len(ids) == 0 {
		return nil, &UsageError{Msg: "vault 里没有任何知识卡，无从采样（零副作用）：" +
			"先造语料，例如 go run ./test/perf/corpus_gen.go -cards 10000 -rels 30000 -out <vault>"}
	}
	kws := queryset.Keywords()

	// —— 三条读指标：真实 fork/exec，进程墙钟。
	searchP95, err := r.benchP95(exe, root, spec, func(i int) []string {
		return []string{"search", kws[i%len(kws)], "--json"}
	})
	if err != nil {
		return nil, err
	}
	cardP95, err := r.benchP95(exe, root, spec, func(i int) []string {
		return []string{"card", "show", ids[i%len(ids)], "--json"}
	})
	if err != nil {
		return nil, err
	}
	relP95, err := r.benchP95(exe, root, spec, func(i int) []string {
		return []string{"rel", ids[i%len(ids)], "--json"}
	})
	if err != nil {
		return nil, err
	}

	// —— 两条构建指标：在临时副本上做（当前 vault 的 .index/ 一个字节不动）。
	buildMs, incrMs, copyNote, err := r.benchBuildMetrics(exe, root, spec)
	if err != nil {
		return nil, err
	}

	rep := report.New()
	rep.AddInfo("eg bench", report.NonOp, "%s", BenchAuthorityNotice)
	rep.AddInfo("eg bench", report.NonOp, "%s", BenchSpecNotice(spec))
	rep.AddInfo("eg bench", report.NonOp, "%s", copyNote)

	data := map[string]interface{}{
		BenchKeySearchP95:   searchP95,
		BenchKeyCardShowP95: cardP95,
		BenchKeyRelP95:      relP95,
		BenchKeyIndexBuild:  buildMs,
		BenchKeyIndexIncr:   incrMs,
	}
	// data **恰五键**：报告体、环境信息、门槛建议一律不进 data（§7.2 / §8.3）。
	res := &Result{
		Data:      data,
		DataOrder: BenchMetricKeys(),
		Warnings:  toCLIReportDiags(rep.Warnings),
		// 摘要**不**追加 report.Lines()：那套模板讲的是「写了几个文件 / 发了什么 commit」，
		// 对一条零写入的采样命令而言全是 0，读起来像在暗示这里会写盘。诊断区照常给 I1 三条。
		Summary: benchSummary(data, spec, len(ids), len(cur.Files)),
	}
	return res, nil
}

// benchP95 采一条读指标：先 Warmup 轮丢弃，再 Rounds 轮计入，取升序第 P95Rank 小值。
//
// 每一轮都是一次**独立子进程**：`exe --vault <root> <args...>`。任何一轮非零退出即判失败
// （退 4）—— 采样期间命令本该恒成功，失败了却把它当样本，采出来的就是假数据。
func (r *Root) benchP95(exe, root string, spec BenchSpec, args func(i int) []string) (int, error) {
	for i := 0; i < spec.Warmup; i++ {
		if _, err := benchRun(exe, root, args(i)); err != nil {
			return 0, err
		}
	}
	samples := make([]time.Duration, 0, spec.Rounds)
	for i := 0; i < spec.Rounds; i++ {
		d, err := benchRun(exe, root, args(spec.Warmup+i))
		if err != nil {
			return 0, err
		}
		samples = append(samples, d)
	}
	return ceilMs(percentileNoInterp(samples, spec.P95Rank)), nil
}

// benchRun 跑一次子进程并返回墙钟耗时。
//
// stdout 全部丢弃（我们量的是「产出结果所需时间」，不是「打印到终端所需时间」，
// 但序列化本身**计入** —— 因为 `--json` 一直开着，序列化在子进程内部照常发生）；
// stderr 留最后一小段，失败时把真实原因带回来，不许出现「采样失败，原因不明」。
func benchRun(exe, root string, args []string) (time.Duration, error) {
	full := append([]string{"--vault", root}, args...)
	cmd := exec.Command(exe, full...)
	cmd.Dir = root
	cmd.Stdout = io.Discard
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	cmd.Stdin = nil

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	if err != nil {
		tail := strings.TrimSpace(errBuf.String())
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		return 0, &BenchError{
			Msg: fmt.Sprintf("采样样本失败（eg %s）：采样期间命令本应恒成功，"+
				"失败样本不计入统计也不静默丢弃；权威 Markdown 零改动。stderr 尾部：%s",
				strings.Join(args, " "), tail),
			Err: err,
		}
	}
	return elapsed, nil
}

// benchBuildMetrics 在**临时副本**上测 index_build_ms 与 index_incremental_ms。
//
// 为什么必须用副本：这两条指标天生要写盘（合同 §7.2 表格「是否含索引构建耗时 = 是」），
// 而 `eg bench` 又承诺当前 vault 零写入（§8.1 bench 行）。副本里只放权威文件与
// `evergreen.yml` / `.gitignore`，**不复制** `.index/` 与 `.git/`：
//
//   - 不复制 `.index/`：第一次构建必须是**真正的全量新建**（fresh build），不能是 no-op；
//   - 不复制 `.git/`：`index_meta.head` 在非 git 目录下写空串（合同 §4.2 允许），
//     三次构建与三次 sync 的口径彼此一致，不会因 HEAD 变化而互相污染。
//
// 三次构建：第 1 次是 fresh build，第 2/3 次用 `index rebuild`（合同 §8.1 明文
// 「先删 .index/ 再全量构建，结果与 fresh build 等价」）—— 不这么做的话第 2 次就是 no-op，
// 量出来的会是「体检耗时」而不是「构建耗时」。取三者中位数。
//
// 三次增量：每次**只改 1 个文件**（在正文尾部追加一行注释），随即 `eg index sync`。
// 每次改的都是不同的文件，避免第 2/3 次因内容早已在页缓存里而系统性偏快。
func (r *Root) benchBuildMetrics(exe, root string, spec BenchSpec) (buildMs, incrMs int, note string, err error) {
	// 一次性副本目录由 internal/index 亲手创建并封装清理：本层**拿不到**「待删除路径」这个旋钮
	// （只能给一个目录名前缀），因此 U-02 派生物例外「删的只可能是该包自造的派生目录」
	// 是类型与控制流保证，不是注释承诺。CLI 层依旧零裸整目录清理。
	tmp, cleanup, err := index.NewScratch("eg-bench-copy-")
	if err != nil {
		return 0, 0, "", &BenchError{Msg: "创建临时副本目录失败（当前 vault 零写入）", Err: err}
	}
	defer func() { _ = cleanup() }()

	copied, err := benchCopyVault(root, tmp)
	if err != nil {
		return 0, 0, "", &BenchError{Msg: "复制 vault 到临时目录失败（当前 vault 零写入）", Err: err}
	}

	builds := make([]time.Duration, 0, spec.BuildRuns)
	for i := 0; i < spec.BuildRuns; i++ {
		sub := "build" // 第 1 次：真正的 fresh build
		if i > 0 {
			sub = "rebuild" // 之后：先删再全量建（与 fresh build 等价，不是 no-op）
		}
		d, rerr := benchRun(exe, tmp, []string{"index", sub, "--json"})
		if rerr != nil {
			return 0, 0, "", rerr
		}
		builds = append(builds, d)
	}

	// 增量前先确保副本索引 healthy（上一步刚 rebuild 完，这里只是把前置写清楚）。
	cards, err := benchCardFiles(tmp)
	if err != nil {
		return 0, 0, "", &BenchError{Msg: "枚举临时副本里的卡文件失败", Err: err}
	}
	if len(cards) == 0 {
		return 0, 0, "", &BenchError{Msg: "临时副本里没有卡文件，无从测增量索引耗时"}
	}
	incrs := make([]time.Duration, 0, spec.BuildRuns)
	for i := 0; i < spec.BuildRuns; i++ {
		// 只改 1 个文件：合同 §7.2 index_incremental_ms 的定义逐字如此。
		target := cards[(i*7+1)%len(cards)]
		if aerr := benchTouch(target, i); aerr != nil {
			return 0, 0, "", &BenchError{Msg: "改动临时副本里的 1 个文件失败", Err: aerr}
		}
		d, rerr := benchRun(exe, tmp, []string{"index", "sync", "--json"})
		if rerr != nil {
			return 0, 0, "", rerr
		}
		incrs = append(incrs, d)
	}

	note = fmt.Sprintf("构建两指标在临时副本上测量（复制 %d 个文件，不含 .index/ 与 .git/）："+
		"index_build_ms 取 %d 次全量构建的中位数（首次 fresh build，其余 eg index rebuild，"+
		"合同 §8.1 明文与 fresh build 等价）；index_incremental_ms 取「改 1 个文件 + eg index sync」"+
		"%d 次的中位数；副本已删除，当前 vault 零写入",
		copied, spec.BuildRuns, spec.BuildRuns)
	return ceilMs(median(builds)), ceilMs(median(incrs)), note, nil
}

// benchCopyVault 把 vault 的**权威面**复制到 dst：`evergreen.yml` / `.gitignore` 与
// `domains/` `sources/` `proposals/` `reviews/` 四棵树。`.index/` 与 `.git/` 明确不复制。
func benchCopyVault(src, dst string) (int, error) {
	n := 0
	for _, f := range []string{"evergreen.yml", ".gitignore"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return n, err
		}
		if err := os.WriteFile(filepath.Join(dst, f), b, 0o644); err != nil {
			return n, err
		}
		n++
	}
	for _, tree := range []string{"domains", "sources", "proposals", "reviews"} {
		base := filepath.Join(src, tree)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, rerr := filepath.Rel(src, p)
			if rerr != nil {
				return rerr
			}
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			if werr := os.WriteFile(target, b, 0o644); werr != nil {
				return werr
			}
			n++
			return nil
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// benchCardFiles 返回 vault 里全部卡文件的绝对路径（字典序，确定性）。
func benchCardFiles(root string) ([]string, error) {
	var out []string
	base := filepath.Join(root, "domains")
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		if filepath.Base(filepath.Dir(p)) != "knowledge" {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// benchTouch 在卡文件**正文尾部**追加一行注释：只改正文、不碰 frontmatter，
// 因此不会把语料改成非法卡（frontmatter 一格不动，id / status / relations 全不变）。
func benchTouch(path string, round int) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n<!-- eg bench 增量采样第 %d 轮（仅临时副本） -->\n", round+1)
	return err
}

// benchSampleIDs 返回 `card show` / `rel` 的采样 id 集合（确定性）。
//
// 取法：先取固定采样点 `queryset.SampleIDs(n)` 与在册卡的交集（在性能语料上这就是全部 20 个），
// 不足 20 个时按**字典序**从在册卡里补齐 —— 于是在任意 vault（含单测的小语料）上都能采样，
// 且同一个 vault 上恒得同一组 id。任何情况下都不随机挑。
func benchSampleIDs(root string) ([]string, error) {
	files, err := benchCardFiles(root)
	if err != nil {
		return nil, err
	}
	present := make(map[string]bool, len(files))
	all := make([]string, 0, len(files))
	for _, p := range files {
		id := strings.TrimSuffix(filepath.Base(p), ".md")
		present[id] = true
		all = append(all, id)
	}
	sort.Strings(all)

	out := make([]string, 0, queryset.IDSampleCount)
	seen := map[string]bool{}
	for _, id := range queryset.SampleIDs(len(all)) {
		if present[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	for _, id := range all {
		if len(out) >= queryset.IDSampleCount {
			break
		}
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out, nil
}

// percentileNoInterp 取升序第 rank 小值（1-based，**不插值**）。
//
// 不插值是合同 §7.3 的原话：插值实现之间会算出不同的 P95，门槛就不可比。
// 样本数不足 rank 时取最大值（只发生在包内测试注入的小轮数上；生产恒为 50 轮 / 第 48 小）。
func percentileNoInterp(samples []time.Duration, rank int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), samples...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	if rank < 1 {
		rank = 1
	}
	if rank > len(s) {
		rank = len(s)
	}
	return s[rank-1]
}

// median 取中位数（偶数个时取偏小的那个中间值：不做平均，保持「取到的是某个真实样本」）。
func median(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), samples...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[(len(s)-1)/2]
}

// ceilMs 把耗时向上取整成毫秒整数（合同 §7.3 输出单位行：毫秒、整数、向上取整）。
func ceilMs(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(math.Ceil(float64(d.Nanoseconds()) / 1e6))
}

// benchSummary 组人类可读摘要：只复述 data 与环境事实，不引入新事实。
//
// 环境登记（CPU / Go 版本 / GOOS-GOARCH）落在这里而**不进 data**：data 恰五键是合同硬约束。
// 门槛与环境绑定 —— 换机器要重新回填而不是放宽门槛（合同 §7.3 环境登记行），
// 因此这几行必须打出来，供 T-…-069 的验收报告逐字引用。
func benchSummary(data map[string]interface{}, spec BenchSpec, ids, cards int) []string {
	lines := []string{
		fmt.Sprintf("采样完成：%d 张在册卡、%d 个 id 采样点、%d 个关键词；口径见下（命令面无参数可调）",
			cards, ids, queryset.KeywordCount),
	}
	for _, k := range BenchMetricKeys() {
		v, _ := data[k].(int)
		lines = append(lines, fmt.Sprintf("  %-22s 实测 %6d ms   →  建议门槛 %6d ms（= ceil(实测 × 1.5 / 10) × 10）",
			k, v, BenchThreshold(v)))
	}
	lines = append(lines,
		BenchSpecNotice(spec),
		fmt.Sprintf("采样环境（门槛与环境绑定，换环境需重新回填而非放宽）：%s/%s、%d 逻辑核、Go %s",
			runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version()),
		BenchAuthorityNotice,
		"本命令不判合格：门槛数值由合同 §7 回填列持有，断言在 test/e2e/m5_bench_p95.sh",
	)
	return lines
}
