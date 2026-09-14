// [S4] internal/query/queryset：M5 性能采样的**固定查询集**与语料命名规则的唯一定义处。
//
// 归位说明（system_assurance 批次 A）：本包历史位置在旧测试根（单数目录，已随测试树外置整体
// 删除）下的 perf/queryset，但它被产品代码
// `internal/cli/bench.go` import —— 被产品 import 的包按定义是产品代码。测试树外置到 `tests/`
// 后，旧位置会让产品树无法独立构建，故归位到 `internal/query/queryset`。
// 采样口径（固定查询集、不随机）一字未改；路径与 M5 合同 §7.3 字面的差异登记在
// issue I-evergreen.system_assurance-158614-001，历史合同不回改。
//
// # 为什么是一个独立的数据包
//
// M5 索引架构合同 §7.3 把采样口径冻结成「查询集写死在固定包内，**不得**随机」。
// 这句话有两个消费者：
//
//   - `tests/perf/corpus_gen.go` —— 按这里的规则**生成**语料（谁出现在哪张卡里）；
//   - `internal/cli/bench.go` —— 按这里的清单**发起**采样（查什么词、查哪些 id）。
//
// 如果两边各写一份字面量，语料与查询集就会静默漂移：关键词一个都命中不到时
// `search_p95_ms` 会假性变快，而门槛却是按「有命中」的实测值回填的 —— 那种绿是假的。
// 因此本包是**单一事实源**：两个消费者都只读这里，谁也不许另写一份。
//
// # 本包的硬边界
//
//   - **零依赖**：只 import 标准库的 `fmt`/`strconv`（`go list -deps` 里不出现本仓任何
//     `internal/` 包），因此被 `internal/cli` 反向 import 也不会造成依赖环，
//     也不影响 `cmd/eg/arch_test.go` 对 `internal/` 依赖方向的判据（它只看 internal/ 集合）。
//   - **纯确定性**：本包不含随机数、不读时钟、不读环境变量。同样入参恒得同样出参。
//   - **不含门槛数值**：门槛是「实测 × 1.5 向上取整到 10ms」，由合同 §7 的回填列与
//     仓内 P95 性能门槛门禁（suite `perf.bench-p95`）持有；本包只管「查什么」，不管「多快算合格」。
package queryset

import (
	"fmt"
	"strconv"
)

// CorpusDate 是语料 id 里的固定日期段（`k-YYYYMMDD-<slug>`，见 internal/model/id.go）。
// 写死而不取当天：语料必须可复算，取时钟就会每天生成出不同的 id。
const CorpusDate = "20261201"

// CorpusSlugPrefix 是语料 slug 的固定前缀，便于在 vault 里一眼分辨性能语料。
const CorpusSlugPrefix = "perf"

// CorpusDomain 是语料落盘的域名（`domains/<domain>/knowledge/<id>.md`）。
const CorpusDomain = "perf"

// CardID 返回第 i 张语料卡的 id（`i` 从 0 起，五位零填充保证字典序 == 生成序）。
func CardID(i int) string {
	return fmt.Sprintf("k-%s-%s-%05d", CorpusDate, CorpusSlugPrefix, i)
}

// Keywords 是 `eg search` 的固定查询集（**恰 20 个**，循环取用）。
//
// 覆盖合同 §2.6 的两条召回路：
//   - **D0**（ASCII 词）：前 8 个纯 ASCII 词；
//   - **D0b**（中文短词）：后 12 个中的 3 字词 6 个 + 2 字词 6 个 ——
//     中文 2 字是最容易在分词/前缀匹配上退化成全表扫的档，必须进采样集。
func Keywords() []string {
	return []string{
		// —— D0：ASCII（8）——
		"retrieval", "embedding", "chunking", "reranker",
		"latency", "throughput", "quantization", "sqlite",
		// —— D0b：中文 3 字（6）——
		"召回率", "语料库", "知识卡", "上下文", "重排序", "向量库",
		// —— D0b：中文 2 字（6）——
		"索引", "缓存", "分片", "推理", "训练", "检索",
	}
}

// KeywordCount 是查询集大小（封闭值；`Keywords()` 与它不一致即判语料/采样集漂移）。
const KeywordCount = 20

// IDSampleCount 是 `card show` / `rel` 采样用的固定 id 个数（**恰 20 个**，循环取用）。
const IDSampleCount = 20

// SampleIDs 返回 `card show` / `rel` 的固定 id 查询集（恰 IDSampleCount 个）。
//
// 取样位置**均匀铺开**而不是取前 20 张：前 20 张在任何 B-tree / 页缓存布局下都偏热，
// 只测它们会系统性低估 P95。这里按 `i * cards / 20` 取模铺开，覆盖 id 空间全程。
// `cards < 20` 时按 `i % cards` 回绕（小语料只用于自测，不产生门槛数值）。
func SampleIDs(cards int) []string {
	if cards <= 0 {
		return nil
	}
	out := make([]string, 0, IDSampleCount)
	for i := 0; i < IDSampleCount; i++ {
		idx := i * cards / IDSampleCount
		if cards < IDSampleCount {
			idx = i % cards
		}
		out = append(out, CardID(idx))
	}
	return out
}

// KeywordForCard 返回第 i 张卡**必须命中**的关键词（`i % 20`）。
//
// 生成器据此把关键词写进卡的标题与正文，采样器据此确定「这个词大约有 cards/20 条命中」。
// 于是 10,000 卡语料下每个词恒有 500 条命中 —— 既不是 0（会假性变快），
// 也不是全库（会退化成全表扫），是一个稳定、可复算的中等选择度。
func KeywordForCard(i int) string {
	kw := Keywords()
	return kw[((i%len(kw))+len(kw))%len(kw)]
}

// ExpectedHits 返回某个关键词在 `cards` 张语料上的**理论命中数**（用于 e2e 反证语料没跑偏）。
func ExpectedHits(cards int) int {
	if cards <= 0 {
		return 0
	}
	return cards / KeywordCount
}

// CardTitle 返回第 i 张卡的标题（含该卡的固定命中词，便于标题命中路径也被采样）。
func CardTitle(i int) string {
	return "性能语料卡 " + strconv.Itoa(i) + "：" + KeywordForCard(i)
}
