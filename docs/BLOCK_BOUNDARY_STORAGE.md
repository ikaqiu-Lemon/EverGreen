# EverGreen 分块边界存储选型：让边界内生于结构，哨兵符号退役

**核心判断**

1. **哨兵注释的病根是它在渲染视图里完全不可见**：用户在 Note 里改的是正文，而 `<!-- kb:begin -->` 这类标记在 Obsidian、GitHub 等阅读视图中不显示 [\[17\]](https://obsidian.md/help/obsidian-flavored-markdown)，一个看不见又没有语义的东西被误删或被正文挤到错位置，解析器无从察觉；TiddlyWiki 官方就为 CompoundTiddlers 自陈过同类代价——「不允许正文中出现单独一行的 `+`」[\[36\]](https://tiddlywiki.com/static/CompoundTiddlers.html)，把普通字符变成保留字的设计一定会反噬正文编辑，哨兵必须从主路径退役。

2. **第一否决项应当是「只改正文时边界还对不对」**：边界内生性与改正文鲁棒性两条一票否决，物化确定性、可视化可达性、改造代价三条做权衡；这套判据把 W3C 自认「very brittle with regards to changes to the resource」的字符偏移选择器 [\[44\]](https://www.w3.org/TR/annotation-model/#text-position-selector) 与需要四级回退才能重新锚定的 Hypothesis 方案 [\[45\]](https://web.hypothes.is/blog/fuzzy-anchoring/) 直接挡在权威层之外，剩下的候选才值得逐格比。

3. **边界的来源只有三类，第三类在纯文本仓库里走不通**：哨兵标记、结构本身、外挂选择器三选一；外挂族的锚点要么依赖 DOM（XPathSelector 对非 DOM 表示「Results are not defined」[\[44\]](https://www.w3.org/TR/annotation-model/#xpath-selector)），要么依赖 CRDT 文档本体——Yjs 的 RelativePosition 必须持 `Y.Doc` 才能还原绝对位置、否则返回 null [\[53\]](https://docs.yjs.dev/api/relative-positions)，锚点无法脱离运行时在 Git 仓库里独立存活。

4. **六个候选里四个过了否决项、只有一个扛住三条权衡项**：哨兵注释与围栏容器在否决项上出局或降级，org 子树、文件即块、块原生运行时三者分别败在生态代价、缺现成前端、纯文本不可 diff；标题小节是唯一在五条判据上都不亮红灯的选项，且 EverGreen 的 `indexSections` 已对 H2 建好全局 Span 索引、`BlockHeading3` 已是独立块型，改造代价最低。

5. **org 的三件机制值得照搬，org 这个格式不值得换**：subtree 边界由星号层级天然确定、`:ID:` 跨文件搬移仍有效 [\[23\]](https://orgmode.org/manual/Handling-Links.html)、`org-archive-subtree` 搬运时自动写入时间与 outline 路径 [\[21\]](https://orgmode.org/manual/Moving-subtrees.html)——三条都可以在 Markdown 上复刻；但 org 语法规范至今只是 Worg 上的 draft 且以 `org-element.el` 实现为准 [\[19\]](https://orgmode.org/manual/Org-Syntax.html)，Pandoc 3\.11 做 org→org 往返会整条丢弃 `:ID:` 并删掉 LOGBOOK，换格式的代价远大于收益。

6. **三级边界优先级加七条 CI 断言就能把这次选型锁死**：标题小节做主边界、围栏容器只在标题层级表达不了时兜底、哨兵注释降为迁移期只读；逻辑名 `k-20260918-agent-development-boundaries` 由标题属性承载并直接决定物化文件名，跨文件原子性用「先写临时文件再 rename \+ journal 回放」补齐，七条断言全部可进 CI，其中包含一条「一条命令剥掉全部容器语法仍不丢正文字节」的逃生舱验收。

---

## 一、需求重述：边界要内生于结构，不能写进正文

> **本节要点**：上一轮把「Markdown 权威 \+ HTML 注释哨兵 \+ 派生 sidecar」推成默认答案时，隐含假设是使用者会顺手维护标记位置。这个假设已被用户明确否掉——人只改正文。假设一撤，哨兵从资产变成负担，整个候选集必须按「边界从哪里来」重新排队。
>
>

### 1\.1 用户只改正文，哨兵标记从资产变负担

哨兵方案的全部确定性都押在一个前提上：区间的起止行始终在正确的位置。只要使用者愿意在改完正文后把 `<!-- kb:begin -->` 挪到新的段落之前，脚本侧的字节截取就是精确的；反过来，一旦这个前提撤掉，哨兵提供的不是确定性，而是一种会静默失效的确定性错觉。

失效的路径有三条，都不需要使用者犯错。其一，HTML 注释在阅读视图里被渲染器吞掉——Obsidian 官方的扩展语法清单里 `%%Text%%` 是注释语法，而标准 HTML 注释同样不出现在预览中 [\[17\]](https://obsidian.md/help/obsidian-flavored-markdown)；使用者在预览态改正文时，看不到自己正在跨越一条边界。其二，注释在源码态可见但没有语义提示，读起来像一行废弃代码，被整段选中删除的概率并不低。其三，即便注释还在，正文段落的增删也会改变「这段正文属于哪个候选」的语义：使用者在两条哨兵之间补了一段与该候选无关的内容，脚本仍会忠实地把它切进同一张卡片。

更根本的问题在于责任错配。哨兵把「维护边界」这件事外包给了唯一不打算做这件事的人。CriticMarkup 是正文内标记这一族里设计最克制的先例，它的三条法则写明「机器可读，且标记语法应能用简单正则解析」并强调无需专用工具 [\[50\]](http://criticmarkup.com/)；即便如此，MultiMarkdown\-6 官方文档仍指出 Markdown span 可能「start in the middle of a CriticMarkup structure, but end outside of it」，因此正确转换成 HTML 的算法「会相当复杂、边缘情况极多」[\[51\]](https://fletcher.github.io/MultiMarkdown-6/syntax/critic.html)。判断：正文内标记的脆弱性并非实现质量问题，而是「把结构信息塞进自由编辑区」这一选择的固有代价。

由此得到本轮的支点约束：分块边界必须能在使用者只做正文编辑的前提下自动保持正确。「Markdown \+ 一对哨兵符号 \+ 靠使用者把正文挪进边界之间」不再是推荐答案，只能作为迁移期兜底。

### 1\.2 边界只有三个来源：哨兵、结构、外挂选择器

把候选集抽象一层，任何「一篇 Note 里圈出一段候选内容」的方案，边界信息只可能来自三个地方。第一类写在正文内部，即哨兵标记与各种正文内轻量语法（HTML 注释、CriticMarkup 的 `{== ==}`、TiddlyWiki 的单独一行 `+`）。第二类由文本结构本身推导，即标题层级、围栏容器、缩进块、org subtree、文件与目录。第三类完全放在正文之外，由一份独立的选择器或锚点清单指向正文区间，W3C Web Annotation 的 Selector 族、TEI 的 `<standOff>`、STAM 的 radical stand\-off、以及 CRDT 的相对位置锚点都属于这一类。

三类来源在「使用者只改正文」这一步上的结局完全不同，这也是本轮全部结论的分水岭。

*图 1：「用户只改正文」这一步决定生死——哨兵被误删、外挂偏移漂移，只有结构容器原样存活（来源：W3C Web Annotation Data Model *[*\[44\]*](https://www.w3.org/TR/annotation-model/)* \+ Org Syntax *[*\[18\]*](https://orgmode.org/worg/org-syntax.html)* \+ 本轮 goldmark 实测）*

结构类之所以能过这一关，是因为边界不是「附加在正文上的东西」，而是正文本身被解析出来的形状。Org 官方语法文档给出的定义最直白：「All content following a heading — up to either the next heading, or the end of the document, forms a section」[\[18\]](https://orgmode.org/worg/org-syntax.html)——边界由层级天然确定，不需要任何闭合标记。Pandoc 的 `--section-divs` 走的是同一条推导：section 边界由 heading 层级算出来，只在 HTML 输出侧才物化成 `<section>` 标签，并把 identifier 挂到外层 section 而非 heading 自身 [\[3\]](https://pandoc.org/MANUAL.html#option--section-divs)。使用者在某个小节里删掉一段、补三段、把两段互换顺序，小节的起止仍由它上下两个标题行决定。

外挂类的失败点则被规范自己写在正文里。TextPositionSelector 以字符流偏移记录 `start`/`end`，规范紧接着注明该选择器「very brittle with regards to changes to the resource」，任何编辑或动态内容都可能改变选中范围，因此 RECOMMENDED 额外附带一个 State 来帮助识别正确的表示 [\[44\]](https://www.w3.org/TR/annotation-model/#text-position-selector)。TextQuoteSelector 用 exact 加 32 字符级别的 prefix/suffix 做消歧，但规范允许在无法唯一定位时退化为「匹配全部命中」[\[44\]](https://www.w3.org/TR/annotation-model/#text-quote-selector)——对需要确定性物化的场景，多命中等于失败。

三类来源里，第一类把维护责任推给不维护的人，第三类把确定性换成概率性重锚。本轮的方向由此收窄到第二类：让边界成为纯文本结构的一部分。

---

## 二、评估判据：改正文鲁棒性是第一否决项

> **本节要点**：判据分两层。两条否决项决定一个方案能不能进入候选池，三条权衡项决定进池之后谁胜出。把否决项与权衡项混在一张打分表里平均加权，是上一轮把哨兵推上默认位置的直接原因。
>
>

### 2\.1 两条否决项：边界内生性与改正文鲁棒性

**否决项一是边界内生性**：边界必须是文本结构被解析后的产物，不能是为了标边界而额外引入、且承载不了其他语义的标记。这条的判据很简单——把边界标记全部删掉，文档是否仍然是一篇结构完整、人读得懂的 Note？标题小节、org subtree、文件与目录都过得了：标题本来就要写，目录本来就要分。哨兵注释过不了：删掉之后文档一字不变，说明它只服务于机器，是纯附加物。围栏容器处在中间——`:::{#id .class}` 在 Pandoc、Quarto、MyST 里是有正式语义的 Div [\[1\]](https://pandoc.org/demo/example33/8.18-divs-and-spans.html) [\[4\]](https://quarto.org/docs/authoring/markdown-basics.html#sec-divs-and-spans) [\[5\]](https://myst-parser.readthedocs.io/en/latest/syntax/optional.html#syntax-colon-fence)，但在本项目的用法里它唯一的作用仍是圈边界，内生性只能算部分满足。

**否决项二是改正文鲁棒性**：使用者只做正文编辑（段落内改字、增删段落、段落重排），不移动也不维护任何边界，重新解析后每个候选块的身份与内容必须仍然正确。这条是本轮新加的一票否决，也是全篇的支点。它的判定有一个可操作的形式：能不能写成一条不变式断言，在随机扰动正文之后自动校验（第六章 6\.3 节给出具体断言）。

两条否决项一起用，效果是把「看起来能工作」和「在使用者不配合时仍能工作」区分开。哨兵注释在理想使用者下工作良好，在真实使用者下静默失效，因此在否决项层面就该出局，而不是在权衡表里扣几分。

### 2\.2 三条权衡项：物化确定性、可视化可达性、改造代价

过了否决项之后，三条权衡项决定最终排序。它们彼此有张力，不存在同时最优的选项，需要显式取舍。

**物化确定性**

把一个候选块变成 `cards/<id>.md` 时，是否有确定的、可重复、可校验的操作，而不是「大致截一段」。

最强形态是文件级 `git mv`；org 的 `org-refile` 与 `org-archive-subtree` 是子树级的现成命令 [\[20\]](https://orgmode.org/manual/Refile-and-Copy.html)。

**可视化可达性**

权威文本能否被现成前端读懂、渲染、甚至编辑，而不必先造一个专用查看器。

判据是「不支持这套语法的渲染器会显示成什么」——丢内容不可接受，显示为可见噪声可以接受。

**改造代价**

落到 EverGreen 现有代码上要动哪些位置，是扩展既有能力还是推翻核心模型。

关键约束是 `internal/mdfile` 的 `Doc` 模型建立在单一 `Raw` 字节切片上，不支持跨文件聚合。

*图 2：三条权衡项及其各自最强形态（来源：Org Manual §9\.1 *[*\[20\]*](https://orgmode.org/manual/Refile-and-Copy.html)* \+ EverGreen 源码审计 **`internal/mdfile/doc_index.go`**）*

物化确定性与可视化可达性之间的张力最明显。粒度越细、边界越硬的存储（一块一文件、块原生数据库），物化越确定，但能把碎片重新拼成一篇连贯笔记的现成前端越少；粒度越粗、越贴近普通 Markdown 的存储，前端生态越充足，但「精确截出哪一段」就越依赖自己实现的解析器。

改造代价这一条在本项目里权重被显式抬高，原因是用户已经给过约束：项目处于开发初期，上线后再换存储格式是最不希望出现的结果。这句话有两层含义——一层是现在就要选对，另一层是选的这套东西本身必须留有低成本退出的出口。后者比前者更重要，因为「选对」无法被证明，「能退出」可以被断言验证。第六章 6\.3 节因此把逃生舱做成 CI 里的一条硬指标，而不是一句承诺。

三条权衡项都不打分加总。它们的用法是逐条问「这个方案在这一条上会不会亮红灯」，红灯即出局，黄灯需要有明确的缓解手段。第四章的矩阵按这个口径逐格填满。

---

## 三、候选全景：按边界来源重排全部存储形态

> **本节要点**：结构内生这一类里，纯文本方案（标题小节、围栏容器、开放块、org 子树）与文件即块是两种不同硬度的边界；块原生运行时则把边界做成了一等实体，代价是权威数据不再是可 diff 的纯文本。三档各有一批已落地的先例，值得逐个看它们把边界安放在哪里。
>
>

### 3\.1 结构内生的纯文本：标题小节、围栏容器、开放块、org 子树

**标题小节是最便宜的一种结构边界，代价是 CommonMark 层面并不存在「小节」这个容器。**CommonMark 0\.31\.2 只定义两类 container block——block quote 与 list item，标题（ATX 与 Setext）都是 leaf block，规范层面不产生 section 容器 [\[7\]](https://spec.commonmark.org/0.31.2/#container-blocks)。也就是说「一个标题管到下一个同级标题之前」是一条需要自行约定的规则，不是解析器白送的。好消息是这条约定已被多方按同样方式实现：Pandoc 的 `--section-divs` 由 heading 层级推导 section 并把 identifier 挂到外层 [\[3\]](https://pandoc.org/MANUAL.html#option--section-divs)，org 的 section 定义与之同构 [\[18\]](https://orgmode.org/worg/org-syntax.html)。给小节挂稳定 ID 也有标准写法：Pandoc 的 `header_attributes` 允许在标题行尾写 `{#identifier .class key=value}`，与 PHP Markdown Extra 兼容 [\[2\]](https://pandoc.org/MANUAL.html#extension-header_attributes)；kramdown 用标题文本后的 `{#id}`，语法来源相同 [\[15\]](https://kramdown.gettalong.org/quickref.html#block-attributes)。

**围栏容器把边界变成显式的一对语法行，规范地位比标题属性更正式，但生态里同一串冒号有三种语义。**Pandoc 的 `fenced_divs` 规定 Div 以「至少 3 个连续冒号 \+ 属性」的围栏行开始、以另一行至少 3 个连续冒号结束，且围栏 Div 必须与前后块用空行分隔；属性语法完全等同于 fenced code block 的 `{#id .class key=value}`；开围栏靠「必须带属性」与闭围栏区分，不带属性的冒号行一律视为闭合 [\[1\]](https://pandoc.org/demo/example33/8.18-divs-and-spans.html)。Quarto 沿用同一套语法，并点出 Pandoc 的属性顺序约束——必须 identifier → classes → key\-value，`{.class key="val" #id}` 不被识别 [\[4\]](https://quarto.org/docs/authoring/markdown-basics.html#sec-divs-and-spans)。MyST 的 `:::` 是 directive 的冒号写法（`colon_fence` 扩展、默认关闭），语义是 `:::{note}` 这类指令，官方选冒号的理由被写成「在任何标准 Markdown 编辑器里也能正确渲染」[\[5\]](https://myst-parser.readthedocs.io/en/latest/syntax/optional.html#syntax-colon-fence)。markdown\-it\-container 则既无属性语法、又必须逐个注册容器名，默认渲染成 `<div class="{name}">` [\[6\]](https://github.com/markdown-it/markdown-it-container)。同一串 `:::` 在三个生态里分别是「带任意属性的 Div」「具名指令」「预注册的具名容器」，这意味着围栏容器的跨前端可移植性弱于它看起来的样子。

**AsciiDoc 提供了这一族里语义最干净的形态，可作为设计参照。**开放块 `--` 被官方称为「最通用的块」，一对分隔符给一段内容套上块属性而不赋予其他语义，即通用结构容器，唯一显著限制是开放块不能嵌套在另一个开放块里 [\[12\]](https://docs.asciidoctor.org/asciidoc/latest/blocks/open-blocks/)。块 ID 有三种等价写法（`[#goals]`、`[id=goals]`、`[[goals]]`），可挂在 section 标题、段落、图片、分隔块甚至行内短语上 [\[13\]](https://docs.asciidoctor.org/asciidoc/latest/attributes/id/)。最值得借用的是 AsciiDoc 对边界的概念划分：list、段落、块宏的边界是隐式的，delimited block 的边界用分隔符显式标注 [\[12\]](https://docs.asciidoctor.org/asciidoc/latest/blocks/)——这正是本项目要的两档结构（隐式的标题小节 \+ 显式的围栏兜底）在官方文档里的表述。rST 的 `container` 指令走的是第三条路，边界靠缩进：指令块「从 directive marker 之后开始，包含其后所有缩进行」[\[14\]](https://docutils.sourceforge.io/docs/ref/rst/directives.html#container)；缩进型边界对「使用者只改正文」并不友好，段落重排时缩进极易被破坏。djot 则把两件事拆开——围栏行只允许 class、块属性单独写在块前一行的 `{#water}`，且围栏可在文档或父块结束时自动闭合 [\[16\]](https://github.com/jgm/djot/blob/main/doc/syntax.md)。

**org subtree 是这一族里唯一自带完整元数据与物化命令的现成形态。**标题的星号个数定义层级，标题之后到下一个标题或文档结束构成一个 section [\[18\]](https://orgmode.org/worg/org-syntax.html)；property drawer 必须紧跟标题（及 planning 行）之后，内部是 `:NAME: VALUE` 形式的 node property、中间不得有空行 [\[22\]](https://orgmode.org/manual/Property-Syntax.html)。`:CUSTOM_ID:` 是人读锚点、可用 `[[#my-custom-id]]` 精确指向；`:ID:` 是全局唯一 ID，官方明确「works even if the entry is moved from file to file」[\[23\]](https://orgmode.org/manual/Handling-Links.html)。Go 侧也有对应的数据结构：go\-org 的 `Headline` 结构体直接带 `Lvl`、`Properties *PropertyDrawer`、`Children []Node`，另有 `Outline` 与 `Section{Headline, Parent, Children}` 树，`Headline.ID()` 优先取 `CUSTOM_ID`、否则退回 `headline-<index>` [\[26\]](https://github.com/niklasfasching/go-org/blob/master/org/headline.go)。org 的生态代价放在第五章 5\.1 节单独拆。

这一族里还有一条与「包围区间」正交的路线值得记下：给单个块挂 ID，而不是圈出一段区间。MyST 的 `attrs_block` 允许在块前单独一行写 `{#mypara .bg-warning}` 给普通段落挂 id/class 并可被 `{ref}` 引用 [\[5\]](https://myst-parser.readthedocs.io/en/latest/syntax/optional.html#syntax-attributes-block)；kramdown 的块 IAL 是块后紧跟一行 `{: #with-an-id}` [\[15\]](https://kramdown.gettalong.org/quickref.html#block-attributes)；Obsidian 原生的 `^id` 定义块、`![[Link#^id]]` 引用块 [\[17\]](https://obsidian.md/help/obsidian-flavored-markdown)；gomarkdown 的 `parser.Attributes` 扩展允许在块前单独一行写 `{#id3 .myclass fontsize="tiny"}`，官方同时警告该扩展可注入任意属性、勿用于不可信输入 [\[11\]](https://github.com/gomarkdown/markdown#extensions)。这条路线的问题是候选块通常不止一个段落，「给一个块挂 ID」表达不了「这三段合起来是一张卡片」，所以它只能作为标题小节的补充，不能替代。

### 3\.2 文件即块：Note bundle、tiddler、outliner 三种拆法

**把边界推到文件系统上，是全部候选里边界最硬的一种：文件边界不可能被正文编辑破坏。**代价则集中在另一头——把一堆文件重新拼成一篇连贯的 Note，需要一个现成前端，而这个前端目前不存在于笔记生态的主干上。

静态站生态提供了「一个目录 = 一篇内容」的最成熟先例。Hugo 的 page bundle 用目录同时封装内容与资源：含 `index.md` 的目录是 leaf bundle、含 `_index.md` 的是 branch bundle；leaf bundle 内除 index 外的其他 `.md` 是 resource type 为 `page` 的 page resource，Hugo 不会把它们渲染成独立页面，只能被主文件组装 [\[33\]](https://gohugo.io/content-management/page-bundles/)。这正是本项目要的语义——碎片只用于被组装、本身不成页，Hugo 的 headless bundle 更是为此专设。Jekyll 的 collections 给出了顺序问题的官方答案：collection 目录里每个带 front matter 的文件是一个 document，`output: true` 才各自成页，否则只能通过 `site.<collection>` 迭代拼进别的页面；顺序支持 `order` 元数据按文件名列表手工指定（可含子目录路径），与 `sort_by` 同时存在时 `order` 优先 [\[34\]](https://jekyllrb.com/docs/collections/)。这是「目录 \+ 顺序清单 = 一篇」最贴近的官方机制。

TiddlyWiki 是「文件即块」的最彻底形态，也把这条路的两个坑标得最清楚。Node\.js 版的核心收益之一被官方写成「Individual tiddlers are stored in separate files, which you can organise as you wish」；`.tid` 文件格式是文件头若干 `name: value` 行 \+ 一个空行 \+ 正文，单个 tiddler 文件还可配套 `.meta` 辅助文件承载元数据 [\[37\]](https://tiddlywiki.com/static/TiddlerFiles.html)——「正文纯净 \+ 元数据外置」在这里有现成格式。但官方同时给出一条硬 Warning：Node\.js 版不支持在运行期通过文件系统直接修改 tiddler 文件，改完必须重启服务，推荐通过 HTTP 或 JavaScript API 交互 [\[35\]](https://tiddlywiki.com/static/TiddlyWiki%20on%20Node.js.html)。对一个「CLI 与外部编辑器共写同一批文件」的项目，这条限制等于宣告该运行时不能作为权威层。CompoundTiddlers 那条自陈缺陷——「格式极简，但不允许正文中出现单独一行的 `+`」[\[36\]](https://tiddlywiki.com/static/CompoundTiddlers.html)——则是任何「用正文里的特殊行做分隔符」方案的反面先例，正好覆盖哨兵路线。

Dendron 把「物化 = 改名」这件事做到了设计层面。层级用 `.` 分隔的文件名表达（`project1.tasks.task1.md`），官方给出三条理由：让每个文件同时是文件夹（节点既有内容又有子节点）、把重构变成单纯 rename（文件夹方案需先把子节点变目录再造一个承载原内容的文件）、可为不存在的层级留 stub 而不产生空文件夹 [\[71\]](https://raw.githubusercontent.com/dendronhq/dendron-site/master/vault/dendron.topic.hierarchies.md)。第二条正是本项目物化出口想要的确定性。但 Dendron 的现状是一条明确的选型风险信号：README 顶部写着「Dendron is currently in maintenance only, active development has ceased\.」[\[72\]](https://github.com/dendronhq/dendron)。它的 transclusion 能力也有官方明示的硬限制——嵌套引用仅支持两层，Note Preview 链式引用解析上限 3 [\[73\]](https://wiki.dendron.so/notes/f1af56bb-db27-47ae-8406-61a98de6c78c/)，深层组装不可靠。Zim 的目录树拆法则暴露另一个副作用：删除 page 会连带 subpages 与 attachments 一起进回收站，因为「page 对应文本文件所在文件夹下的所有文件」，恢复后还需 Re\-build Index [\[43\]](https://zim-wiki.org/manual/Help/Pages.html)。

**把目录拼回一篇笔记的现成前端，目前只有两个 Obsidian 社区插件，且体量都很小。**Feuillets 的 Continuous 模式把一个文件、文件夹、选区或整个项目组装成一个连续编辑器，sheet 边界可见且受保护，编辑被回写分发到对应源 Markdown 文件，磁盘上不生成合成稿；它的边界保护取向与本项目诉求高度一致——Reorder text 只允许在单个 sheet 内移动段落，不允许跨 sheet 边界 [\[74\]](https://community.obsidian.md/plugins/feuillets)。Feuillets 同时给出 manifest 必要性的先例：结构与移动由 Binder 负责，Scrivener 导入时「Binder order 被显式持久化，独立于 vault 字母序」，即文件名序不够用 [\[74\]](https://community.obsidian.md/plugins/feuillets)。Folder Reader 是只读向的最小实现：右键文件夹后把目录直属 `.md` 按文件名序（数字感知）依次渲染为一页长文档，用 Obsidian 内置 `MarkdownRenderer` 逐文件渲染、IntersectionObserver 按 15 篇一批懒渲染，只含目录直属文件、不递归子目录 [\[75\]](https://community.obsidian.md/plugins/folder-reader)。两者都是维护者自述、本轮未实操核验，且 Feuillets 约 1k downloads、Folder Reader 约 294 downloads，作为长期依赖的基础设施偏薄。

Git 侧的收益与代价同样要按官方口径说清。文件级重命名是 blame 的舒适区：blame 自动跨整文件重命名追溯行的来源，且目前无法关闭 [\[76\]](https://git-scm.com/docs/git-blame)。但把一段正文从 Note 切进另一个文件后，追这段行的来源必须显式加 `-C`，而跨文件检测默认下限是 40 个字母数字字符（同文件内 `-M` 的下限是 20）[\[76\]](https://git-scm.com/docs/git-blame)——短块被切出去后，blame 会断链。至于「大量小文件对索引与文件系统监听的量化影响」，官方对 `git status` 只给了定性口径：在大工作树上「can be very slow if/when it needs to search for untracked files and directories」，需靠多项配置加速，且「没有一套对所有人最优的设置」[\[77\]](https://git-scm.com/docs/git-status/2.40.0)；FSMonitor 的效果只有厂商工程博客的自报（三个不同大型工作树的 status 时间降到 1 秒以内，测试环境与仓库规模未给出）[\[78\]](https://github.blog/engineering/infrastructure/improve-git-monorepo-performance-with-a-file-system-monitor/)。本轮未找到公开量化数据来回答「这个项目的规模下，一块一文件会让编辑器与 Git 慢多少」。

### 3\.3 块原生运行时：块树 JSON、SQLite、CRDT 各自解决了什么

**块原生运行时把边界做成了一等实体，代价是权威数据不再是可 diff、可 git blame 的纯文本。**这一族的五个样本在「权威数据放哪里」上各有答案，但在「Markdown 只是导出通道」这一点上高度一致。

Anytype 把数据先全量存本地再同步，底层用私有 IPFS 网络，媒体文件以加密分片存放，官方明示不可在 Anytype 之外访问 [\[58\]](https://doc.anytype.io/anytype/data/storage)。导出只有 Markdown、Any\-Block、PDF、HTML 四种，其中 Any\-Block 可选 JSON 或 Protobuf，官方原话是「Protobuf is not human\-readable, but it usually delivers the best results」[\[59\]](https://doc.anytype.io/anytype/data/import-and-export.md)——最高保真的通道恰好是不可读的那个。AFFiNE 的 BlockSuite 更直接：官方说明服务端存的文档数据「no longer JSON, but always a binary representation of CRDT」，block tree 状态完全由 CRDT 数据驱动；Snapshot API 导出的 block tree JSON 是旁路，Markdown 等第三方格式由建在 snapshot 之上的 Adapter 负责互转 [\[60\]](https://blocksuite.io/guide/data-synchronization)。落盘接口也印证这一点——nbstore 的 doc storage API 按 `pushDocUpdate({ docId, bin: Uint8Array, editor })` 组织，磁盘上是二进制 blob [\[61\]](https://github.com/toeverything/AFFiNE/blob/canary/packages/common/nbstore/README.md)。

Notion 的块模型最成熟，往返保真的官方口径也最坦白：整页 database 导出为 CSV 加每行子页各一个 `.md`，callout 块因「Markdown 无等价物」导出为 HTML，导出 database 时只能选 current view 或 default view、不支持一次导出全部视图；最关键的一句是「You can't instantly recreate your workspace by reuploading your exported workspace content」[\[62\]](https://www.notion.com/help/export-your-content)——官方直接承认不支持往返。

Trilium 的价值在于它把「为什么块原生要选数据库」写成了一份逐条判据，而这份判据正好是本项目的反方论据。权威数据是单个 SQLite `document.db`；维护者对「为什么不用 flat files」给出六条理由：clone 相当于「hard directory link」而任何文件系统都没有、Trilium 刻意不区分目录与文件、文件系统不保序且用户无法控制顺序、note attributes 只能映射到 extended user attributes 且跨系统支持差异大、notes 之间的 links/relations 需要快速检索否则得靠 side\-car 迷你数据库、文件系统非事务性难以保证 note 与 metadata 一致 [\[63\]](https://docs.triliumnotes.org/user-guide/faq)。同一份 FAQ 也明确承认 flat files「easily interoperable, work with SCM/git」。DB 权威形态的直接代价写在官方警告里：不能用 Dropbox、Google Drive、OneDrive 同步，会损坏数据库并报 `SqliteError: database disk image is malformed`，唯一受支持的方式是自建 sync/web server [\[63\]](https://docs.triliumnotes.org/user-guide/faq)。Joplin 的答案更短：同步目录用的是开放格式但「not meant to be user\-editable」，「Joplin sync directory is basically just a database」[\[65\]](https://joplinapp.org/help/faq/)。SiYuan 侧同样明确不支持第三方同步盘、「One workspace, one writer」，且 Docker/K8s 部署下不支持导入 Markdown 文件 [\[69\]](https://github.com/siyuan-note/siyuan/blob/master/README.md)。

**「纯文本落盘 \+ 块运行时双向同步」这条混合路线，本轮未找到已交付的成功先例。**最直接的对标是 Logseq DB 版的 Markdown Mirror：一个只读的磁盘 Markdown 投影，随编辑更新、按 graph 选择开启、处理路径冲突与重命名清理，把 block id 以注释形式嵌入文件；维护者 changelog 同时写明「Two\-way sync is in development on a feature branch」[\[66\]](https://discuss.logseq.com/t/logseq-db-changelog/30013/37)。官方文档对导出口径的自陈更直白：`Export as standard Markdown (no block properties)`「unlikely to ever export timestamps or all properties」且「cannot capture all data in a graph」，唯一完整且可编辑的导出是 EDN，而官方又标注它「not yet recommended as the only means to backup a graph」[\[67\]](https://github.com/logseq/docs/blob/master/db-version.md)。公开路线图把这条路线的未解问题列成待办清单：导出 DB graph 到 Markdown、多客户端改同一块内容时呈现冲突、Markdown mirror、Markdown 与 DB graph 的双向同步、隐藏裸块 ID、让 inline properties 成为可能、用 CRDT 自动解决块内容冲突 [\[68\]](https://logseq.io/page/3bc00ad3-f421-41e7-8c65-40861c298be5/6954ee2a-506b-4dd9-bd6d-0dc24db9c055)。判断：一个有全职团队、已用 50k pages / 1M\+ blocks 做过测试的项目，把这些问题公开列为未解，说明混合路线的工程量级远超一个个人项目的预算。

---

## 四、逐项对比：否决项筛掉两个，权衡项筛掉三个

> **本节要点**：七个候选按两条否决项先过一遍，哨兵注释出局、围栏容器降级；剩下的四个再过三条权衡项，org 子树败在生态代价、文件即块败在缺现成前端、块原生运行时败在权威数据不可 diff。唯一在五条判据上都不亮红灯的是标题小节。
>
>

### 4\.1 方案 × 判据：七个候选逐格填满

符号口径全篇固定：✅ 满足、🟡 部分满足或需缓解手段、🔴 不满足。判据定义见第二章。

|候选（边界来源）|边界内生性|改正文鲁棒性|物化确定性|可视化可达性|改造代价|结论|
|---|---|---|---|---|---|---|
|哨兵注释<br>（正文内标记）|🔴 删掉后文档一字不变，纯附加物|🔴 渲染视图不可见，易被连带删除或挤错位|✅ 起止行明确，字节截取精确|✅ 任何渲染器都当注释忽略|✅ 已实现，但需扩展 `SplitBlocks` 处理跨行注释闭合|🔴 否决项双红，仅保留迁移期只读|
|标题小节<br>（H3 小节 \+ 标题属性）|✅ 标题本来就要写，删掉边界文档即残缺|✅ 边界由上下两个标题行决定，改段落不触碰|🟡 需自行实现小节区间截取，可复用 `Unprocessed.Cut`|✅ 任何渲染器都渲染成标题；`{#id}` 在 CommonMark 下留为字面文字|✅ `indexSections` 已建 H2 全局 Span 索引，`BlockHeading3` 已是独立块型|✅ 主边界|
|围栏容器<br>（`:::{#id .class}`）|🟡 有正式 Div 语义，但本项目里只用于圈边界|✅ 围栏行独立成行，正文编辑不经过它|✅ 一对围栏行给出显式区间|🟡 goldmark 核心库无 fenced div；GitHub 渲染 API 与 markdown\-it 实测把冒号行原样显示为正文|🟡 goldmark 需挂第三方扩展（11 stars、2023\-01 停更）或自实现最小扫描器|🟡 兜底边界|
|org 子树<br>（星号层级 \+ PROPERTIES）|✅ 标题之后到下一标题即 section，规范定义|✅ 层级天然定界，无闭合标记|✅ `org-refile` / `org-archive-subtree` 是现成命令，搬运时自动写入上下文属性|🔴 VS Code 头部扩展已从商店下架；Obsidian 内无法新建非 md 文件；GitHub 渲染后端 2024\-08 后停更|🔴 权威格式整体更换，且 Pandoc org→org 会丢 `:ID:` 与 LOGBOOK|🔴 机制借鉴，格式不换|
|文件即块<br>（一块一文件 \+ manifest）|✅ 文件边界即块边界，最硬|✅ 正文编辑不可能跨文件，边界绝对稳定|✅ 物化近似 `git mv`，Dendron 官方把「重构变成单纯 rename」列为设计理由|🔴 能把目录拼成一篇的现成前端只有两个小体量 Obsidian 插件（约 1k 与 294 downloads）且均未实操核验|🔴 迫使 `internal/mdfile` 放弃单一 `Raw` 字节切片模型（`Doc` 不支持跨文件聚合）|🔴 仅作物化出口，不作权威层|
|块原生运行时<br>（块树 JSON / SQLite / CRDT）|✅ 块是一等实体，自带稳定 ID|✅ 编辑经由运行时，边界由数据结构保证|✅ 块可寻址，搬移是数据操作|🔴 权威数据是加密分片、CRDT 二进制或 SQLite；Notion 官方明示导出不可回灌|🔴 纯文本落盘 \+ 双向同步在 Logseq DB 仍是 feature branch 上的未交付项|🔴 出局|
|外挂选择器 / CRDT 锚点<br>（边界在正文之外）|✅ 正文完全不含标记|🔴 字符偏移被规范自认 very brittle；重锚是概率性匹配|🔴 无法保证唯一命中，规范允许退化为「匹配全部命中」|🟡 正文是干净纯文本，但区间信息对任何前端都不可见|🔴 需自建 dom\-text 映射与模糊匹配层；CRDT 锚点必须持有对应文档才能解析|🔴 仅可用于派生层|

*表 1：七个候选 × 五条判据全格填满——过否决项的四个里，只有标题小节在权衡项上不亮红灯（来源：本轮 brief\_01–brief\_06，含 goldmark v1\.8\.6 本地实测与 EverGreen 源码审计）*

### 4\.2 出局理由：两条否决项与三条权衡项各筛掉了谁

**否决项筛掉的是哨兵注释，降级的是围栏容器。**哨兵在两条否决项上同时亮红：它在内生性上是纯附加物，在鲁棒性上又依赖使用者维护——恰好是本轮被撤掉的那个假设。值得强调的是，哨兵在其余三条上表现相当好（物化确定性与可视化可达性都是绿的），这正是上一轮把它推成默认答案的原因。判据分层的价值在此显现：一个方案在三条权衡项上全绿，也不能弥补它在否决项上的双红。围栏容器则是因为内生性只算部分满足、可视化可达性与改造代价各带一处黄灯，被定位为兜底而非主路径。

**权衡项筛掉的是 org 子树、文件即块、块原生运行时三者，各自败在不同的一条上。**org 子树在前三条判据上全绿，是纸面上最完整的答案——边界由层级定义、有全局唯一 ID、有现成的确定性搬运命令。它倒在后两条：可视化可达性与改造代价同时亮红，因为换的是权威格式本身。文件即块在前三条同样全绿，且物化确定性比 org 更硬（文件 rename 是文件系统原语），但把碎片重新组装成一篇可读 Note 没有可依赖的现成前端，加上 EverGreen 的 `Doc` 模型建立在单一 `Raw` 字节切片上、不支持跨文件聚合，这条路要先推翻核心数据模型。块原生运行时的红灯最集中：权威数据形态与「Markdown 是权威、Git 可评审」这条前置约束直接冲突。

标题小节是唯一没有红灯的一列，代价集中在一处黄灯——物化确定性需要自行实现小节区间截取，因为 CommonMark 层面不存在 section 容器。这处黄灯有明确的缓解手段：EverGreen 的 `Unprocessed.Cut` 已提供定位、字节剪除与自检的完整路径，`ReplaceBlock` 与 `ReplaceSectionBody` 已实现基于偏移的无损替换，`indexSections` 已对 H2 建立全局 Span 索引，把索引粒度下推到 H3 是扩展而非重写。判断：这一格从「需要自己写解析」降级为「需要把已有能力复用一层」，是全部候选里代价最小的一处让步。

---

## 五、关键差异：org 的机制可借鉴，格式不必换

> **本节要点**：org 把「层级定界 \+ 稳定 ID \+ 确定性搬运」三件事做成了现成能力，这三件事都可以在 Markdown 上复刻；换成 org 格式要付的却是全套生态代价。围栏容器与标题小节的分工由 Go 侧实测决定，外挂锚点则因为无法脱离运行时持久化而只能留在派生层。
>
>

### 5\.1 org\-refile 就是现成的确定性物化命令

**org 在「把一段内容确定性地搬到别处」这件事上，提供的是命令级而非思路级的答案。**`org-refile`（`C-c C-w`）把 point 所在的 entry 或区域移到交互选定的目标标题之下，作为 sub\-item 归档，插入首位还是末位由 `org-reverse-note-order` 决定，跨文件目标由 `org-refile-targets` 配置 [\[20\]](https://orgmode.org/manual/Refile-and-Copy.html)。`org-archive-subtree`（`C-c C-x C-s`）把整棵子树搬到 `org-archive-location`，默认是同目录同名文件加 `_archive` 后缀 [\[21\]](https://orgmode.org/manual/Moving-subtrees.html)。这两个命令的底层是 `org-cut-subtree` 与 `org-paste-subtree`，后者在粘贴时会自动调整子树层级以适配落点 [\[79\]](https://orgmode.org/manual/Structure-Editing.html)。

**最值得照搬的是它的「搬运即补元数据」。**官方写明：子树被移动时会获得一批特殊属性，记录 entry 来自哪个文件、它的 outline 路径、归档时间等上下文信息，具体项由 `org-archive-save-context-info` 控制 [\[21\]](https://orgmode.org/manual/Moving-subtrees.html)；源码里这个变量的默认值是 `(time file olpath category todo itags)`，`org-archive-to-archive-sibling` 的实现路径是「剪切子树 → 重贴并调层级 → 盖 `ARCHIVE_TIME` 属性」[\[24\]](https://git.savannah.gnu.org/cgit/emacs/org-mode.git/plain/lisp/org-archive.el)。这套默认集合可以原封不动搬进本项目的物化 op：物化时间、源 Note 路径、标题路径、标签——第六章 6\.1 节的 front matter 字段直接按它对齐。ID 侧同样有现成语义：`:ID:` 是全局唯一 ID，官方明确「works even if the entry is moved from file to file」，`org-id-get-create` 的行为是无则创建、有则复用 [\[23\]](https://orgmode.org/manual/Handling-Links.html)——这正是物化幂等所需要的语义。

org 还有两条需要避开的坑，同样以官方口径写明。`org-refile-copy` 与 `org-refile-keep` 保留原件，官方直接警告「this may result in duplicated 'ID' properties」[\[20\]](https://orgmode.org/manual/Refile-and-Copy.html)——复制语义会破坏 ID 唯一性，本项目的物化因此必须是移动而非复制。org\-transclusion 提供了「原子笔记 \+ 引用组装」的另一条路，源改后刷新即同步、文件里只留链接 [\[31\]](https://nobiot.github.io/org-transclusion/)，但它与 refile 式物化不兼容：官方 Known Limitations 写明「Org refile does not work 'properly' on the transcluded headlines」，作者不打算支持，要求 refile 源而非副本 [\[31\]](https://nobiot.github.io/org-transclusion/)。org\-roam 的定位则给了另一条可借鉴的边界：「notes are first and foremost plain Org\-mode files – Org\-roam simply builds an auxiliary database」，且「The notes are still functional even if Org\-roam ceases to exist」[\[32\]](https://www.orgroam.com/manual.html)——纯文本是权威、数据库只是辅助索引，这条正是本项目派生层的设计原则。

**把 org 作为权威格式的代价则集中在规范地位与生态两处，且两处都在恶化。**规范侧：官方手册说明形式语法参考文档「is available as a draft on Worg」[\[19\]](https://orgmode.org/manual/Org-Syntax.html)；该文档自述它描述的是「Org syntax as it is currently read by its parser \(org\-element\.el\)」，即以 Emacs 实现为准而非实现以规范为准，且语法文档大量以 Emacs Lisp 变量为参数、专设「References to lisp variables」一节 [\[18\]](https://orgmode.org/worg/org-syntax.html)。对一个用 Go 实现解析的项目，这意味着语法的部分判定依赖运行期 Emacs 配置。Go 侧唯一可用的解析器 go\-org 自述目标是「support a reasonable subset of Org mode」并遵循 80/20 原则 [\[25\]](https://github.com/niklasfasching/go-org)；它 400 stars、最后 push 2026\-07\-03，活着但低频。

生态侧的取证更直接。VS Code 星数最高的 org 扩展仓库最后 push 2024\-04\-13、近两年半无提交 [\[27\]](https://github.com/vscode-org-mode/vscode-org-mode)，其 README 自述的 Marketplace 地址现返回 HTTP 404、按 extensionName 精确查询返回 0 条——已无法从商店安装。Obsidian 侧的 org 支持只有社区插件，官方商店 7806 个插件中命中 2 个；插件 README 自陈两条硬摩擦：默认侧边栏不显示 org 文件、需勾选「Detect all file extensions」，以及「To create an org file in your vault, you currently have to create it outside obsidian」[\[28\]](https://github.com/BBazard/obsidian-orgmode-cm6)。GitHub 网页渲染 `.org` 走 `github-markup` 管线、底层依赖 Ruby gem `org-ruby` [\[29\]](https://github.com/github/markup)，而该 gem 最后 push 2024\-08\-14 [\[30\]](https://github.com/wallyqs/org-ruby)——GitHub 的 org 渲染后端已约两年无更新。

往返保真的实测把最后一块拼上。go\-org 本身保真良好：输入含 `:ID:`、`:CUSTOM_ID:`、`:CREATED:` 的子树，`go-org render sample.org org` 输出与输入逐字节一致，含 `SCHEDULED:` planning 行与 `:LOGBOOK:` CLOCK 行的例子同样原样保留。但 Pandoc 3\.11（当前最新 release，2026\-08\-29 发布 [\[70\]](https://github.com/jgm/pandoc/releases/latest)）做 org→org 往返时：`:ID:` 被整条丢弃、`:CREATED:` 被降为小写、所有标题被自动注入原文不存在的 `:CUSTOM_ID:`；更严重的一例里 `SCHEDULED:` planning 行与整个 `:LOGBOOK:` 被删除，原 `:ID:` 被改写成 `:CUSTOM_ID:`，语义被篡改。org→gfm 则让全部 PROPERTIES drawer、ID、planning、LOGBOOK 消失，只剩标题与正文。

结论明确：**抄机制，不换格式**。要从 org 拿走的是三条——层级定边界、标题级稳定 ID、搬运时自动补上下文属性；要留下的是 org 的语法方言与它的生态负担。这三条在 Markdown 上都有对应的现成语法（`header_attributes` 的 `{#id}`、front matter 承载上下文、小节区间截取），代价是最后一条要自己实现，而这正好落在 EverGreen 已有能力的延长线上。

### 5\.2 围栏容器与标题小节把边界变成语法结构

**围栏容器与标题小节的分工，由 Go 侧的实测能力边界决定，而不是由语法优雅程度决定。**本轮在 goldmark v1\.8\.6 \+ goldmark\-fences v1\.0\.0（Go 1\.24\.13）上做了三组实测，结果把两者的成本差拉得很开。第一组：纯 goldmark（CommonMark）把 `:::{#card-1 .kb-candidate}` 原样输出为段落文字 `<p>:::{#card-1 .kb-candidate}...`。第二组：加上 `parser.WithAttribute()` 后，`## My heading {#foo}` 正确输出 `<h2 id="foo">My heading</h2>`，但 `:::` 仍是文字。第三组：加上 `&fences.Extender{}` 后才得到 `<div data-fence="0" id="card-1" class="kb-candidate">`，内部段落正常解析。

这三组结果对应的官方口径是一致的：goldmark README 明确「目前只有 heading 支持属性」，核心库无 fenced div，fenced div 只能靠第三方扩展 [\[9\]](https://github.com/yuin/goldmark)。而那个扩展的生态体量是一个明确的维护风险信号——`goldmark-fences` 11 stars、2 forks、最后 push 2023\-01\-16 [\[10\]](https://api.github.com/repos/stefanfritsch/goldmark-fences)，另一个实现 `nemunaire/goldmark-fenced_divs` 只有 1 star。换言之：标题属性是 goldmark 的一等能力，围栏容器需要押注一个停更两年半的小仓库，或者自己写一个最小扫描器。

**还有一个实现细节直接决定「改正文不破坏边界」能不能落地。**goldmark 的设计目标即「AST\-based; preserves source position of nodes」，节点不存文本、只存 `text.Segment` 的 `Start`/`End`/`Padding` [\[9\]](https://github.com/yuin/goldmark)；本轮实测确认块级节点可直接拿到字节区间（`Paragraph startByte=27 endByte=37`、`Heading 60–70` 之类）。但 goldmark\-fences 的 `FencedContainer` 节点没有 line segments、`Lines()` 为空，容器整体区间必须由子节点或围栏行自行推导。也就是说围栏容器在 Go 侧不仅要多挂一个扩展，还要额外写一段区间推导代码；标题小节的区间反而可以从 heading 节点的 Segment 与下一个 heading 的 Segment 直接算出来。

**降级表现这一条，两者的差别是「不可见」与「可见噪声」。**本轮实测了两个通用渲染器：GitHub 官方 Markdown 渲染 API（`POST https://api.github.com/markdown`，`mode=markdown`）对 `:::{#card-1 .kb-candidate}\nBody text.\n\nSecond para.\n:::` 返回的是 `<p>:::{#card-1 .kb-candidate}\nBody text.</p><p>Second para.\n:::</p>`——冒号围栏与属性被当普通正文原样显示，既不生成 div、也不被吞掉；markdown\-it\-py 4\.2\.0（commonmark preset）给出同样结果，且 `## My heading {#foo}` 输出 `<h2>My heading {#foo}</h2>`，`{#foo}` 作为字面文字留在标题里。Obsidian 官方的支持范围声明是 CommonMark \+ GFM \+ LaTeX，扩展语法表里不含 `:::` 容器，也不含标题或块属性语法 [\[17\]](https://obsidian.md/help/obsidian-flavored-markdown)；GFM 规范同样不含 fenced div 与标题属性，其扩展只有 tables、task list items、strikethrough、autolinks、disallowed raw HTML [\[8\]](https://github.github.com/gfm/)。

这些结果指向同一个判断：两种语法在不支持的渲染器里都不会丢正文，这是可接受的底线；但围栏方案会让每张卡片的上下各多出一行可见的冒号噪声，而标题属性只在标题末尾留一个 `{#id}`，视觉侵入小一个量级。另有一条直接反证把「退回裸 HTML `<div>` 包区间」这条路也堵死：Obsidian 不在 HTML 元素内部解析 Markdown，`<div>` 内的 `**bold**` 不生效，官方称这是为性能与解析复杂度所做的有意设计 [\[17\]](https://obsidian.md/help/obsidian-flavored-markdown)——用 div 包住候选块会让块内正文在 Obsidian 里整段失去格式。

由此定下的分工是：标题小节承担全部常规候选块，围栏容器只在标题层级表达不了的情形下出场（一个小节内需要圈出多个互不相邻的候选、或候选跨越了标题结构）。Pandoc 的嵌套规则在这里是有用的保障——开围栏靠「必须带属性」来区分，不带属性的冒号行一律视为闭合，闭合围栏的冒号数不必与开围栏相同 [\[1\]](https://pandoc.org/demo/example33/8.18-divs-and-spans.html)；这条规则让自实现的最小扫描器可以只认「行首 ≥3 冒号 \+ 属性」为开、「行首 ≥3 冒号且无属性」为闭，不做嵌套，解析逻辑足够短到可以完全掌控。

### 5\.3 外挂选择器与 CRDT 锚点为何不进权威层

**把边界完全放到正文之外，是三类来源里唯一能让正文保持绝对纯净的方案，也是唯一无法给出确定性的方案。**四条独立证据指向同一个结论，且每条都来自规范或官方实现自身的陈述。

*图 3：四条来自规范与官方实现自身的证据，共同指向「边界必须内生于纯文本结构」（来源：W3C Web Annotation Data Model *[*\[44\]*](https://www.w3.org/TR/annotation-model/)* \+ Hypothesis *[*\[45\]*](https://web.hypothes.is/blog/fuzzy-anchoring/)* \+ Yjs *[*\[53\]*](https://docs.yjs.dev/api/relative-positions)* \+ Logseq 公开路线图 *[*\[68\]*](https://logseq.io/page/3bc00ad3-f421-41e7-8c65-40861c298be5/6954ee2a-506b-4dd9-bd6d-0dc24db9c055)*）*

**第一条，字符偏移的脆弱性由规范自己写明。**TextPositionSelector 用 `start`/`end` 记录字符流偏移，规范紧接着注明该方式「very brittle with regards to changes to the resource」，并 RECOMMENDED 额外附带 State 来识别正确的表示 [\[44\]](https://www.w3.org/TR/annotation-model/#text-position-selector)。RangeSelector 的存在动机本身就是承认单一选择器不够用——规范原话是用户的选择可能很长或跨越内部边界，「making it difficult to construct a single selector that robustly describes the correct content」[\[44\]](https://www.w3.org/TR/annotation-model/#range-selector)。偏移口径还依赖归一化实现一致：规范要求文本必须先按与 TextQuoteSelector 相同的方式归一化再计数字符 [\[44\]](https://www.w3.org/TR/annotation-model/#text-position-selector)。XPathSelector 更是直接不适用——它只对符合 DOM 的表示有定义，「Results are not defined for when an XPath Selector is applied to a representation that does not conform to the DOM」[\[44\]](https://www.w3.org/TR/annotation-model/#xpath-selector)，而本项目的权威层是纯文本文件，没有 DOM。

**第二条，工业界最成熟的实现用四级回退换来的仍是概率性命中。**Hypothesis 早期方案是 XPath range 加元素内字符 offset，官方自述「works if the content is stable, but is vulnerable to changes to the structure of the page」[\[45\]](https://web.hypothes.is/blog/fuzzy-anchoring/)。现方案同时保存三种选择器，并按四级依次回退：Range Selector（无变更时最快）→ Position Selector（结构变、文本未变）→ Context\-first Fuzzy Matching（在期望起点附近模糊搜 prefix、终点附近搜 suffix，比对二者之间文本与 exact 的差异，若差异在给定接受阈值内则算命中）→ Selector\-only Fuzzy Matching（last\-ditch，仅对 exact 做全文模糊搜）[\[45\]](https://web.hypothes.is/blog/fuzzy-anchoring/)。注意第三级的判定是「差异在阈值内即命中」——这是一个近似判定，不能作为确定性物化的输入。工程成本也由官方写明：必须维护 DOM 与字符串的完整双向映射，首次映射「can take a long time \(several seconds\)，especially for longer documents」，且「We don't yet have automatic change detection」[\[45\]](https://web.hypothes.is/blog/fuzzy-anchoring/)；一次锚定要靠 dom\-text\-mapper、diff\-match\-patch、text\-match\-engines、dom\-text\-matcher 四层组件凑出来。至于最想要的那个数字——锚点丢失率或 orphan 比例——**本轮未找到公开量化数据**，官方博客与规范都只给机制与失败条件，未给命中率统计。

**第三条，纯文本 \+ 外置标注这条范式自己就需要两个额外的校验与迁移扩展。**TEI P5 把 stand\-off markup 定义为「the annotation of information by pointing at it, rather than by placing XML tags within it」，做法是另建一棵不含文本内容、只含指针的树 [\[46\]](https://www.tei-c.org/release/doc/tei-p5-doc/en/html/NH.html#NHSO)；TEI 工作组文档同时承认「there is no specific recommendation for the appropriate applications of standoff markup」。STAM 自称 radical stand\-off，文本按原样保存为不含任何标记的 utf\-8 纯文本、注释通过字符偏移指向文本片段 [\[47\]](https://annotation.github.io/stam/)——这几乎就是本项目的理想形态。但 STAM 官方必须为此单独提供两个扩展：`stam-textvalidation` 用于「Validation of the integrity of stand\-off annotations\. Do they still point to the right text?」[\[48\]](https://annotation.github.io/stam/specs/extensions/stam-textvalidation)，`stam-transpose` 用 Smith\-Waterman 或 Needleman\-Wunsch 自动对齐两份相似文本并据此把注释迁移过去 [\[49\]](https://annotation.github.io/stam/specs/extensions/stam-transpose/)。需要序列对齐算法来救回偏移，就是「外置偏移不具备确定性」最硬的工程证据。URL Text Fragments 规范给出的是同类自陈：§2\.2 明确「links like this may "rot"」，§4\.1 最佳实践则说 range\-based 匹配「less stable」，页面后来在更早位置出现同样文本时链接会指向非预期片段 [\[52\]](https://wicg.github.io/scroll-to-text-fragment/)。

**第四条，CRDT 的锚点确实解决了「随编辑自动跟随」，但它无法脱离 CRDT 文档在纯文本仓库里持久化。**Yjs 的问题陈述与本项目完全同构：「Normal index\-positions \(expressed as integers\) are not convenient to use because the index\-range is invalidated as soon as a remote change manipulates the document」[\[53\]](https://docs.yjs.dev/api/relative-positions)。它的 RelativePosition 把锚点固定到共享文档中的某个元素上、不受远端变更影响，并声称「guaranteed to always point to the same location」；锚点本身可 `encodeRelativePosition` 成字节或直接 JSON 序列化，存进任意文件都行。关键约束在还原侧：必须 `createAbsolutePositionFromRelativePosition(relPos, Y.Doc)`，且「If the relative position cannot be referenced, or the type is deleted, then the result is null」[\[53\]](https://docs.yjs.dev/api/relative-positions)。Automerge 的 cursor 同理——cursor 类型就是 `string`、官方称其表示「shareable」，但读回位置必须 `getCursorPosition(doc, path, cursor)`，即必须持有 Automerge Doc [\[54\]](https://automerge.org/automerge/api-docs/js/functions/getCursor.html)。Loro 说得更明白：解析必须 `doc.getCursorPos(cursor)`，且官方提示「It may also return an updated cursor you should persist to minimize future replays」[\[56\]](https://loro.dev/docs/tutorial/text)——锚点会随时间需要被重新持久化以避免重放成本。判断：把 CRDT 锚点写进 Git 仓库里的一个 JSON 文件，得到的是一串没有解释器的字节。

**顺带被这条证据链否掉的，是「把边界控制字符写进纯文本再交给 CRDT 合并」这个折中方案。**Peritext 给出的反例足够具体：Alice 加粗「The fox」得到 `**The fox** jumped.`，Bob 并发加粗「fox jumped」得到 `The **fox jumped.**`，纯文本合并后控制字符交错成 `**The **fox** jumped.**`，渲染结果是两人都加粗的「fox」反而不加粗 [\[57\]](https://www.inkandswitch.com/peritext/)；两人各自在行首插 `#` 想建一级标题，合并成 `##` 变二级标题，作者补充「Other markup languages, such as reStructuredText or HTML, suffer from similar problems」[\[57\]](https://www.inkandswitch.com/peritext/)。改用隐藏控制字符同样不成立：作者试过计数 span 起止、把 span\-end 与 span\-begin 配对等变体，「found them difficult to reason about and prone to strange edge cases」，根因是累积状态不足以表达重叠 span [\[57\]](https://www.inkandswitch.com/peritext/)。Automerge 的设计取向是把格式区间「conceptually stored outside the text」，每个 span 带 `expand` 标志决定边界插入时是否扩张 [\[55\]](https://automerge.org/docs/reference/documents/rich-text/)——即富文本 CRDT 自己也放弃了「把区间写进正文」。

四条证据的合并结论：外挂选择器与 CRDT 锚点不进权威层，但它们在派生层有明确位置。派生索引里保留一份 TextQuoteSelector 风格的 exact \+ 前后 32 字符上下文，用于在结构边界被人为破坏（删了标题、改了层级）时给出「这段内容原本属于哪张卡片」的尽力而为提示——这是恢复辅助，不是权威来源。

---

## 六、方案建议：标题小节定边界，围栏容器兜底

> **本节要点**：本章给出可直接落地的定稿——三级边界优先级、逻辑名到物化文件名的规则、跨文件原子性的补齐方式，以及七条可进 CI 的逃生舱断言。权威格式仍是 Markdown，变的只是边界从哪里来。
>
>

### 6\.1 三级边界优先级与语法定稿

三级优先级按「解析器优先尝试哪一档」排序，越靠前的档位覆盖越多的日常情形。

**L1 主边界：标题小节（heading\-scoped section）。**候选块以 H3 小节为单位，H2 保留为分区（沿用 `indexSections` 现有语义），逻辑名写在标题行尾的属性里：

```markdown
## 待处理候选

### Agent 开发边界的三条硬约束 {#k-20260918-agent-development-boundaries}

边界一：Agent 不得越过工作区写入宿主目录。

边界二：所有外部调用必须显式声明超时与重试上限。

### 下一张候选卡的标题 {#k-20260918-next-candidate}

这里开始就是另一张卡片了。
```

边界定义：从该 H3 标题行的首字节起，到下一个同级或更高级标题行的前一个字节止（文档结束时到文件末尾）。这条规则与 org 的 section 定义同构 [\[18\]](https://orgmode.org/worg/org-syntax.html)，与 Pandoc `--section-divs` 的推导一致 [\[3\]](https://pandoc.org/MANUAL.html#option--section-divs)。属性语法用 Pandoc 的 `header_attributes`（`{#identifier}`）[\[2\]](https://pandoc.org/MANUAL.html#extension-header_attributes)，Go 侧由 goldmark 的 `parser.WithAttribute()` 原生解析，本轮已实测通过。使用者在小节内部做任何正文编辑（改字、增删段落、段落重排）都不触碰这两条标题行，边界自动保持。

**L2 兜底：围栏容器（fenced div）。**只在标题层级表达不了的两种情形下使用——同一个小节里需要圈出多个互不相邻的候选，或候选内容跨越了标题结构：

```markdown
### 一次会议里冒出的三个想法

前面这段是背景，不属于任何候选。

:::{#k-20260918-agent-development-boundaries .kb-candidate}
Agent 开发边界的三条硬约束……

这段也属于同一张卡片。
:::

中间这段又是背景。

:::{#k-20260918-next-candidate .kb-candidate}
另一个候选的正文。
:::
```

语法遵循 Pandoc `fenced_divs` 的最小子集：行首 ≥3 个连续冒号 \+ 属性为开围栏，行首 ≥3 个连续冒号且无属性为闭围栏，围栏行与前后块之间留空行，属性顺序必须 identifier → classes [\[1\]](https://pandoc.org/demo/example33/8.18-divs-and-spans.html) [\[4\]](https://quarto.org/docs/authoring/markdown-basics.html#sec-divs-and-spans)。**不支持嵌套**——这一条是刻意的收窄，理由是 AsciiDoc 的开放块也明确禁止嵌套在另一个开放块内 [\[12\]](https://docs.asciidoctor.org/asciidoc/latest/blocks/open-blocks/)，而放弃嵌套能让自实现的扫描器短到可以完全掌控，避免押注 11 stars、2023\-01 停更的第三方扩展 [\[10\]](https://api.github.com/repos/stefanfritsch/goldmark-fences)。实现上按 brief\_06 的判断走 `SplitBlocks` 的 `fenceEnd` 扩展路径，代价低于处理跨行注释闭合。

**L3 退役：HTML 注释哨兵。**迁移期保留只读兼容，读到 `<!-- kb:begin -->` / `<!-- kb:end -->` 时正常解析、同时输出一条警告并给出「转成 L1 或 L2」的建议命令；不再新写哨兵，解析器不为它增加任何新能力。退役理由已在第一章给出：它在渲染视图里不可见、在源码态没有语义，且「用正文里的特殊行做分隔符」这条路已有明确的反面先例——TiddlyWiki 为 CompoundTiddlers 自陈「不允许正文中出现单独一行的 `+`」[\[36\]](https://tiddlywiki.com/static/CompoundTiddlers.html)，CriticMarkup 与宿主 Markdown 语法的耦合让正确转换的算法「相当复杂、边缘情况极多」[\[51\]](https://fletcher.github.io/MultiMarkdown-6/syntax/critic.html)。

**逻辑名到物化文件名的规则。**逻辑名即标题属性或围栏属性里的那个 identifier，形如 `k-20260918-agent-development-boundaries`，由现有的 `NewCardID` 与 `Slug` 生成（`internal/model/id.go`），格式沿用已定的 `k-yyyyMMdd-slug`：日期段取候选创建日（`20260918`），slug 段由标题文本 slug 化。物化时文件名直接等于逻辑名加 `.md` 后缀：

```text
Note 侧：  notes/2026-09-18.md
           └─ ### Agent 开发边界的三条硬约束 {#k-20260918-agent-development-boundaries}

物化后：  cards/k-20260918-agent-development-boundaries.md
```

这条映射是一对一且无歧义的：文件名由 ID 决定、ID 由标题属性固定，因此同一逻辑名重复物化必然幂等。ID 缺失时先执行「有则复用、无则创建」，语义对齐 `org-id-get-create` [\[23\]](https://orgmode.org/manual/Handling-Links.html)。ID 字符集遵循可移植口径——AsciiDoc 官方推荐 ID 首字符为字母、不含空格 [\[13\]](https://docs.asciidoctor.org/asciidoc/latest/attributes/id/)，Zim 则给出了文件名侧的禁用字符清单（`? # / \ * " < > | %`）[\[43\]](https://zim-wiki.org/manual/Help/Pages.html)；`k-yyyyMMdd-slug` 的现有格式已同时满足两端。

**物化 op 的五步定义。**新增一个 `materialize_card` op 并注册进 `AllOpNames`（该集合是封闭的，新增物化逻辑必须注册），执行顺序固定为：

1. 用小节索引定位该逻辑名对应的字节 Span——L1 走标题层级推导，L2 走围栏行推导（`FencedContainer` 节点无 line segments，区间需由子节点或围栏行自行算出）。

2. 复用 `Unprocessed.Cut` 的定位—剪除—自检路径取出该段字节，保持现有的保字节契约。

3. 写入 `cards/<逻辑名>.md`，front matter 承载上下文字段，字段集合直接对齐 org 的 `org-archive-save-context-info` 默认值——物化时间、源文件、标题路径、分类、标签 [\[24\]](https://git.savannah.gnu.org/cgit/emacs/org-mode.git/plain/lisp/org-archive.el)。

4. 用 `ReplaceSectionBody` 把源 Note 里该小节替换为一行指向 `cards/<逻辑名>.md` 的链接（保留标题行与 ID，便于回溯）。

5. 索引层不参与本次写入，事后由 `Rebuild` 全量重建 `TableCards` 与 `TableRelations`。

注意第 4 步是移动而非复制。org 官方对 `org-refile-copy` / `org-refile-keep` 的警告是「this may result in duplicated 'ID' properties」[\[20\]](https://orgmode.org/manual/Refile-and-Copy.html)；本项目的逻辑名唯一性同样撑不住复制语义，保留原件的需求应通过 Git 历史满足，而不是在工作树里留两份。

### 6\.2 推荐架构与可随时更换的派生层

五层自底向上，权威层以下不变、派生层以上可整层替换。

*图 4：派生消费层是一条隔离带——它上面的可视化可以整层换掉，下面的权威字节不动（来源：本轮选型结论 \+ EverGreen 源码审计 **`internal/index/schema.go`**、**`internal/index/rebuild.go`**）*

这个分层的关键性质是隔离带的位置。权威层、候选语义层、物化层三者只认「Markdown 字节 \+ 标题小节 \+ 逻辑名」，不认任何具体前端；派生消费层（块 JSON sidecar 与 SQLite 索引）是纯计算产物，删掉后可由权威层完整重建。org\-roam 已经验证过这种分工的可行性并把它写成了官方承诺：「notes are first and foremost plain Org\-mode files – Org\-roam simply builds an auxiliary database」，且「The notes are still functional even if Org\-roam ceases to exist」[\[32\]](https://www.orgroam.com/manual.html)。现有的 `Rebuild` 实现恰好也是这个取向——清空非保留条目后全量构建，说明索引层从设计上就被当成可丢弃产物。

可视化可达性因此有三档出口，且三档互不依赖。第一档，直接用 Obsidian、GitHub 或任何 CommonMark 渲染器打开权威 Markdown——标题小节被渲染成标题，`{#id}` 在不支持标题属性的渲染器里留为标题末尾的字面文字（markdown\-it\-py 4\.2\.0 实测），可读性不受影响。第二档，需要「多文件拼成一篇」时，Obsidian 的 embed 机制现成可用（`![[Note#Heading]]` 按标题嵌入）[\[39\]](https://help.obsidian.md/embeds)，标题小节的边界与 embed 的寻址粒度天然对齐——这是选标题小节而非围栏容器的一项额外收益：围栏容器没有任何主流前端能按它的 ID 做嵌入。第三档，需要块级交互时用派生的块 JSON sidecar 喂自建视图，换视图不动权威层。

物化出口保留「文件即块」的形态但不把它当权威层，这一点值得单独说明。`cards/` 目录下一卡一文件，物化操作近似 Dendron 官方所说的「把重构变成单纯 rename」[\[71\]](https://raw.githubusercontent.com/dendronhq/dendron-site/master/vault/dendron.topic.hierarchies.md)，Git 侧能拿到文件级重命名的自动追溯 [\[76\]](https://git-scm.com/docs/git-blame)。Note 侧则不拆文件，因此避开了「缺少能把目录拼成一篇笔记的现成前端」这个真正的堵点——那两个 Obsidian 插件（Feuillets 与 Folder Reader）都还是维护者自述、体量偏小、本轮未实操核验 [\[74\]](https://community.obsidian.md/plugins/feuillets) [\[75\]](https://community.obsidian.md/plugins/folder-reader)，不适合作为长期依赖。如果将来 `cards/` 的组装需求真的出现，Jekyll collections 的 `order` 机制给了最贴近的官方范式：一份显式顺序清单优先于文件名序 [\[34\]](https://jekyllrb.com/docs/collections/)；Feuillets 的 Binder order 被显式持久化、独立于 vault 字母序，也印证文件名序不够用 [\[74\]](https://community.obsidian.md/plugins/feuillets)。

### 6\.3 迁移路径与逃生舱验收标准

**迁移分三步，每步都可单独回滚。**第一步只读兼容：解析器同时认 L1、L2、L3 三档，读到 L3 即告警，不改任何现有文件；这一步上线后可立即观察现存 Note 里有多少块仍依赖哨兵。第二步批量转换：提供一条把 L3 区间就地改写成 L1 标题小节的命令（哨兵区间的首段若已是 H3 标题则只补 `{#id}`，否则在区间首插入一行 H3 标题），转换前后跑一遍下面的字节保真断言。第三步收口：解析器移除 L3 的写入路径，只保留读取兼容与告警。

**跨文件原子性有一个真实缺口，处理方式不依赖数据库事务。**现状是：跨进程互斥由 `run.lock` 承载，但跨文件写回的事务性在 M3 阶段尚不完备；`Unprocessed.Cut` 只支持单文件剪除，跨文件同步需要新机制。org 那边也没有更好的答案——官方手册对 `org-refile` 的原子性（写入中断时的一致性保证）未作说明，未见对并发或崩溃语义的陈述。补齐方式是四条工程约定：

- **两阶段落盘**：先把目标卡片写到同目录下的临时名（`cards/.k-20260918-agent-development-boundaries.md.tmp`），再 `rename` 到正式名——同目录 rename 在主流文件系统上是原子替换。

- **先目标后源**：只有目标文件 rename 成功之后，才用 `ReplaceSectionBody` 改源 Note；顺序反过来会在崩溃时丢内容。

- **journal 记录**：每次物化前写一条 journal（源文件、字节 Span、目标路径、逻辑名、当前阶段），崩溃后启动时按 journal 判定回放或回滚，使仓库只可能停在「完全未物化」或「完全已物化」两态。

- **全程持锁 \+ 索引事后重建**：整个 op 持 `run.lock`，禁止并发物化；索引不进事务，一律事后 `Rebuild`——索引可重建，所以它不需要强一致。

**逃生舱验收标准，七条全部可进 CI。**这是「上线后再换存储格式是最不希望出现的结果」这条约束的落地方式：选对无法被证明，能退出可以被断言。

|\#|断言|怎么测|守住什么|
|---|---|---|---|
|1|改正文不改边界|对每篇 Note 随机做 N 次正文扰动（段落内改字、增删段落、段落重排），不动标题行与围栏行；重新解析后，候选块的逻辑名集合与「逻辑名 → 正文内容」映射保持一致|第一否决项本身，本轮全部结论的地基|
|2|物化字节保真|`cards/<id>.md` 的正文字节与源 Note 该小节正文字节逐字节相等（剥去标题行、front matter 与首尾空行后比对）|`internal/mdfile` 的保字节契约不被物化破坏|
|3|物化幂等|同一逻辑名重复执行 `materialize_card`：不产生第二个文件、目标文件哈希不变、源 Note 不再被改动|逻辑名到文件名的一对一映射|
|4|索引可重建|删除全部派生索引后 `Rebuild`，`TableCards` 与 `TableRelations` 与删除前逐行等价|派生层是可丢弃产物，隔离带成立|
|5|降级不丢正文|把含 L2 围栏的 Note 过一遍 CommonMark 渲染器，断言全部正文段落出现在输出中（围栏行原样显示为文字是已知且允许的，丢正文不允许）|可视化可达性的底线|
|6|语法可剥离（逃生舱）|一条命令把整个仓库导出为不含任何容器语法的纯 CommonMark（剥掉 `:::` 行、把标题行的 `{#id}` 移入 front matter 或 sidecar），断言剥离前后的正文字节集合完全相同|随时能退出这套语法而不丢内容——这条是对「不想换格式」的正面回答|
|7|崩溃两态|在两阶段落盘的每个阶段注入失败，断言仓库停在「完全未物化」或「完全已物化」，不存在半成品或孤儿临时文件|跨文件原子性缺口的兜底|

*表 2：七条逃生舱断言——第 1 条守住本轮的支点约束，第 6 条把「能退出」变成可验证指标*

其中第 6 条是整份方案最重要的一条保险。它成立的前提正好是标题小节这一档的固有性质：把 `{#id}` 从标题行摘掉之后，剩下的仍是一篇结构完整的普通 Markdown，小节边界依然由标题层级隐含表达。换成围栏容器作主路径就做不到这么干净——剥掉围栏行会同时丢掉边界信息；换成 org 或块原生存储则连「剥离」这个动作都不存在，只剩格式转换及其全套损耗。

两项残余缺口进第八章：`internal/store/write.go` 在多文件并发写入失败时的回滚表现尚未实测，索引层新增 blocks 表对全量扫描性能的影响需压测确认。

---

## 七、综合判断：只换边界机制，权威格式不动

**综合判断**：存在比「Markdown \+ 一对哨兵符号 \+ 靠使用者把正文挪进边界之间」更合理的存储方式，它就是标题小节即块——权威格式仍是 Markdown，变的只是边界从「写在正文里的标记」改成「由标题层级推导的结构」。

方向成立。推理链是这样合上的：用户澄清「只改正文、不维护边界」之后（第一章），第一否决项从「物化是否精确」变成「使用者不配合时边界是否还对」（第二章）；这一改判直接让哨兵从三条全绿的方案变成否决项双红（第四章表 1），也把外挂选择器与 CRDT 锚点整族挡在权威层之外——W3C 自认字符偏移 very brittle、STAM 需要序列对齐才能救回偏移、Yjs 锚点脱离 `Y.Doc` 即解析为 null（第五章 5\.3）。剩下四个过否决项的候选里，org 子树与文件即块各在两条权衡项上亮红（换格式的全套生态代价、缺把目录拼成一篇的现成前端），块原生运行时与「Markdown 权威 \+ Git 可评审」直接冲突；标题小节是唯一没有红灯的一列，且它那处唯一的黄灯（需自行实现小节区间截取）正好落在 EverGreen 已有能力的延长线上——`indexSections` 已建 H2 全局 Span 索引、`BlockHeading3` 已是独立块型、`Unprocessed.Cut` 与 `ReplaceSectionBody` 已给出偏移级无损改写的完整路径（第六章）。

残余风险与反向信号：

1. **标题小节把「不删标题、不改层级」变成了新的隐性契约**：使用者虽然不必维护边界标记，但删掉一个 H3 标题或把 H3 改成 H2，边界仍会变化。缓解手段是派生层保留 exact \+ 前后上下文的尽力而为提示，加上第六章第 1 条 CI 断言把「什么算正文编辑」写死；但这条契约本身无法用语法消除。

2. **CommonMark 层面不存在 section 容器，「小节即块」永远是自有约定**：规范只定义 block quote 与 list item 两类容器块，标题是 leaf block [\[7\]](https://spec.commonmark.org/0.31.2/#container-blocks)。这意味着没有任何第三方解析器会替本项目验证这条约定，区间推导的正确性全靠自己的测试覆盖。

3. **围栏容器这条兜底路径的 Go 侧生态确实很薄**：唯一的 pandoc 风格扩展 11 stars、2023\-01 起停更 [\[10\]](https://api.github.com/repos/stefanfritsch/goldmark-fences)，自实现最小扫描器虽可行，但等于把一份语法解析的长期维护责任揽进项目内部。若 L2 的实际使用比例超出预期，应重新评估是否值得保留这一档。

### 7\.1 结论的适用边界

这个结论成立的前提有三条，缺任何一条都要重新评估。第一条，权威存储必须是人手可编辑、Git 可评审的纯文本——如果这条约束放松（例如接受「编辑只通过自建前端进行」），块原生运行时的全部红灯都会转绿，Trilium 那份「为什么不用 flat files」的六条判据立刻从反方论据变成正方论据 [\[63\]](https://docs.triliumnotes.org/user-guide/faq)。第二条，候选块的粒度是「一段到若干段连续内容」，与一个小节的自然粒度匹配——如果需求变成「圈出一句话」或「圈出跨越多个小节的内容」，L2 围栏的使用比例会显著上升，届时该重新比较围栏容器与文件即块。第三条，单写者。全部结论都建立在「一次只有一个进程写」之上，`run.lock` 是这条前提的实现；一旦引入多端并发编辑，Peritext 那组并发标记交错的反例就会开始生效 [\[57\]](https://www.inkandswitch.com/peritext/)，整个选型需要从 CRDT 一侧重做。

结论不适用的场景也应说清：本方案不解决富文本格式（加粗、颜色、表格样式）的权威表达，也不解决多端实时协作。它解决的是单一问题——在一篇人写的 Markdown 笔记里，稳定地圈出若干张候选卡片，并确定性地把它们物化成独立文件。

### 7\.2 与上一轮结论的差异

变的部分只有一层：边界的来源。上一轮的架构是「Markdown 权威 \+ HTML 注释哨兵标记 \+ 派生 sidecar」，本轮把中间那一层换成「标题小节 \+ 标题属性 ID，围栏容器兜底」，哨兵降为迁移期只读。权威格式没变，派生 sidecar 没变，Git 评审链路没变，物化仍是「从 Note 里截一段字节写成 `cards/<id>.md`」。

没变的部分里有一条值得重新确认：派生层可整层替换这个设计，在本轮反而变得更重要。理由是本轮把「外挂选择器与 CRDT 锚点只能进派生层」写成了明确结论——恢复提示、块级视图、模糊重锚这些能力全部落在派生层，而它们都是易变、易被更好的方案替代的部分。org\-roam 的那句官方承诺可以直接借用作本项目的验收标准：即使派生层整个消失，笔记本身仍然可用 [\[32\]](https://www.orgroam.com/manual.html)。

最后，用户最不希望出现的结果是「上线后再换存储格式」。本轮给出的答复不是一句承诺，而是第六章表 2 的第 6 条断言：一条命令把整个仓库剥成不含任何容器语法的纯 CommonMark，剥离前后正文字节集合完全相同。这条断言能成立，恰恰因为选的是标题小节——摘掉 `{#id}` 之后，剩下的还是一篇完整的普通 Markdown。

---

## 八、待验证事项

**待验证与口径说明**（以下结论本轮未取到可引用数据，不作精确判断）：

- **Hypothesis 锚点丢失率 / orphan 比例**：**本轮未找到公开量化数据**。官方博客只给四级回退策略与失败条件，未给命中率或丢失率统计，也未给统计口径 [\[45\]](https://web.hypothes.is/blog/fuzzy-anchoring/)。第五章 5\.3 节对外挂锚点的否定依据是规范与官方实现的自陈机制，不含任何量化对比。

- **大量小文件对编辑器索引与 Git 性能的影响**：**本轮未找到公开可复现 benchmark**。官方对 `git status` 只给定性口径（大工作树上可能很慢、无一套对所有人最优的配置）[\[77\]](https://git-scm.com/docs/git-status/2.40.0)；FSMonitor 的「status 降到 1 秒以内」来自厂商工程博客自报，测试环境与仓库规模未公开 [\[78\]](https://github.blog/engineering/infrastructure/improve-git-monorepo-performance-with-a-file-system-monitor/)。第四章对「文件即块」的评估据此只用可验证的 blame 阈值（跨文件检测默认下限 40 个字母数字字符）[\[76\]](https://git-scm.com/docs/git-blame)，不引用任何性能数字。

- **Feuillets 与 Folder Reader 的实际行为**：两者的机制描述均为维护者自述，本轮未实操核验，无实测截图 [\[74\]](https://community.obsidian.md/plugins/feuillets)[\[75\]](https://community.obsidian.md/plugins/folder-reader)。「缺少现成前端」这一判断据此只用「候选前端数量少、体量小」这一可核事实，未对其可用性下定论。

- **Obsidian 对 ****`:::`**** 与 ****`{#id}`**** 的实际渲染**：未实测，仅据官方支持清单未列出该语法推断 [\[17\]](https://obsidian.md/help/obsidian-flavored-markdown)；官方清单「未列出」不等于「已实测报错」。第六章表 2 第 5 条断言正是为补这个缺口而设。

- **GitHub 网页版（非 ****`api.github.com/markdown`****）对 ****`:::`**** 与 HTML 注释哨兵的渲染**：未实测。本轮对哨兵与标题属性的补充 API 调用触发 403 rate limit 与超时，未取到结果；已取到的结论仅限该 API 端点、`mode=markdown`、未鉴权条件。

- **`org-refile`**** 的原子性**：官方手册未说明写入中断时的一致性保证，未见对并发或崩溃语义的陈述 [\[20\]](https://orgmode.org/manual/Refile-and-Copy.html)。第六章的两阶段落盘与 journal 方案是本项目自定的补齐方式，未在 org 生态中找到可对照的先例。

- **`internal/store/write.go`**** 在多文件并发写入失败时的回滚表现**：未通过实测验证（源码审计结论）。这是第六章跨文件原子性方案上线前必须补的一项。

- **索引层新增 blocks 表对全量扫描性能的影响**：需压测确认，本轮未测（源码审计结论）。

- **Logseq Markdown Mirror 双向同步的最新状态**：仅取到 2026\-05 的维护者 changelog 与 2026\-07 的公开路线图，2026\-05 之后是否已从 feature branch 合入主干未核实 [\[66\]](https://discuss.logseq.com/t/logseq-db-changelog/30013/37)[\[68\]](https://logseq.io/page/3bc00ad3-f421-41e7-8c65-40861c298be5/6954ee2a-506b-4dd9-bd6d-0dc24db9c055)。第三章「混合路线未找到已交付先例」这一判断的时效止于该日期。

- **`:::`**** 在 MyST 两套实现间的语义一致性**：本轮证据取自 myst\-parser（Sphinx 侧）官方文档，与 mystmd 新版规范站之间是否完全一致未核对 [\[5\]](https://myst-parser.readthedocs.io/en/latest/syntax/optional.html#syntax-colon-fence)。

- **AsciiDoc 语言规范的正式状态**：官方页面自标为「AsciiDoc pre\-spec」，本文引用其开放块与 ID 条款时未引用正式 spec 条款编号 [\[12\]](https://docs.asciidoctor.org/asciidoc/latest/blocks/open-blocks/)。

- **Pandoc fenced div 属性语法的已知脆弱点**：社区讨论提出属性解析 brittle、与 directive 语法不一致（jgm/pandoc 的 \#10108 与 \#7480），本轮仅取到 issue 线索，未回到官方结论；第六章把 L2 收窄为「不支持嵌套的最小子集」部分出于这一未决风险。

- **本轮为控制交付时长未展开的方向**：一是 JavaScript 侧 remark / mdast 生态里是否已有成熟的 section\-scoped 节点实现（可作为标题小节推导的第二实现参照）；二是 Anytype、AFFiNE、SiYuan 的 Markdown 往返字段级丢失清单（官方文档均未列出，需逐个实测）。两者都不影响本轮结论方向，可按需追加一轮采集。

---

## 九、参考文献

- \[1\] Pandoc, 用户手册 §8\.18 Divs and Spans, 2026\-09 访问\. https://pandoc\.org/demo/example33/8\.18\-divs\-and\-spans\.html

- \[2\] Pandoc, MANUAL · extension header\_attributes, 2026\-09\-19 抓取\. https://pandoc\.org/MANUAL\.html\#extension\-header\_attributes

- \[3\] Pandoc, MANUAL · option \-\-section\-divs, 2026\-09\-19 抓取\. https://pandoc\.org/MANUAL\.html\#option\-\-section\-divs

- \[4\] Quarto, Markdown Basics · Divs and Spans, 2026\-09 访问\. https://quarto\.org/docs/authoring/markdown\-basics\.html\#sec\-divs\-and\-spans

- \[5\] MyST\-Parser, Optional syntax · colon\_fence / attrs\_block, 2026\-07\-27 更新\. https://myst\-parser\.readthedocs\.io/en/latest/syntax/optional\.html\#syntax\-colon\-fence

- \[6\] markdown\-it\-container, 官方 README, 2026\-09\-19 访问\. https://github\.com/markdown\-it/markdown\-it\-container

- \[7\] CommonMark, Spec 0\.31\.2 · Container blocks\. https://spec\.commonmark\.org/0\.31\.2/\#container\-blocks

- \[8\] GitHub, GitHub Flavored Markdown Spec, 现行版\. https://github\.github\.com/gfm/

- \[9\] yuin, goldmark README（attributes / parsing / extensions）, v1\.8\.6, 2026\-09\-19 抓取\. https://github\.com/yuin/goldmark

- \[10\] GitHub API, stefanfritsch/goldmark\-fences 仓库计量, 2026\-09\-19 查询\. https://api\.github\.com/repos/stefanfritsch/goldmark\-fences

- \[11\] gomarkdown, README · extensions, 2026\-09\-19 抓取\. https://github\.com/gomarkdown/markdown\#extensions

- \[12\] AsciiDoc Language, Open Blocks, 2026 版\. https://docs\.asciidoctor\.org/asciidoc/latest/blocks/open\-blocks/

- \[13\] AsciiDoc Language, ID Attribute, 2026 版\. https://docs\.asciidoctor\.org/asciidoc/latest/attributes/id/

- \[14\] Docutils, reStructuredText Directives · container, 2026\-09\-19 抓取\. https://docutils\.sourceforge\.io/docs/ref/rst/directives\.html\#container

- \[15\] kramdown, Quick Reference · Block Attributes, v2\.5\.2, 2026\-09 访问\. https://kramdown\.gettalong\.org/quickref\.html\#block\-attributes

- \[16\] jgm, djot syntax 规范, 2026\-09\-19 抓取\. https://github\.com/jgm/djot/blob/main/doc/syntax\.md

- \[17\] Obsidian, Obsidian Flavored Markdown, 2026\-09 访问\. https://obsidian\.md/help/obsidian\-flavored\-markdown

- \[18\] Org mode, Org Syntax（Worg, v2）, 2026\-09\-19 取证\. https://orgmode\.org/worg/org\-syntax\.html

- \[19\] The Org Manual, §17\.10 Org Syntax, 2026\-09\-19 取证\. https://orgmode\.org/manual/Org\-Syntax\.html

- \[20\] The Org Manual, §9\.1 Refile and Copy, 2026\-09\-19 取证\. https://orgmode\.org/manual/Refile\-and\-Copy\.html

- \[21\] The Org Manual, §9\.2\.1 Moving subtrees, 2026\-09\-19 取证\. https://orgmode\.org/manual/Moving\-subtrees\.html

- \[22\] The Org Manual, §7\.1 Property Syntax, 2026\-09\-19 取证\. https://orgmode\.org/manual/Property\-Syntax\.html

- \[23\] The Org Manual, §4\.5 Handling Links（:ID: 语义）, 2026\-09\-19 取证\. https://orgmode\.org/manual/Handling\-Links\.html

- \[24\] GNU Emacs / Org mode, org\-archive\.el 源码, 2026\-09\-19 拉取 main\. https://git\.savannah\.gnu\.org/cgit/emacs/org\-mode\.git/plain/lisp/org\-archive\.el

- \[25\] niklasfasching, go\-org 仓库与 README, 2026\-09\-19 访问\. https://github\.com/niklasfasching/go\-org

- \[26\] niklasfasching, go\-org · org/headline\.go, commit 2f088a1\. https://github\.com/niklasfasching/go\-org/blob/master/org/headline\.go

- \[27\] vscode\-org\-mode, 仓库（最后 push 2024\-04\-13）, 2026\-09\-19 访问\. https://github\.com/vscode\-org\-mode/vscode\-org\-mode

- \[28\] BBazard, obsidian\-orgmode\-cm6 README, 2026\-09\-19 访问\. https://github\.com/BBazard/obsidian\-orgmode\-cm6

- \[29\] GitHub, github/markup README, 2026\-09\-19 访问\. https://github\.com/github/markup

- \[30\] wallyqs, org\-ruby 仓库（最后 push 2024\-08\-14）, 2026\-09\-19 访问\. https://github\.com/wallyqs/org\-ruby

- \[31\] nobiot, org\-transclusion User Manual 2\.0\.0\-rc, 2026\-01\-03\. https://nobiot\.github\.io/org\-transclusion/

- \[32\] Org\-roam, User Manual 2\.3\.1\-devel, 2026\-09\-19 取证\. https://www\.orgroam\.com/manual\.html

- \[33\] Hugo, Page Bundles, 2026\-06\-18 更新\. https://gohugo\.io/content\-management/page\-bundles/

- \[34\] Jekyll, Collections（含 order 元数据）, v4\.4\.1\. https://jekyllrb\.com/docs/collections/

- \[35\] TiddlyWiki, TiddlyWiki on Node\.js, 2022\-06\-17\. https://tiddlywiki\.com/static/TiddlyWiki%2520on%2520Node\.js\.html

- \[36\] TiddlyWiki, CompoundTiddlers, 2025\-08\-14\. https://tiddlywiki\.com/static/CompoundTiddlers\.html

- \[37\] TiddlyWiki, TiddlerFiles（\.tid 与 \.meta 格式）, 2021\-07\-14\. https://tiddlywiki\.com/static/TiddlerFiles\.html

- \[38\] Astro, Content Collections, 2026\-09\-19 访问\. https://docs\.astro\.build/en/guides/content\-collections/

- \[39\] Obsidian, Embed files（embeds）, 2026\-09\-19 访问\. https://help\.obsidian\.md/embeds

- \[40\] TiddlyWiki, TranscludeWidget, v5\.4\.1, 2026\-09\-19 访问\. https://tiddlywiki\.com/static/TranscludeWidget\.html

- \[41\] Pandoc, MANUAL · extension fenced\_code\_attributes, 2026\-09\-19 抓取\. https://pandoc\.org/MANUAL\.html\#extension\-fenced\_code\_attributes

- \[42\] Pandoc, MANUAL · extension implicit\_header\_references, 2026\-09\-19 抓取\. https://pandoc\.org/MANUAL\.html\#extension\-implicit\_header\_references

- \[43\] Zim Wiki, Manual · Pages, 2026\-09\-19 访问\. https://zim\-wiki\.org/manual/Help/Pages\.html

- \[44\] W3C, Web Annotation Data Model（REC）, 2017\-02\-23\. https://www\.w3\.org/TR/annotation\-model/

- \[45\] Hypothesis, Fuzzy Anchoring, 2013\-04\-22\. https://web\.hypothes\.is/blog/fuzzy\-anchoring/

- \[46\] TEI Consortium, TEI P5 Guidelines · Stand\-off Markup, P5 4\.11\.0, 2026\-02\-18\. https://www\.tei\-c\.org/release/doc/tei\-p5\-doc/en/html/NH\.html\#NHSO

- \[47\] STAM（KNAW HuC / CLARIAH）, 官方模型说明, 现行版\. https://annotation\.github\.io/stam/

- \[48\] STAM, 扩展 stam\-textvalidation, 现行版\. https://annotation\.github\.io/stam/specs/extensions/stam\-textvalidation

- \[49\] STAM, 扩展 stam\-transpose, 现行版\. https://annotation\.github\.io/stam/specs/extensions/stam\-transpose/

- \[50\] CriticMarkup, 官方说明与三法则, Apache\-2\.0\. http://criticmarkup\.com/

- \[51\] MultiMarkdown\-6, Syntax · CriticMarkup, 现行版\. https://fletcher\.github\.io/MultiMarkdown\-6/syntax/critic\.html

- \[52\] WICG, Text Fragments（Draft Community Group Report）, 2023\-12\-13\. https://wicg\.github\.io/scroll\-to\-text\-fragment/

- \[53\] Yjs, Relative Positions, 官方文档\. https://docs\.yjs\.dev/api/relative\-positions

- \[54\] Automerge, API · getCursor, v3\.5\.0\. https://automerge\.org/automerge/api\-docs/js/functions/getCursor\.html

- \[55\] Automerge, Rich Text（marks 与 formatting spans）, 2026\-09\-04 lastmod\. https://automerge\.org/docs/reference/documents/rich\-text/

- \[56\] Loro, Text / Cursor 教程, 2026\-09\-04 更新\. https://loro\.dev/docs/tutorial/text

- \[57\] Ink \& Switch, Peritext: A CRDT for Rich\-Text Collaboration, 2021\-11（CSCW 2022 修订）\. https://www\.inkandswitch\.com/peritext/

- \[58\] Anytype, Docs · Storage, 2026\-09\-19 取证\. https://doc\.anytype\.io/anytype/data/storage

- \[59\] Anytype, Docs · Import and Export, 2026\-09\-19 取证\. https://doc\.anytype\.io/anytype/data/import\-and\-export\.md

- \[60\] BlockSuite, Guide · Data Synchronization, 2026\-09\-19 访问\. https://blocksuite\.io/guide/data\-synchronization

- \[61\] AFFiNE, packages/common/nbstore README（canary）, 2026\-09\-19 访问\. https://github\.com/toeverything/AFFiNE/blob/canary/packages/common/nbstore/README\.md

- \[62\] Notion, Help · Export your content, 2026\-09\-19 访问\. https://www\.notion\.com/help/export\-your\-content

- \[63\] Trilium Notes, User Guide · FAQ（为何不用 flat files）, 2026\-09\-19 访问\. https://docs\.triliumnotes\.org/user\-guide/faq

- \[64\] Trilium Notes, Markdown supported syntax, 2026\-09\-19 访问\. https://docs\.triliumnotes\.org/user\-guide/concepts/import\-export/markdown/supported\-syntax

- \[65\] Joplin, Help · FAQ, 2026\-09\-19 访问\. https://joplinapp\.org/help/faq/

- \[66\] Logseq, DB Changelog（Markdown Mirror, PR \#12589 等）, 2026\-05\-16\. https://discuss\.logseq\.com/t/logseq\-db\-changelog/30013/37

- \[67\] Logseq, docs/db\-version\.md（导出口径, 状态截至 2026\-04\-28）\. https://github\.com/logseq/docs/blob/master/db\-version\.md

- \[68\] Logseq, 公开路线图, 2026\-07\-11\. https://logseq\.io/page/3bc00ad3\-f421\-41e7\-8c65\-40861c298be5/6954ee2a\-506b\-4dd9\-bd6d\-0dc24db9c055

- \[69\] SiYuan, 官方 README（v3\.7\.0 起）, 2026\-09\-19 访问\. https://github\.com/siyuan\-note/siyuan/blob/master/README\.md

- \[70\] Pandoc, 3\.11 release, 2026\-08\-29\. https://github\.com/jgm/pandoc/releases/latest

- \[71\] Dendron, 官方文档 · Hierarchies（dot 命名的三条理由）, ≈2022\-03\. https://raw\.githubusercontent\.com/dendronhq/dendron\-site/master/vault/dendron\.topic\.hierarchies\.md

- \[72\] Dendron, 仓库 README（maintenance only 声明）, 2026\-09\-19 访问\. https://github\.com/dendronhq/dendron

- \[73\] Dendron, 官方文档 · Note Reference（嵌套上限）, 2022\-07\-29\. https://wiki\.dendron\.so/notes/f1af56bb\-db27\-47ae\-8406\-61a98de6c78c/

- \[74\] Obsidian 社区插件, Feuillets（v3\.0\.1, GPL\-3\.0）, 2026\-09\-19 访问\. https://community\.obsidian\.md/plugins/feuillets

- \[75\] Obsidian 社区插件, Folder Reader（v1\.0\.0, MIT）, 2026\-09\-19 访问\. https://community\.obsidian\.md/plugins/folder\-reader

- \[76\] Git, git\-blame 官方文档（\-C 跨文件阈值 40 字符）, 2\.53\.0\. https://git\-scm\.com/docs/git\-blame

- \[77\] Git, git\-status 官方文档（大工作树性能口径）, 2\.40\.0\. https://git\-scm\.com/docs/git\-status/2\.40\.0

- \[78\] GitHub Engineering, Improve Git monorepo performance with a file system monitor, 2022\-06\-29\. https://github\.blog/engineering/infrastructure/improve\-git\-monorepo\-performance\-with\-a\-file\-system\-monitor/

- \[79\] The Org Manual, §2\.4 Structure Editing（cut / paste subtree）, 2026\-09\-19 取证\. https://orgmode\.org/manual/Structure\-Editing\.html

- \[80\] EverGreen 源码审计（`internal/mdfile`、`internal/plan`、`internal/index`、`internal/model`）, 2026\-09\-19\. 内部仓库，无公开 URL

- \[81\] 本轮一手实测记录：goldmark v1\.8\.6 \+ goldmark\-fences v1\.0\.0（Go 1\.24\.13）、GitHub Markdown 渲染 API（mode=markdown, 未鉴权）、markdown\-it\-py 4\.2\.0（commonmark preset）、pandoc 3\.11 org 往返、go\-org@2f088a1 org 往返, 2026\-09\-19\. 脚本与输出见本次调研草稿区 raw 目录
