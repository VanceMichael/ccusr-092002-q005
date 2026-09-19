// Package domain 实现改造前后节能效果的计算与溯源。
//
// 计算只使用现场实测读数(供应商宣传值从不进入本包);读数以生效值为准,
// 被剔除的读数与应用的更正全部写入结果,供业主核查。指标只能在有效
// (validated)测量窗口与确定的口径下计算发布,窗口/口径的合法性由
// 存储层与 HTTP 层在发布前再次校验。
package domain

import (
	"fmt"
	"math"
	"time"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

// ComputationError 表示当前数据不满足口径要求(覆盖率不足、缺少归一化
// 输入等),HTTP 层在发布时映射为 409。
type ComputationError struct{ Message string }

func (e *ComputationError) Error() string { return e.Message }

// WindowInput 是一个测量窗口的全部输入:窗口本身、该建筑的解析后读数、
// 以及该建筑的室内环境记录(函数内部按窗口时段筛选)。
type WindowInput struct {
	Window     store.MeasurementWindow
	Readings   []store.ResolvedReading
	EnvRecords []store.EnvironmentRecord
}

// ReadingLine 是纳入计算的读数明细。
type ReadingLine struct {
	ReadingID    int64   `json:"reading_id"`
	MeterRef     string  `json:"meter_ref"`
	MeterKind    string  `json:"meter_kind"`
	PeriodStart  string  `json:"period_start"`
	PeriodEnd    string  `json:"period_end"`
	EffectiveKWH float64 `json:"effective_kwh"`
}

// ExcludedLine 是被剔除读数的溯源明细(业主可见)。
type ExcludedLine struct {
	ReadingID   int64   `json:"reading_id"`
	MeterRef    string  `json:"meter_ref"`
	PeriodStart string  `json:"period_start"`
	PeriodEnd   string  `json:"period_end"`
	OriginalKWH float64 `json:"original_kwh"`
	Reason      string  `json:"reason"`
	ExcludedBy  string  `json:"excluded_by"`
	ExcludedAt  string  `json:"excluded_at"`
}

// CorrectionLine 是应用到读数上的数值更正明细(原读数保留)。
type CorrectionLine struct {
	ReadingID    int64   `json:"reading_id"`
	MeterRef     string  `json:"meter_ref"`
	OriginalKWH  float64 `json:"original_kwh"`
	CorrectedKWH float64 `json:"corrected_kwh"`
	Reason       string  `json:"reason"`
	CorrectedBy  string  `json:"corrected_by"`
	CorrectedAt  string  `json:"corrected_at"`
}

// WindowResult 是一个窗口的计算结果与溯源信息。
type WindowResult struct {
	WindowID           int64            `json:"window_id"`
	Phase              string           `json:"phase"`
	PeriodStart        string           `json:"period_start"`
	PeriodEnd          string           `json:"period_end"`
	EnergyKWH          float64          `json:"energy_kwh"`
	CoverageRatio      float64          `json:"coverage_ratio"`
	OccupancyRatio     float64          `json:"occupancy_ratio"`
	OccupancyAssumed   bool             `json:"occupancy_assumed"`
	HeatingDegreeDays  float64          `json:"heating_degree_days"`
	IncludedReadings   []ReadingLine    `json:"included_readings"`
	ExcludedReadings   []ExcludedLine   `json:"excluded_readings"`
	CorrectionsApplied []CorrectionLine `json:"corrections_applied"`
}

// CaliberSnapshot 是计算时口径定义的快照,随结果冻结。
type CaliberSnapshot struct {
	ID                 int64    `json:"id"`
	Code               string   `json:"code"`
	Version            int      `json:"version"`
	MeterKinds         []string `json:"meter_kinds"`
	NormalizeOccupancy bool     `json:"normalize_occupancy"`
	NormalizeHeating   bool     `json:"normalize_heating"`
	MinCoverageRatio   float64  `json:"min_coverage_ratio"`
	Description        string   `json:"description"`
}

// Factors 是应用到报告期能耗上的归一化系数。
type Factors struct {
	Occupancy float64 `json:"occupancy"`
	Heating   float64 `json:"heating"`
}

// Result 是完整的节能计算结果,发布时冻结进报告。
type Result struct {
	Caliber              CaliberSnapshot `json:"caliber"`
	Baseline             WindowResult    `json:"baseline"`
	Reporting            WindowResult    `json:"reporting"`
	Factors              Factors         `json:"factors"`
	AdjustedReportingKWH float64         `json:"adjusted_reporting_kwh"`
	SavingsKWH           float64         `json:"savings_kwh"`
	SavingsPct           float64         `json:"savings_pct"`
	ComputedAt           string          `json:"computed_at"`
}

// Compute 按口径计算改造前后节能效果。任何不满足口径要求的情况都返回
// *ComputationError,由发布流程拒绝发布。
func Compute(caliber store.Caliber, baseline, reporting WindowInput, computedAt time.Time) (Result, error) {
	res := Result{
		Caliber: CaliberSnapshot{
			ID:                 caliber.ID,
			Code:               caliber.Code,
			Version:            caliber.Version,
			MeterKinds:         caliber.MeterKinds,
			NormalizeOccupancy: caliber.NormalizeOccupancy,
			NormalizeHeating:   caliber.NormalizeHeating,
			MinCoverageRatio:   caliber.MinCoverageRatio,
			Description:        caliber.Description,
		},
		ComputedAt: computedAt.UTC().Format(time.RFC3339),
	}

	base, err := computeWindow(caliber, baseline)
	if err != nil {
		return res, fmt.Errorf("基线窗口 %d: %w", baseline.Window.ID, err)
	}
	rep, err := computeWindow(caliber, reporting)
	if err != nil {
		return res, fmt.Errorf("报告期窗口 %d: %w", reporting.Window.ID, err)
	}
	res.Baseline = base
	res.Reporting = rep

	factors := Factors{Occupancy: 1, Heating: 1}
	if caliber.NormalizeOccupancy {
		if base.OccupancyRatio <= 0 || rep.OccupancyRatio <= 0 {
			return res, &ComputationError{Message: "口径要求占用率归一化,但窗口缺少有效的占用率记录"}
		}
		factors.Occupancy = base.OccupancyRatio / rep.OccupancyRatio
	}
	if caliber.NormalizeHeating {
		if base.HeatingDegreeDays <= 0 || rep.HeatingDegreeDays <= 0 {
			return res, &ComputationError{Message: "口径要求采暖度日归一化,但窗口缺少有效的采暖度日记录"}
		}
		factors.Heating = base.HeatingDegreeDays / rep.HeatingDegreeDays
	}
	res.Factors = Factors{Occupancy: round4(factors.Occupancy), Heating: round4(factors.Heating)}

	if base.EnergyKWH <= 0 {
		return res, &ComputationError{Message: "基线窗口在口径纳入范围内没有有效能耗,无法计算节能率"}
	}
	adjusted := rep.EnergyKWH * factors.Occupancy * factors.Heating
	res.AdjustedReportingKWH = round3(adjusted)
	res.SavingsKWH = round3(base.EnergyKWH - adjusted)
	res.SavingsPct = round2((base.EnergyKWH - adjusted) / base.EnergyKWH * 100)
	return res, nil
}

// computeWindow 汇总单个窗口:筛选纳入口径的读数、计算覆盖率与环境量。
func computeWindow(caliber store.Caliber, in WindowInput) (WindowResult, error) {
	w := in.Window
	out := WindowResult{
		WindowID:    w.ID,
		Phase:       w.Phase,
		PeriodStart: w.PeriodStart,
		PeriodEnd:   w.PeriodEnd,
	}
	ws, we := store.MustTime(w.PeriodStart), store.MustTime(w.PeriodEnd)
	windowHours := we.Sub(ws).Hours()

	var covered []interval
	for _, rr := range in.Readings {
		if !containsKind(caliber.MeterKinds, rr.Meter.Kind) {
			continue // 不在口径范围内,既不算纳入也不算剔除
		}
		rs, re := store.MustTime(rr.Reading.PeriodStart), store.MustTime(rr.Reading.PeriodEnd)
		if rs.Before(ws) || re.After(we) {
			continue // 不完全落在窗口内的读数不参与该窗口
		}
		if rr.Excluded {
			last := rr.Corrections[len(rr.Corrections)-1]
			out.ExcludedReadings = append(out.ExcludedReadings, ExcludedLine{
				ReadingID:   rr.Reading.ID,
				MeterRef:    rr.Meter.MeterRef,
				PeriodStart: rr.Reading.PeriodStart,
				PeriodEnd:   rr.Reading.PeriodEnd,
				OriginalKWH: round3(rr.Reading.ReadingKWH),
				Reason:      last.Reason,
				ExcludedBy:  last.CorrectedBy,
				ExcludedAt:  last.CreatedAt,
			})
			continue
		}
		out.IncludedReadings = append(out.IncludedReadings, ReadingLine{
			ReadingID:    rr.Reading.ID,
			MeterRef:     rr.Meter.MeterRef,
			MeterKind:    rr.Meter.Kind,
			PeriodStart:  rr.Reading.PeriodStart,
			PeriodEnd:    rr.Reading.PeriodEnd,
			EffectiveKWH: round3(rr.EffectiveKWH),
		})
		out.EnergyKWH += rr.EffectiveKWH
		covered = append(covered, interval{start: rs, end: re})
		for _, c := range rr.Corrections {
			if c.Action == store.CorrectionAdjust && c.CorrectedKWH != nil {
				out.CorrectionsApplied = append(out.CorrectionsApplied, CorrectionLine{
					ReadingID:    rr.Reading.ID,
					MeterRef:     rr.Meter.MeterRef,
					OriginalKWH:  round3(rr.Reading.ReadingKWH),
					CorrectedKWH: round3(*c.CorrectedKWH),
					Reason:       c.Reason,
					CorrectedBy:  c.CorrectedBy,
					CorrectedAt:  c.CreatedAt,
				})
			}
		}
	}
	out.EnergyKWH = round3(out.EnergyKWH)
	out.CoverageRatio = round4(unionHours(covered) / windowHours)
	if out.CoverageRatio+1e-9 < caliber.MinCoverageRatio {
		return out, &ComputationError{Message: fmt.Sprintf(
			"读数覆盖率 %.4f 低于口径要求的 %.4f,窗口数据不足以发布指标",
			out.CoverageRatio, caliber.MinCoverageRatio)}
	}

	occupancy, assumed := windowOccupancy(in.EnvRecords, ws, we)
	out.OccupancyRatio = round4(occupancy)
	out.OccupancyAssumed = assumed
	out.HeatingDegreeDays = round3(windowHDD(in.EnvRecords, ws, we))
	return out, nil
}

type interval struct{ start, end time.Time }

// unionHours 计算若干时段的并集时长(小时)。
func unionHours(intervals []interval) float64 {
	if len(intervals) == 0 {
		return 0
	}
	sorted := make([]interval, len(intervals))
	copy(sorted, intervals)
	for i := 1; i < len(sorted); i++ { // 插入排序,读数量级下足够
		for j := i; j > 0 && sorted[j].start.Before(sorted[j-1].start); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	total := 0.0
	cur := sorted[0]
	for _, iv := range sorted[1:] {
		if iv.start.After(cur.end) {
			total += cur.end.Sub(cur.start).Hours()
			cur = iv
		} else if iv.end.After(cur.end) {
			cur.end = iv.end
		}
	}
	total += cur.end.Sub(cur.start).Hours()
	return total
}

// windowOccupancy 返回窗口内占用率的重叠时长加权平均;没有记录时返回 1 并标记为假定值。
func windowOccupancy(records []store.EnvironmentRecord, ws, we time.Time) (float64, bool) {
	var weighted, hours float64
	for _, r := range records {
		if r.OccupancyRatio == nil {
			continue
		}
		overlap := overlapHours(r.PeriodStart, r.PeriodEnd, ws, we)
		if overlap <= 0 {
			continue
		}
		weighted += *r.OccupancyRatio * overlap
		hours += overlap
	}
	if hours == 0 {
		return 1, true
	}
	return weighted / hours, false
}

// windowHDD 按记录重叠比例折算窗口内的采暖度日合计。
func windowHDD(records []store.EnvironmentRecord, ws, we time.Time) float64 {
	total := 0.0
	for _, r := range records {
		if r.HeatingDegreeDays == nil {
			continue
		}
		rs, re := store.MustTime(r.PeriodStart), store.MustTime(r.PeriodEnd)
		overlap := overlapHours(r.PeriodStart, r.PeriodEnd, ws, we)
		if overlap <= 0 {
			continue
		}
		total += *r.HeatingDegreeDays * overlap / re.Sub(rs).Hours()
	}
	return total
}

func overlapHours(start, end string, ws, we time.Time) float64 {
	rs, re := store.MustTime(start), store.MustTime(end)
	if rs.Before(ws) {
		rs = ws
	}
	if re.After(we) {
		re = we
	}
	if !rs.Before(re) {
		return 0
	}
	return re.Sub(rs).Hours()
}

func containsKind(kinds []string, kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
