package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// 时间格式两类（技术方案 §4）：
//
//   - created_at            → YYYY-MM-DD（Date）
//   - updated_at / reviewed_at / deleted_at / saved_at / generated_at / attempted_at
//     → **带时区** RFC3339（Stamp）
//
// 理由：`updated_at > reviewed_at` 需要在同日多次编辑时仍可比较，因此除 created_at 外
// 一律用带时区的时刻，且**不接受**缺时区的写法。

// DateLayout 是 created_at 的唯一合法布局。
const DateLayout = "2006-01-02"

// Date 是只到日的日期（created_at）。带时间部分即报错。
type Date struct{ t time.Time }

// ParseDate 严格解析 YYYY-MM-DD。
func ParseDate(raw string) (Date, error) {
	if len(raw) != len(DateLayout) {
		return Date{}, fmt.Errorf("非法日期 %q：created_at 只接受 %s（10 个字符，不含时间与时区）",
			raw, "YYYY-MM-DD")
	}
	t, err := time.Parse(DateLayout, raw)
	if err != nil {
		return Date{}, fmt.Errorf("非法日期 %q：created_at 只接受 YYYY-MM-DD（%v）", raw, err)
	}
	return Date{t: t}, nil
}

// NewDate 由 time.Time 取其日期部分（UTC 语义由调用方决定）。
func NewDate(t time.Time) Date {
	y, m, d := t.Date()
	return Date{t: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

func (d Date) IsZero() bool    { return d.t.IsZero() }
func (d Date) Time() time.Time { return d.t }

func (d Date) String() string {
	if d.t.IsZero() {
		return ""
	}
	return d.t.Format(DateLayout)
}

// Compact 返回 ID 里用的 yyyymmdd 形态。
func (d Date) Compact() string {
	if d.t.IsZero() {
		return ""
	}
	return d.t.Format("20060102")
}

func (d *Date) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	v, err := ParseDate(raw)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

func (d *Date) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	v, err := ParseDate(raw)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// MarshalJSON 只用于报告 / --json 输出（JSON 是 CLI 输出格式，不是 YAML 写路径）。
func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// Stamp 是带时区的 RFC3339 时刻。缺时区即报错。
type Stamp struct{ t time.Time }

// ParseStamp 严格解析带时区的 RFC3339。
func ParseStamp(raw string) (Stamp, error) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return Stamp{}, fmt.Errorf(
			"非法时刻 %q：只接受**带时区**的 RFC3339（如 2026-09-01T10:30:00+08:00 / …Z）（%v）",
			raw, err)
	}
	return Stamp{t: t}, nil
}

// NewStamp 包装一个 time.Time。
func NewStamp(t time.Time) Stamp { return Stamp{t: t} }

func (s Stamp) IsZero() bool    { return s.t.IsZero() }
func (s Stamp) Time() time.Time { return s.t }

// Before 供 updated_at > reviewed_at 之类的比较使用。
func (s Stamp) Before(o Stamp) bool { return s.t.Before(o.t) }

func (s Stamp) String() string {
	if s.t.IsZero() {
		return ""
	}
	return s.t.Format(time.RFC3339)
}

func (s *Stamp) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	v, err := ParseStamp(raw)
	if err != nil {
		return err
	}
	*s = v
	return nil
}

func (s *Stamp) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	v, err := ParseStamp(raw)
	if err != nil {
		return err
	}
	*s = v
	return nil
}

// MarshalJSON 只用于报告 / --json 输出。
func (s Stamp) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }
