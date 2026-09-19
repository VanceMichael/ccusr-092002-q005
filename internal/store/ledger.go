// Package store 提供仅追加的哈希链事件日志。
//
// 领域内的一切状态变更都以事件形式追加：读数、校准更正、窗口、口径、报告、
// 第三方结论均不可原地修改。每条记录固化上一条的哈希，形成链式证据；
// 对任何历史行的删改都会在校验时断裂。数值一律以十进制字符串放入载荷，
// 避免 JSON 数字在不同语言间的精度歧义。
package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Event 是日志中的一行不可变记录。
type Event struct {
	Seq       int64          `json:"seq"`
	Type      string         `json:"type"`
	Actor     string         `json:"actor"`
	CreatedAt string         `json:"created_at"`
	PrevHash  string         `json:"prev_hash"`
	Hash      string         `json:"hash"`
	Payload   map[string]any `json:"payload"`
}

// Ledger 是进程内回放后的哈希链日志，追加即落盘。
type Ledger struct {
	mu     sync.Mutex
	path   string
	events []Event
}

// ErrChainBroken 表示历史日志被篡改或损坏。
var ErrChainBroken = errors.New("哈希链校验失败：日志存在篡改或损坏")

// Open 打开（必要时创建）日志文件并回放校验整条链。
func Open(path string) (*Ledger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	l := &Ledger{path: path}
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开日志失败: %w", err)
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e Event
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber() // 数字保持原文，避免 float64 损害证据精度
		if err := dec.Decode(&e); err != nil {
			f.Close()
			return nil, fmt.Errorf("%w（第 %d 行无法解析）", ErrChainBroken, len(l.events)+1)
		}
		l.events = append(l.events, e)
	}
	f.Close()
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取日志失败: %w", err)
	}
	if err := l.verifyLocked(); err != nil {
		return nil, err
	}
	return l, nil
}

// Append 在链尾追加一条事件并 fsync 落盘，返回固化后的事件。
func (l *Ledger) Append(actor, eventType string, payload map[string]any) (Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now().Format(time.RFC3339)
	e := Event{
		Seq:       int64(len(l.events)) + 1,
		Type:      eventType,
		Actor:     actor,
		CreatedAt: now,
		Payload:   payload,
	}
	if len(l.events) > 0 {
		e.PrevHash = l.events[len(l.events)-1].Hash
	} else {
		e.PrevHash = strings.Repeat("0", 64)
	}
	h, err := hashEvent(e)
	if err != nil {
		return Event{}, err
	}
	e.Hash = h

	line, err := json.Marshal(e)
	if err != nil {
		return Event{}, err
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return Event{}, fmt.Errorf("打开日志追加失败: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return Event{}, fmt.Errorf("写入日志失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return Event{}, fmt.Errorf("刷新日志失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return Event{}, err
	}
	l.events = append(l.events, e)
	return e, nil
}

// Events 返回全部事件的副本。
func (l *Ledger) Events() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

// Head 返回链尾序号与哈希（空链为 0 与全 0 串），用于报告锚点。
func (l *Ledger) Head() (seq int64, hash string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) == 0 {
		return 0, strings.Repeat("0", 64)
	}
	return l.events[len(l.events)-1].Seq, l.events[len(l.events)-1].Hash
}

// Verify 重新计算整条链并与记录哈希比对。
func (l *Ledger) Verify() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.verifyLocked()
}

func (l *Ledger) verifyLocked() error {
	prev := strings.Repeat("0", 64)
	for i, e := range l.events {
		if e.Seq != int64(i+1) {
			return fmt.Errorf("%w（第 %d 行序号不连续）", ErrChainBroken, i+1)
		}
		if e.PrevHash != prev {
			return fmt.Errorf("%w（第 %d 行 prev_hash 不衔接）", ErrChainBroken, i+1)
		}
		want, err := hashEvent(e)
		if err != nil {
			return fmt.Errorf("%w（第 %d 行 %v）", ErrChainBroken, i+1, err)
		}
		if want != e.Hash {
			return fmt.Errorf("%w（第 %d 行载荷哈希不匹配）", ErrChainBroken, i+1)
		}
		prev = e.Hash
	}
	return nil
}

// hashEvent 以确定性的字段顺序与规范化载荷计算记录哈希。
func hashEvent(e Event) (string, error) {
	canonical, err := canonicalJSON(e.Payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\n%s\n%s\n%s\n%s\n%s",
		e.Seq, e.Type, e.Actor, e.CreatedAt, e.PrevHash, canonical)))
	return hex.EncodeToString(sum[:]), nil
}

// canonicalJSON 生成键有序、无多余空白的 JSON；载荷内的浮点数会被拒绝。
func canonicalJSON(v any) ([]byte, error) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			b.Write(kb)
			b.WriteByte(':')
			vb, err := canonicalJSON(t[k])
			if err != nil {
				return nil, err
			}
			b.Write(vb)
		}
		b.WriteByte('}')
		return []byte(b.String()), nil
	case []any:
		var b strings.Builder
		b.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			vb, err := canonicalJSON(item)
			if err != nil {
				return nil, err
			}
			b.Write(vb)
		}
		b.WriteByte(']')
		return []byte(b.String()), nil
	case json.Number:
		// 数字以原文固化（UseNumber 解码得到），杜绝浮点改写。
		return []byte(t.String()), nil
	case float64, float32:
		// 领域约定数值为十进制字符串或经 json.Number 保留原文；
		// 出现裸浮点说明上游构造错误。
		return nil, fmt.Errorf("载荷中不允许浮点数 %v，请改用十进制字符串", t)
	default:
		return json.Marshal(v)
	}
}
