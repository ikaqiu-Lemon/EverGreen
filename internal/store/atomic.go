package store

// atomic.go 是 store 的**内存预演层**（T-072 批次 B1）。
//
// 预演模式下，本包的所有权威写口只写内存 overlay，实盘与 Git 全程零变化：
//   - 首次触达某路径时从实盘读入并保留原始前像（pre bytes / hash / 是否存在）；
//   - 之后同一预演内对该路径的读取必须看到最新 staged 字节；
//   - accepted write-set 按**首次写入**路径顺序去重，每路径恰一个 FileSpec，
//     目标为最终 staged 字节，新建保持「首次不存在」这一事实。
//
// 本层不发布任何写意图、不提交，也不接 CLI / index：它只负责把一次 plan 执行的
// 最终落盘目标攒成一个可交给后续 S5 提交层的 write-set。
//
// 边界：只覆盖单文件权威写口（Read / Exists / CreateFile / WriteGuarded /
// mutateGuarded 及其之上的 ApplyXxx，以及收件区 detach / register 的原子替换）。
// 目录级枚举（scan / walkMarkdown）仍直读实盘——预演不动实盘，枚举照见原始状态，
// 这与「首次读取来自实盘」同源；索引级一致性属于 M5 范畴，不在本批次。

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrAtomicActive 表示已处于预演模式（不支持嵌套预演）。
var ErrAtomicActive = errors.New("store 已处于原子预演模式")

// AtomicFileSpec 是预演导出的单个待落盘目标。
//
// IsNew=true 表示首次触达该路径时实盘不存在——这一事实供后续 S5 提交层裁决回滚
// 语义（新建走 quarantine，既有文件走前像还原）。同一路径的多个 op 合并为**一个**
// FileSpec：TargetBytes 是最终 staged 字节，前像取首次实盘快照。
type AtomicFileSpec struct {
	Path        string // vault 内相对路径（调用方原样保留）
	TargetBytes []byte // 最终 staged 字节（多 op 合并后的结果）
	TargetHash  string // TargetBytes 的 content_hash
	IsNew       bool   // true 表示首次触达时实盘不存在（新建事实）
	PreBytes    []byte // 首次实盘字节（IsNew 时为 nil）
	PreHash     string // 首次实盘 content_hash（IsNew 时为空）
}

// stagedFile 记录单个路径在一次预演内的前像与最新 staged 字节。
type stagedFile struct {
	rel        string
	preExisted bool
	preBytes   []byte
	preHash    string
	staged     []byte
	stagedHash string
	written    bool // 是否发生过权威写（区分「仅读捕获」与「已 stage」）
}

func (sf *stagedFile) exists() bool { return sf.preExisted || sf.written }

// atomicOverlay 是一次预演的内存状态。
type atomicOverlay struct {
	files map[string]*stagedFile
	order []string // 首次**写入**顺序（accepted write-set 去重与排序依据）
}

// preimage 返回某路径的 staged 记录，首次触达时从实盘捕获前像（读或写都触发）。
// 新建事实（preExisted=false）在首次触达时定死，之后不因 staged 而翻转。
func (o *atomicOverlay) preimage(s *Store, rel string) (*stagedFile, error) {
	key := filepath.Clean(rel)
	if sf := o.files[key]; sf != nil {
		return sf, nil
	}
	abs, err := s.Abs(rel)
	if err != nil {
		return nil, err
	}
	sf := &stagedFile{rel: rel}
	b, err := os.ReadFile(abs)
	switch {
	case err == nil:
		sf.preExisted = true
		sf.preBytes = append([]byte(nil), b...)
		sf.preHash = ContentHash(b)
		// staged 初值镜像前像；权威写发生前，同 plan 读取即读到实盘原样。
		sf.staged = append([]byte(nil), b...)
		sf.stagedHash = sf.preHash
	case errors.Is(err, os.ErrNotExist):
		// 首次不存在：保持 preExisted=false，preBytes/preHash 留空。
	default:
		return nil, err
	}
	o.files[key] = sf
	return sf, nil
}

// stage 把最新字节写入 overlay（首次写入登记进 order），实盘零变化。
func (o *atomicOverlay) stage(s *Store, rel string, data []byte) error {
	sf, err := o.preimage(s, rel)
	if err != nil {
		return err
	}
	if !sf.written {
		sf.written = true
		o.order = append(o.order, filepath.Clean(rel))
	}
	sf.staged = append([]byte(nil), data...)
	sf.stagedHash = ContentHash(sf.staged)
	return nil
}

// BeginAtomic 进入预演模式：此后所有权威写口只写内存 overlay，实盘与 Git 零变化。
func (s *Store) BeginAtomic() error {
	if s.overlay != nil {
		return ErrAtomicActive
	}
	s.overlay = &atomicOverlay{files: map[string]*stagedFile{}}
	return nil
}

// InAtomic 报告是否处于预演模式。
func (s *Store) InAtomic() bool { return s.overlay != nil }

// EndAtomic 退出预演模式并丢弃 overlay（预演从不落盘）。
func (s *Store) EndAtomic() { s.overlay = nil }

// AtomicWriteSet 返回本次预演的 accepted write-set：按首次写入顺序去重，
// 每路径恰一个 FileSpec，目标为最终 staged 字节；仅包含发生过权威写的路径。
// 导出字节为副本（原样，不共享底层数组）；不在预演模式时返回 nil。
func (s *Store) AtomicWriteSet() []AtomicFileSpec {
	if s.overlay == nil {
		return nil
	}
	out := make([]AtomicFileSpec, 0, len(s.overlay.order))
	for _, key := range s.overlay.order {
		sf := s.overlay.files[key]
		if sf == nil || !sf.written {
			continue
		}
		spec := AtomicFileSpec{
			Path:        sf.rel,
			TargetBytes: append([]byte(nil), sf.staged...),
			TargetHash:  sf.stagedHash,
			IsNew:       !sf.preExisted,
		}
		if sf.preExisted {
			spec.PreBytes = append([]byte(nil), sf.preBytes...)
			spec.PreHash = sf.preHash
		}
		out = append(out, spec)
	}
	return out
}

// persist 是所有权威写口的**唯一**落盘入口：正常模式直落实盘（writeAtomic），
// 预演模式只写内存 overlay（实盘零变化）。
func (s *Store) persist(rel, abs string, data []byte, perm os.FileMode) error {
	if s.overlay != nil {
		return s.overlay.stage(s, rel, data)
	}
	return writeAtomic(abs, data, perm)
}

// existsForWrite 是新建写口的存在性判据：预演模式看 overlay（前像存在或已 staged），
// 正常模式看实盘。
func (s *Store) existsForWrite(rel, abs string) (bool, error) {
	if s.overlay != nil {
		sf, err := s.overlay.preimage(s, rel)
		if err != nil {
			return false, err
		}
		return sf.exists(), nil
	}
	if _, err := os.Stat(abs); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return false, nil
}
