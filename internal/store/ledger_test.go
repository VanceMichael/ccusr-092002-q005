package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendReopenAndVerify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.log")
	l, err := Open(path)
	if err != nil {
		t.Fatalf("打开日志失败：%v", err)
	}
	if _, err := l.Append("actor-field", "reading_recorded", map[string]any{"value": "230.5"}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
	if _, err := l.Append("actor-calibration", "reading_corrected", map[string]any{"value": "23.05"}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("链校验失败：%v", err)
	}
	seq, head := l.Head()
	if seq != 2 || head == strings.Repeat("0", 64) {
		t.Fatalf("链尾异常 seq=%d head=%s", seq, head)
	}

	// 重新打开应回放成功。
	l2, err := Open(path)
	if err != nil {
		t.Fatalf("重新打开失败：%v", err)
	}
	if got := len(l2.Events()); got != 2 {
		t.Fatalf("回放事件数 = %d，期望 2", got)
	}
}

func TestTamperDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.log")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append("actor-field", "reading_recorded", map[string]any{"value": "1000"}); err != nil {
		t.Fatal(err)
	}

	// 篡改历史行中的载荷数字。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), `"value":"1000"`, `"value":"9999"`, 1)
	if tampered == string(raw) {
		t.Fatal("测试准备失败：未找到待篡改内容")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path); err == nil {
		t.Fatal("篡改后重新打开应失败，但校验通过了")
	} else if !errorsIs(err, ErrChainBroken) {
		t.Fatalf("期望 ErrChainBroken，得到 %v", err)
	}
}

func errorsIs(err, target error) bool {
	return strings.Contains(err.Error(), target.Error())
}
