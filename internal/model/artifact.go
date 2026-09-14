package model

// 产物结构体（技术方案 §4.1 / §4.3 / §4.4）。
//
// 每个结构体都带一个 Extra 容器字段承接**未知字段原样透传**：解析时未知 frontmatter 键
// 落进 Extra，**渲染由 mdfile 负责**（落盘只做字节区间拼接，Extra 不参与回写，所以
// 引号风格 / 键顺序 / 注释 / 空行天然逐字保留）。

// Card 是知识卡。
//
// S1 必需：id / status / created_at / updated_at / sources[]；S1 可选：tags / relations[]。
// ReviewedAt / DeletedAt / DeletedReason / ReplacedBy 属 S2 引入：S1 **不写、不校验、
// 读到即原样保留**（状态与删除是两个正交维度，冻结合同 F3）。
type Card struct {
	ID        CardID      `yaml:"id" json:"id"`
	Status    Status      `yaml:"status" json:"status"`
	CreatedAt Date        `yaml:"created_at" json:"created_at"`
	UpdatedAt Stamp       `yaml:"updated_at" json:"updated_at"`
	Sources   []SourceRef `yaml:"sources" json:"sources"`

	Tags      []string   `yaml:"tags,omitempty" json:"tags,omitempty"`
	Relations []Relation `yaml:"relations,omitempty" json:"relations,omitempty"`

	// —— S2 预留（S1 只读不写；出现即原样保留）——
	ReviewedAt    *Stamp      `yaml:"reviewed_at,omitempty" json:"reviewed_at,omitempty"`
	DeletedAt     *Stamp      `yaml:"deleted_at,omitempty" json:"deleted_at,omitempty"`
	DeletedReason string      `yaml:"deleted_reason,omitempty" json:"deleted_reason,omitempty"`
	ReplacedBy    *ReplacedBy `yaml:"replaced_by,omitempty" json:"replaced_by,omitempty"`

	// Extra 承接未知 frontmatter 字段（原样透传容器）。
	Extra map[string]interface{} `yaml:",inline" json:"-"`
}

// ReplacedBy 是失效卡上的替代指针（提案合同 §8.1 第 3 行）。
//
// 为什么是结构体而不是裸 ID：替代关系必须自带**为什么替代**的理由，否则读者只能
// 靠猜；两个子字段一起写、一起读，缺一即视为未给（整体 nil）。
// 为什么是指针：`replaced_by` 缺失与「给了空值」必须可区分——nil 表示这张卡没有
// 替代指针，本层因此不会把空 mapping 写进 frontmatter（不留墓碑）。
// **单向存储**：指针只落在失效卡自己的文件里，被指向的目标卡永不被反写。
type ReplacedBy struct {
	Target string `yaml:"target" json:"target"`
	Reason string `yaml:"reason" json:"reason"`
}

// SourceRef 是知识卡 sources[] 的一条四要素材料关系（EG-KNW-04）。
// Note 存**笔记 ID**，不存路径。
type SourceRef struct {
	Source SourceID    `yaml:"source" json:"source"`
	Note   NoteID      `yaml:"note" json:"note"`
	Rel    MaterialRel `yaml:"rel" json:"rel"`
	Reason string      `yaml:"reason" json:"reason"`
}

// MissingFields 返回缺失的要素名（顺序稳定），供上层出 error / warning。
func (r SourceRef) MissingFields() []string {
	var miss []string
	if r.Source == "" {
		miss = append(miss, "source")
	}
	if r.Note == "" {
		miss = append(miss, "note")
	}
	if r.Rel == "" {
		miss = append(miss, "rel")
	}
	if r.Reason == "" {
		miss = append(miss, "reason")
	}
	return miss
}

// Complete 报告四要素是否齐全。
func (r SourceRef) Complete() bool { return len(r.MissingFields()) == 0 }

// Relation 是论证关系：落在**来源卡**的 relations[]，单向一条。
type Relation struct {
	Type   RelationType `yaml:"type" json:"type"`
	Target CardID       `yaml:"target" json:"target"`
	Reason string       `yaml:"reason" json:"reason"`
}

// Note 是材料笔记（§4.3）。
//
// 笔记**没有 status**（EG-NOTE-01），也**不生成理解自检**分区——两者都是知识卡独有。
type Note struct {
	ID        NoteID   `yaml:"id" json:"id"`
	Source    SourceID `yaml:"source" json:"source"`
	CreatedAt Date     `yaml:"created_at" json:"created_at"`
	UpdatedAt Stamp    `yaml:"updated_at" json:"updated_at"`

	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`

	// —— S2 预留（S1 只读不写；出现即原样保留）——
	ReviewedAt    *Stamp `yaml:"reviewed_at,omitempty" json:"reviewed_at,omitempty"`
	DeletedAt     *Stamp `yaml:"deleted_at,omitempty" json:"deleted_at,omitempty"`
	DeletedReason string `yaml:"deleted_reason,omitempty" json:"deleted_reason,omitempty"`

	// Extra 承接未知 frontmatter 字段（原样透传容器）。
	Extra map[string]interface{} `yaml:",inline" json:"-"`
}

// Source 是原文（§4.4）。正文收录后**不因任何加工改写**。
type Source struct {
	ID      SourceID `yaml:"id" json:"id"`
	URL     string   `yaml:"url" json:"url"`
	Title   string   `yaml:"title" json:"title"`
	SavedAt Stamp    `yaml:"saved_at" json:"saved_at"`

	// Extra 承接未知 frontmatter 字段（原样透传容器）。
	Extra map[string]interface{} `yaml:",inline" json:"-"`
}

// UnprocessedItem 是收件区 `vault/unprocessed.md` 的一个条目：
// **一个顶层列表项 = 一个条目**，键为 source_id。
//
// TargetDomain 是**白名单**用法（领域由目录决定，但收件区条目需要记下目标领域）。
type UnprocessedItem struct {
	SourceID     SourceID `yaml:"source_id" json:"source_id"`
	Title        string   `yaml:"title" json:"title"`
	SavedAt      Stamp    `yaml:"saved_at" json:"saved_at"`
	Reason       string   `yaml:"reason" json:"reason"`
	TargetDomain Domain   `yaml:"target_domain,omitempty" json:"target_domain,omitempty"`

	// Extra 承接未知条目字段（原样透传容器）。
	Extra map[string]interface{} `yaml:",inline" json:"-"`
}

// MissingFields 返回缺失的必需键（source_id / title / saved_at / reason）。
func (i UnprocessedItem) MissingFields() []string {
	var miss []string
	if i.SourceID == "" {
		miss = append(miss, "source_id")
	}
	if i.Title == "" {
		miss = append(miss, "title")
	}
	if i.SavedAt.IsZero() {
		miss = append(miss, "saved_at")
	}
	if i.Reason == "" {
		miss = append(miss, "reason")
	}
	return miss
}
