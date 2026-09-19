package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

func ptr(v float64) *float64 { return &v }

func window(id int64, phase, start, end string) store.MeasurementWindow {
	return store.MeasurementWindow{ID: id, Phase: phase, PeriodStart: start, PeriodEnd: end, Status: store.WindowValidated}
}

func reading(id int64, meterKind, start, end string, kwh float64) store.ResolvedReading {
	return store.ResolvedReading{
		Reading:      store.Reading{ID: id, PeriodStart: start, PeriodEnd: end, ReadingKWH: kwh},
		Meter:        store.Meter{MeterRef: "M", Kind: meterKind},
		EffectiveKWH: kwh,
	}
}

var (
	baseWindow = window(1, store.PhaseBaseline, "2025-01-01T00:00:00+08:00", "2025-02-01T00:00:00+08:00")
	repWindow  = window(2, store.PhaseReporting, "2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00")
	now        = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
)

func baseCaliber() store.Caliber {
	return store.Caliber{
		ID: 1, Code: "GB", Version: 1,
		MeterKinds:       []string{"heat"},
		MinCoverageRatio: 0.9,
		Status:           store.CaliberActive,
	}
}

func TestComputeBasicSavings(t *testing.T) {
	base := WindowInput{Window: baseWindow, Readings: []store.ResolvedReading{
		reading(1, "heat", "2025-01-01T00:00:00+08:00", "2025-01-16T00:00:00+08:00", 500),
		reading(2, "heat", "2025-01-16T00:00:00+08:00", "2025-02-01T00:00:00+08:00", 520),
	}}
	rep := WindowInput{Window: repWindow, Readings: []store.ResolvedReading{
		reading(3, "heat", "2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00", 700),
	}}
	res, err := Compute(baseCaliber(), base, rep, now)
	if err != nil {
		t.Fatalf("计算失败: %v", err)
	}
	if res.Baseline.EnergyKWH != 1020 || res.Reporting.EnergyKWH != 700 {
		t.Fatalf("能耗汇总错误: baseline=%v reporting=%v", res.Baseline.EnergyKWH, res.Reporting.EnergyKWH)
	}
	if res.SavingsKWH != 320 {
		t.Fatalf("节能量错误: %v", res.SavingsKWH)
	}
	if res.SavingsPct != 31.37 {
		t.Fatalf("节能率错误: %v", res.SavingsPct)
	}
	if res.Baseline.CoverageRatio != 1 || res.Reporting.CoverageRatio != 1 {
		t.Fatalf("覆盖率应为 1: %v / %v", res.Baseline.CoverageRatio, res.Reporting.CoverageRatio)
	}
	if !res.Baseline.OccupancyAssumed {
		t.Fatal("无环境记录时占用率应标记为假定值")
	}
}

func TestComputeNormalization(t *testing.T) {
	caliber := baseCaliber()
	caliber.NormalizeOccupancy = true
	caliber.NormalizeHeating = true
	env := []store.EnvironmentRecord{
		{PeriodStart: "2025-01-01T00:00:00+08:00", PeriodEnd: "2025-02-01T00:00:00+08:00", OccupancyRatio: ptr(0.8), HeatingDegreeDays: ptr(600)},
		{PeriodStart: "2026-01-01T00:00:00+08:00", PeriodEnd: "2026-02-01T00:00:00+08:00", OccupancyRatio: ptr(0.9), HeatingDegreeDays: ptr(700)},
	}
	base := WindowInput{Window: baseWindow, EnvRecords: env, Readings: []store.ResolvedReading{
		reading(1, "heat", "2025-01-01T00:00:00+08:00", "2025-02-01T00:00:00+08:00", 1000),
	}}
	rep := WindowInput{Window: repWindow, EnvRecords: env, Readings: []store.ResolvedReading{
		reading(2, "heat", "2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00", 700),
	}}
	res, err := Compute(caliber, base, rep, now)
	if err != nil {
		t.Fatalf("计算失败: %v", err)
	}
	// 占用率系数 0.8/0.9,采暖度日系数 600/700,调整后报告期 ≈ 533.333
	if res.AdjustedReportingKWH != 533.333 {
		t.Fatalf("归一化后报告期能耗错误: %v", res.AdjustedReportingKWH)
	}
	if res.SavingsKWH != 466.667 {
		t.Fatalf("节能量错误: %v", res.SavingsKWH)
	}
	if res.Factors.Occupancy != 0.8889 || res.Factors.Heating != 0.8571 {
		t.Fatalf("归一化系数错误: %+v", res.Factors)
	}
}

func TestComputeExcludedAndCorrectedReadings(t *testing.T) {
	excluded := reading(2, "heat", "2025-01-01T00:00:00+08:00", "2025-01-16T00:00:00+08:00", 999)
	excluded.Excluded = true
	excluded.Corrections = []store.Correction{
		{ID: 1, ReadingID: 2, Action: store.CorrectionExclude, Reason: "仪表故障", CorrectedBy: "cal-1", CreatedAt: "2025-03-01T00:00:00Z"},
	}
	corrected := reading(1, "heat", "2025-01-01T00:00:00+08:00", "2025-01-16T00:00:00+08:00", 500)
	corrected.EffectiveKWH = 480
	corrected.Corrections = []store.Correction{
		{ID: 2, ReadingID: 1, Action: store.CorrectionAdjust, CorrectedKWH: ptr(480), Reason: "表计倍率错误", CorrectedBy: "cal-1", CreatedAt: "2025-03-01T00:00:00Z"},
	}
	base := WindowInput{Window: baseWindow, Readings: []store.ResolvedReading{
		corrected, excluded,
		reading(3, "heat", "2025-01-16T00:00:00+08:00", "2025-02-01T00:00:00+08:00", 520),
	}}
	rep := WindowInput{Window: repWindow, Readings: []store.ResolvedReading{
		reading(4, "heat", "2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00", 700),
	}}
	res, err := Compute(baseCaliber(), base, rep, now)
	if err != nil {
		t.Fatalf("计算失败: %v", err)
	}
	if res.Baseline.EnergyKWH != 1000 {
		t.Fatalf("生效值求和错误(应为 480+520=1000): %v", res.Baseline.EnergyKWH)
	}
	if len(res.Baseline.ExcludedReadings) != 1 || res.Baseline.ExcludedReadings[0].ReadingID != 2 {
		t.Fatalf("剔除溯源缺失: %+v", res.Baseline.ExcludedReadings)
	}
	if res.Baseline.ExcludedReadings[0].OriginalKWH != 999 {
		t.Fatal("剔除记录必须保留原读数")
	}
	if len(res.Baseline.CorrectionsApplied) != 1 || res.Baseline.CorrectionsApplied[0].CorrectedKWH != 480 {
		t.Fatalf("更正溯源缺失: %+v", res.Baseline.CorrectionsApplied)
	}
}

func TestComputeCoverageInsufficient(t *testing.T) {
	base := WindowInput{Window: baseWindow, Readings: []store.ResolvedReading{
		reading(1, "heat", "2025-01-01T00:00:00+08:00", "2025-01-06T00:00:00+08:00", 100),
	}}
	rep := WindowInput{Window: repWindow, Readings: []store.ResolvedReading{
		reading(2, "heat", "2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00", 700),
	}}
	_, err := Compute(baseCaliber(), base, rep, now)
	if err == nil || !strings.Contains(err.Error(), "覆盖率") {
		t.Fatalf("覆盖率不足应报错,实际: %v", err)
	}
}

func TestComputeMissingNormalizationInputs(t *testing.T) {
	caliber := baseCaliber()
	caliber.NormalizeHeating = true
	full := []store.ResolvedReading{
		reading(1, "heat", "2025-01-01T00:00:00+08:00", "2025-02-01T00:00:00+08:00", 1000),
	}
	rep := []store.ResolvedReading{
		reading(2, "heat", "2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00", 700),
	}
	_, err := Compute(caliber, WindowInput{Window: baseWindow, Readings: full}, WindowInput{Window: repWindow, Readings: rep}, now)
	if err == nil || !strings.Contains(err.Error(), "采暖度日") {
		t.Fatalf("缺少采暖度日记录应报错,实际: %v", err)
	}
}

func TestComputeIgnoresOutOfScopeReadings(t *testing.T) {
	base := WindowInput{Window: baseWindow, Readings: []store.ResolvedReading{
		reading(1, "heat", "2025-01-01T00:00:00+08:00", "2025-02-01T00:00:00+08:00", 1000),
		reading(2, "electricity", "2025-01-01T00:00:00+08:00", "2025-02-01T00:00:00+08:00", 500), // 口径外类型
		reading(3, "heat", "2024-12-01T00:00:00+08:00", "2025-01-01T00:00:00+08:00", 800),        // 窗口外
	}}
	rep := WindowInput{Window: repWindow, Readings: []store.ResolvedReading{
		reading(4, "heat", "2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00", 700),
	}}
	res, err := Compute(baseCaliber(), base, rep, now)
	if err != nil {
		t.Fatalf("计算失败: %v", err)
	}
	if res.Baseline.EnergyKWH != 1000 {
		t.Fatalf("口径外与窗口外读数不应计入: %v", res.Baseline.EnergyKWH)
	}
	if len(res.Baseline.ExcludedReadings) != 0 {
		t.Fatal("口径外读数不算被剔除,不应出现在剔除清单")
	}
}
