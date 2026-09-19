package archive

import (
	"fmt"
	"math/big"
	"time"
)

// decimal 是基于 big.Rat 的精确十进制数。领域内能耗、节能率等关键数字
// 一律以十进制字符串传输和落盘，计算用有理数，只在最终展示时舍入。
type decimal struct {
	r *big.Rat
}

func zeroDecimal() decimal { return decimal{r: new(big.Rat)} }

func mustParseDecimal(s string) decimal {
	d, err := parseDecimal(s)
	if err != nil {
		panic(err)
	}
	return d
}

func parseDecimal(s string) (decimal, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return decimal{}, fmt.Errorf("无法解析为数字：%q", s)
	}
	return decimal{r: r}, nil
}

func (d decimal) add(o decimal) decimal { return decimal{r: new(big.Rat).Add(d.r, o.r)} }
func (d decimal) sub(o decimal) decimal { return decimal{r: new(big.Rat).Sub(d.r, o.r)} }
func (d decimal) mul(o decimal) decimal { return decimal{r: new(big.Rat).Mul(d.r, o.r)} }
func (d decimal) quo(o decimal) decimal { return decimal{r: new(big.Rat).Quo(d.r, o.r)} }
func (d decimal) sign() int             { return d.r.Sign() }

// fixed 返回小数点后 prec 位的字符串（big.Rat 的最近舍入）。
func (d decimal) fixed(prec int) string { return d.r.FloatString(prec) }

func (d decimal) isZero() bool { return d.r.Sign() == 0 }

// ratSeconds 把时间段表示为秒的精确分数。
func ratSeconds(d time.Duration) decimal {
	return decimal{r: new(big.Rat).SetFrac(big.NewInt(d.Nanoseconds()), big.NewInt(1_000_000_000))}
}
