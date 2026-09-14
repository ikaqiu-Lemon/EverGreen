// [S4] tests/perf/corpus_gen.go：M5 性能语料的**确定性**生成器
// （M5 索引架构合同 §7.3「语料规模」行：`-cards 10000 -rels 30000`，固定随机种子，
// 同参数两次生成逐字节相同）。
//
// # 用法
//
//	eg init --vault "$VAULT" --domain perf          # 先有一个 vault（本文件不造 vault）
//	go run ./tests/perf/corpus_gen.go -cards 10000 -rels 30000 -out "$VAULT"
//
// # 三条硬边界
//
//   - **确定性**：本文件不用 `math/rand`、不读时钟、不读环境变量。随机感来自
//     splitmix64 —— 一个把「卡序号」映射成固定伪随机数的纯函数，种子是编译期常量
//     （`-seed` 只是让 e2e 能反证「换种子结果就变」，默认值恒为 DefaultSeed）。
//     因此同参数两次生成的每个文件逐字节相同（`TestCorpusGenIsDeterministic` / e2e 第 2 步）。
//   - **只写指定目录**：一切写入都在 `-out` 指向的 vault 内的
//     `domains/<CorpusDomain>/knowledge/` 下；不碰 `.index/`、不碰 Git、不发 commit、
//     不写任何其它域，也不会覆盖 vault 里既有的非语料文件。
//   - **不是产品代码**：它在 `test/` 下，只服务于性能采样；`eg` 二进制不 import 它
//     （反向的 `internal/cli/bench.go` 只 import 同目录的纯数据包 `queryset`）。
//
// # 语料形态（为什么这么造）
//
//   - **关键词选择度固定**：第 i 张卡命中 `queryset.KeywordForCard(i)`，于是 20 个词
//     各有 `cards/20` 条命中（10,000 卡 ⇒ 每词 500 条）。既不是 0 命中（会让
//     `search_p95_ms` 假性变快），也不是全库命中（会退化成全表扫）。
//   - **关系度数均匀**：`rels/cards` 条为基数，余数摊给前若干张卡；目标去自环、去重复，
//     类型在四种论证关系里轮转 —— `rel_p95_ms` 采样时每张卡都有活干。
//   - **少量 deprecated + replaced_by**：每 97 张卡有一张失效并指向下一张，
//     用来让读路径的可见性过滤与 `replaced_by` 反查在采样中真实发生（而不是空跑）。
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query/queryset"
)

// DefaultSeed 是固定种子（合同 §7.3「固定随机种子」）。改它就等于换一套语料，
// 门槛数值随之失效 —— 因此它是常量，`-seed` 只用于 e2e 的反证实验。
const DefaultSeed uint64 = 0x5EED_2026_1201

// deprecatedEvery：每多少张卡出现一张 deprecated + replaced_by 的失效卡。
const deprecatedEvery = 97

// fillerTokens 是正文填充词表（固定、非随机）：让每张卡有真实的词量与中英混排，
// 但不引入新的高选择度词干扰 20 个采样词的命中数。
var fillerTokens = []string{
	"pipeline", "batch", "window", "shard", "vector", "graph", "prompt", "token",
	"离线", "在线", "评测", "回归", "样本", "阈值", "队列", "落盘",
}

func main() {
	var (
		cards = flag.Int("cards", 10000, "生成的知识卡张数")
		rels  = flag.Int("rels", 30000, "生成的论证关系总条数")
		out   = flag.String("out", "", "目标 vault 根目录（必须已由 eg init 创建）")
		seed  = flag.Uint64("seed", DefaultSeed, "伪随机种子（默认恒为固定值；改动即换语料）")
	)
	flag.Parse()

	if err := run(*cards, *rels, *out, *seed); err != nil {
		// 用 log.Fatalf 而非 os.Exit：本仓门禁 TestOnlyMainCallsOsExit 规定裸 os.Exit 只许出现在
		// cmd/eg/main.go；语料生成器是 test/ 下的独立工具，按 Go 惯用法用 log.Fatalf 报错并退非零。
		log.Fatalf("corpus_gen: %v", err)
	}
}

func run(cards, rels int, out string, seed uint64) error {
	if cards <= 0 {
		return fmt.Errorf("-cards 必须为正整数，实际 %d", cards)
	}
	if rels < 0 {
		return fmt.Errorf("-rels 不得为负，实际 %d", rels)
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("-out 必填：指向一个已由 eg init 创建的 vault 根目录")
	}
	// 只往**已存在的 vault** 里写：语料生成器不造 vault（那是 `eg init` 的职责，
	// 它还要写 .gitignore / evergreen.yml / git 仓，重复实现必然漂移）。
	if _, err := os.Stat(filepath.Join(out, "evergreen.yml")); err != nil {
		return fmt.Errorf("%s 不像 vault 根（缺 evergreen.yml）：先跑 eg init --vault %s --domain %s",
			out, out, queryset.CorpusDomain)
	}

	dir := filepath.Join(out, "domains", queryset.CorpusDomain, "knowledge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("建目录 %s：%w", dir, err)
	}

	edges := planEdges(cards, rels, seed)
	digest := sha256.New()
	written := 0
	for i := 0; i < cards; i++ {
		body := renderCard(i, cards, edges[i], seed)
		path := filepath.Join(dir, queryset.CardID(i)+".md")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return fmt.Errorf("写 %s：%w", path, err)
		}
		// 摘要按「相对路径 + 内容」累计：路径也进摘要，改名同样会被反证出来。
		fmt.Fprintf(digest, "%s\x00", queryset.CardID(i)+".md")
		digest.Write([]byte(body))
		written++
	}

	total := 0
	for _, es := range edges {
		total += len(es)
	}
	// 输出一行 JSON（键序固定）：e2e 直接 grep 取值，不需要 jq。
	fmt.Printf(`{"cards":%d,"relations":%d,"keywords":%d,"expected_hits_per_keyword":%d,`+
		`"domain":%q,"seed":"0x%X","digest":%q}`+"\n",
		written, total, queryset.KeywordCount, queryset.ExpectedHits(cards),
		queryset.CorpusDomain, seed, hex.EncodeToString(digest.Sum(nil)))
	return nil
}

// edge 是一条待生成的关系（from 隐含为所属卡）。
type edge struct {
	Type   string
	Target string
	Reason string
}

// planEdges 把 rels 条关系确定性地摊到 cards 张卡上。
//
// 度数分配：每张卡 `rels / cards` 条为基数，余数 `rels % cards` 摊给**前若干张**卡
// （不是随机摊：随机摊会让「哪张卡多一条」随实现细节漂移）。
// 目标选取：以 splitmix64(seed, i, k) 为步长在 id 空间上跳，跳到自己或已选过的目标就顺移，
// 因此无自环、无重复边，且完全可复算。
func planEdges(cards, rels int, seed uint64) [][]edge {
	types := relationTypes()
	base, extra := rels/cards, rels%cards
	all := make([][]edge, cards)
	for i := 0; i < cards; i++ {
		n := base
		if i < extra {
			n++
		}
		if n > cards-1 {
			n = cards - 1 // 最多连到其它每一张卡各一次
		}
		used := make(map[int]bool, n)
		es := make([]edge, 0, n)
		for k := 0; k < n; k++ {
			step := int(splitmix64(seed^uint64(i)*0x9E37_79B9_7F4A_7C15+uint64(k)) % uint64(cards))
			t := (i + 1 + step) % cards
			for t == i || used[t] {
				t = (t + 1) % cards
			}
			used[t] = true
			es = append(es, edge{
				// 类型轮转由 (i+k) 决定：同一张卡上的多条关系一定跨类型，
				// 于是四级排序键的第 ① 级（relation_type）在采样时真实起作用。
				Type:   types[(i+k)%len(types)],
				Target: queryset.CardID(t),
				Reason: fmt.Sprintf("perf-%05d-%02d", i, k),
			})
		}
		all[i] = es
	}
	return all
}

// relationTypes 是四种论证关系的**字面量**（与 internal/model.ValidRelationTypes 同集合）。
//
// 这里写字面量而不 import `internal/model`：本包在 `test/` 下、`queryset` 是零依赖数据包，
// 生成器也应当只依赖「合同里写下的字面量」。若两边不一致，`eg` 读语料时会直接判非法关系类型
// —— e2e 第 1 步的 `eg index build` 与 `eg check` 会当场红，不存在静默漂移。
func relationTypes() []string {
	return []string{"opposing", "limits", "supports", "derives"}
}

// renderCard 渲染第 i 张卡的完整 Markdown（frontmatter + 正文）。
//
// frontmatter 键序与既有语料一致（id → status → created_at → updated_at → title →
// sources → relations → replaced_by）：键序稳定是「两次生成逐字节相同」的一部分。
func renderCard(i, cards int, es []edge, seed uint64) string {
	var b strings.Builder
	w := bufio.NewWriter(&b)
	kw := queryset.KeywordForCard(i)
	deprecated := deprecatedEvery > 0 && i%deprecatedEvery == 0 && cards > 1

	fmt.Fprintln(w, "---")
	fmt.Fprintf(w, "id: %s\n", queryset.CardID(i))
	if deprecated {
		fmt.Fprintln(w, "status: deprecated")
	} else {
		fmt.Fprintln(w, "status: active")
	}
	fmt.Fprintf(w, "created_at: '%s'\n", isoDate())
	fmt.Fprintf(w, "updated_at: '%sT10:00:00+08:00'\n", isoDate())
	fmt.Fprintf(w, "title: %s\n", queryset.CardTitle(i))
	fmt.Fprintln(w, "sources: []")
	if len(es) > 0 {
		fmt.Fprintln(w, "relations:")
		for _, e := range es {
			fmt.Fprintf(w, "  - type: %s\n    target: %s\n    reason: %s\n", e.Type, e.Target, e.Reason)
		}
	}
	if deprecated {
		// 失效卡指向**下一张**卡：链短、可复算，且让 replaced_by 正反查询在采样中真实发生。
		fmt.Fprintf(w, "replaced_by:\n  target: %s\n  reason: perf-replaced-%05d\n",
			queryset.CardID((i+1)%cards), i)
	}
	fmt.Fprintln(w, "---")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## 知识内容")
	fmt.Fprintln(w)
	// 正文：固定 6 行，每行含该卡的命中词一次 + 4 个填充词（填充词由 splitmix64 选，可复算）。
	for line := 0; line < 6; line++ {
		fmt.Fprintf(w, "- %s：", kw)
		for t := 0; t < 4; t++ {
			idx := int(splitmix64(seed+uint64(i)*17+uint64(line)*7+uint64(t)) % uint64(len(fillerTokens)))
			if t > 0 {
				fmt.Fprint(w, " / ")
			}
			fmt.Fprint(w, fillerTokens[idx])
		}
		fmt.Fprintf(w, "（%s 第 %d 段）\n", kw, line+1)
	}
	fmt.Fprintln(w)
	_ = w.Flush()
	return b.String()
}

// isoDate 返回语料的固定日期（`queryset.CorpusDate` 的 `YYYY-MM-DD` 形态）。
// 不取 time.Now()：取时钟就不可复算。
func isoDate() string {
	d := queryset.CorpusDate
	return d[0:4] + "-" + d[4:6] + "-" + d[6:8]
}

// splitmix64 是一个纯函数式的位混淆器（Steele et al. 2014）：把一个 uint64 打散成
// 另一个看起来随机、但完全可复算的 uint64。选它而不是 math/rand 的三条理由：
//
//	① 无状态 —— 第 i 张卡的取值不依赖前面生成了几张（并行/重排都不会变）；
//	② 无标准库版本依赖 —— math/rand 的序列在 Go 版本间可能变化，语料就不再逐字节可复算；
//	③ 实现只有五行，读代码的人可以直接验算。
func splitmix64(x uint64) uint64 {
	x += 0x9E37_79B9_7F4A_7C15
	z := x
	z = (z ^ (z >> 30)) * 0xBF58_476D_1CE4_E5B9
	z = (z ^ (z >> 27)) * 0x94D0_49BB_1331_11EB
	return z ^ (z >> 31)
}
