// Package store 提供测量档案的持久化存储。
//
// 当前运行时使用内嵌 JSON 快照存储(纯标准库实现,落盘到数据库文件),
// migrations/ 下的 SQL 文件是同一数据模型的关系模式契约;存储层通过
// 本包的方法与上层解耦,未来可在不改动 HTTP 层的情况下替换为 SQLite 后端。
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SchemaVersion 是快照格式版本,与 migrations/ 的最大版本号对应。
const SchemaVersion = 2

// ErrNotFound 表示按标识查询的记录不存在。
var ErrNotFound = errors.New("record not found")

// ConflictError 表示违反档案不变量(重复引用、状态不允许等),HTTP 层映射为 409。
type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }

// ValidationError 表示字段级校验失败,HTTP 层映射为 400。
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// snapshot 是落盘的完整数据视图。
type snapshot struct {
	SchemaVersion int                    `json:"schema_version"`
	NextIDs       map[string]int64       `json:"next_ids"`
	Buildings     []Building             `json:"buildings"`
	Baselines     []Baseline             `json:"baselines"`
	Meters        []Meter                `json:"meters"`
	Readings      []Reading              `json:"readings"`
	Corrections   []Correction           `json:"corrections"`
	Replacements  []EquipmentReplacement `json:"equipment_replacements"`
	Claims        []VendorClaim          `json:"vendor_claims"`
	EnvRecords    []EnvironmentRecord    `json:"environment_records"`
	Windows       []MeasurementWindow    `json:"measurement_windows"`
	Calibers      []Caliber              `json:"calibers"`
	Reports       []Report               `json:"reports"`
}

// Store 是并发安全的档案存储。所有写操作立即落盘(临时文件 + 原子改名)。
type Store struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
	data snapshot
}

// Open 打开(必要时初始化)位于 path 的档案文件。path 为 ":memory:" 时仅驻留内存,
// 供测试使用。
func Open(path string) (*Store, error) {
	s := &Store{path: path, now: time.Now}
	s.data = snapshot{SchemaVersion: SchemaVersion, NextIDs: map[string]int64{}}
	if path == ":memory:" {
		return s, nil
	}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, fmt.Errorf("读取档案文件失败: %w", err)
	default:
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &s.data); err != nil {
				return nil, fmt.Errorf("档案文件损坏或格式不兼容: %w", err)
			}
			if s.data.NextIDs == nil {
				s.data.NextIDs = map[string]int64{}
			}
		}
	}
	return s, nil
}

// SetClock 替换时间源,仅供测试获得确定性时间戳。
func (s *Store) SetClock(now func() time.Time) { s.now = now }

func (s *Store) timestamp() string { return s.now().UTC().Format(time.RFC3339) }

func (s *Store) nextIDLocked(kind string) int64 {
	s.data.NextIDs[kind]++
	return s.data.NextIDs[kind]
}

// persistLocked 将快照原子写入磁盘。调用时必须已持有锁。
func (s *Store) persistLocked() error {
	if s.path == ":memory:" {
		return nil
	}
	raw, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化档案失败: %w", err)
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建档案目录失败: %w", err)
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("写入临时档案失败: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("替换档案文件失败: %w", err)
	}
	return nil
}

// mutate 执行一次写操作并落盘。
func (s *Store) mutate(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(); err != nil {
		return err
	}
	return s.persistLocked()
}

// ParsePeriod 校验 ISO 8601(带时区偏移)时段,要求 start < end。
func ParsePeriod(start, end string) (time.Time, time.Time, error) {
	s, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return time.Time{}, time.Time{}, &ValidationError{Message: "period_start 必须是带时区偏移的 ISO 8601 时间"}
	}
	e, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return time.Time{}, time.Time{}, &ValidationError{Message: "period_end 必须是带时区偏移的 ISO 8601 时间"}
	}
	if !s.Before(e) {
		return time.Time{}, time.Time{}, &ValidationError{Message: "period_start 必须早于 period_end"}
	}
	return s, e, nil
}

// MustTime 解析已入库的时间串;入库前均经过 ParsePeriod 校验。
func MustTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		panic(fmt.Sprintf("档案中存在非法时间 %q: %v", v, err))
	}
	return t
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// sortedByID 返回按 ID 升序的拷贝,保证对外输出顺序稳定。
func sortedByID[T interface{ GetID() int64 }](items []T) []T {
	out := make([]T, len(items))
	copy(out, items)
	sort.Slice(out, func(i, j int) bool { return out[i].GetID() < out[j].GetID() })
	return out
}

func (b Building) GetID() int64             { return b.ID }
func (b Baseline) GetID() int64             { return b.ID }
func (m Meter) GetID() int64                { return m.ID }
func (r Reading) GetID() int64              { return r.ID }
func (c Correction) GetID() int64           { return c.ID }
func (r EquipmentReplacement) GetID() int64 { return r.ID }
func (c VendorClaim) GetID() int64          { return c.ID }
func (r EnvironmentRecord) GetID() int64    { return r.ID }
func (w MeasurementWindow) GetID() int64    { return w.ID }
func (c Caliber) GetID() int64              { return c.ID }
func (r Report) GetID() int64               { return r.ID }
