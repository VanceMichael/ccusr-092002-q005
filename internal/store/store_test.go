package store

import (
	"path/filepath"
	"testing"
)

func mustBuilding(t *testing.T, s *Store, ref string) Building {
	t.Helper()
	b, err := s.CreateBuilding(Building{BuildingRef: ref, Name: "示例楼", CreatedBy: "tester"})
	if err != nil {
		t.Fatalf("创建建筑失败: %v", err)
	}
	return b
}

func TestPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	b := mustBuilding(t, s, "B-1")
	m, err := s.CreateMeter(Meter{BuildingID: b.ID, MeterRef: "M-1", Kind: "heat", CreatedBy: "tester"})
	if err != nil {
		t.Fatalf("创建仪表失败: %v", err)
	}
	if _, err := s.CreateReading(Reading{
		MeterID: m.ID, PeriodStart: "2025-01-01T00:00:00+08:00", PeriodEnd: "2025-02-01T00:00:00+08:00",
		ReadingKWH: 1000, RecordedBy: "tester",
	}); err != nil {
		t.Fatalf("创建读数失败: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("重新打开存储失败: %v", err)
	}
	readings, err := reopened.ListReadings(b.ID)
	if err != nil || len(readings) != 1 || readings[0].ReadingKWH != 1000 {
		t.Fatalf("重启后读数丢失: %v %+v", err, readings)
	}
}

func TestCorrectionPreservesOriginal(t *testing.T) {
	s, _ := Open(":memory:")
	b := mustBuilding(t, s, "B-1")
	m, _ := s.CreateMeter(Meter{BuildingID: b.ID, MeterRef: "M-1", Kind: "heat", CreatedBy: "tester"})
	r, _ := s.CreateReading(Reading{
		MeterID: m.ID, PeriodStart: "2025-01-01T00:00:00+08:00", PeriodEnd: "2025-01-16T00:00:00+08:00",
		ReadingKWH: 500, RecordedBy: "tester",
	})
	corrected := 480.0
	if _, err := s.AddCorrection(Correction{
		ReadingID: r.ID, Action: CorrectionAdjust, CorrectedKWH: &corrected,
		Reason: "表计倍率错误", CorrectedBy: "cal-1",
	}); err != nil {
		t.Fatalf("更正失败: %v", err)
	}
	resolved, _ := s.ResolveReadings(b.ID)
	if len(resolved) != 1 {
		t.Fatalf("读数缺失: %+v", resolved)
	}
	if resolved[0].Reading.ReadingKWH != 500 {
		t.Fatalf("原读数被改写: %v", resolved[0].Reading.ReadingKWH)
	}
	if resolved[0].EffectiveKWH != 480 {
		t.Fatalf("生效值错误: %v", resolved[0].EffectiveKWH)
	}
	if len(resolved[0].Corrections) != 1 {
		t.Fatal("更正链缺失")
	}
}

func TestExcludeThenAdjustLatestWins(t *testing.T) {
	s, _ := Open(":memory:")
	b := mustBuilding(t, s, "B-1")
	m, _ := s.CreateMeter(Meter{BuildingID: b.ID, MeterRef: "M-1", Kind: "heat", CreatedBy: "tester"})
	r, _ := s.CreateReading(Reading{
		MeterID: m.ID, PeriodStart: "2025-01-01T00:00:00+08:00", PeriodEnd: "2025-01-16T00:00:00+08:00",
		ReadingKWH: 500, RecordedBy: "tester",
	})
	if _, err := s.AddCorrection(Correction{ReadingID: r.ID, Action: CorrectionExclude, Reason: "疑似串表", CorrectedBy: "cal-1"}); err != nil {
		t.Fatal(err)
	}
	resolved, _ := s.ResolveReadings(b.ID)
	if !resolved[0].Excluded {
		t.Fatal("剔除后应标记为 excluded")
	}
	restored := 495.0
	if _, err := s.AddCorrection(Correction{
		ReadingID: r.ID, Action: CorrectionAdjust, CorrectedKWH: &restored,
		Reason: "复核后恢复并修正", CorrectedBy: "cal-2",
	}); err != nil {
		t.Fatal(err)
	}
	resolved, _ = s.ResolveReadings(b.ID)
	if resolved[0].Excluded || resolved[0].EffectiveKWH != 495 {
		t.Fatalf("最新更正未生效: %+v", resolved[0])
	}
	if len(resolved[0].Corrections) != 2 {
		t.Fatal("完整更正链必须保留")
	}
}

func TestDuplicateBuildingRefRejected(t *testing.T) {
	s, _ := Open(":memory:")
	mustBuilding(t, s, "B-1")
	if _, err := s.CreateBuilding(Building{BuildingRef: "B-1", Name: "重复", CreatedBy: "x"}); err == nil {
		t.Fatal("重复 building_ref 应被拒绝")
	}
}

func TestPublishedReportLocksWindowAndCaliber(t *testing.T) {
	s, _ := Open(":memory:")
	b := mustBuilding(t, s, "B-1")
	mkWindow := func(phase, start, end string) MeasurementWindow {
		w, err := s.CreateWindow(MeasurementWindow{BuildingID: b.ID, Phase: phase, PeriodStart: start, PeriodEnd: end, CreatedBy: "insp"})
		if err != nil {
			t.Fatalf("创建窗口失败: %v", err)
		}
		if _, err := s.SetWindowStatus(w.ID, WindowValidated, "insp"); err != nil {
			t.Fatalf("审核窗口失败: %v", err)
		}
		return w
	}
	bw := mkWindow(PhaseBaseline, "2025-01-01T00:00:00+08:00", "2025-02-01T00:00:00+08:00")
	rw := mkWindow(PhaseReporting, "2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00")
	cal, err := s.CreateCaliber(Caliber{Code: "GB", MeterKinds: []string{"heat"}, MinCoverageRatio: 0.9, CreatedBy: "insp"})
	if err != nil {
		t.Fatalf("创建口径失败: %v", err)
	}
	rep, err := s.CreateReport(Report{
		BuildingID: b.ID, CaliberID: cal.ID,
		BaselineWindowID: bw.ID, ReportingWindowID: rw.ID, CreatedBy: "insp",
	})
	if err != nil {
		t.Fatalf("创建报告失败: %v", err)
	}
	if _, err := s.PublishReport(rep.ID, []byte(`{"ok":true}`), "insp"); err != nil {
		t.Fatalf("发布失败: %v", err)
	}

	if _, err := s.PublishReport(rep.ID, []byte(`{"ok":true}`), "insp"); err == nil {
		t.Fatal("已发布报告不得再次发布")
	}
	if _, err := s.SetWindowStatus(bw.ID, WindowInvalid, "insp"); err == nil {
		t.Fatal("被已发布报告引用的窗口不得变更状态")
	}
	if _, err := s.RetireCaliber(cal.ID); err == nil {
		t.Fatal("被已发布报告引用的口径不得退役")
	}
	frozen, _ := s.GetReport(rep.ID)
	if string(frozen.Result) != `{"ok":true}` {
		t.Fatalf("冻结结果被改写: %s", frozen.Result)
	}
}

func TestPublishRequiresValidatedWindows(t *testing.T) {
	s, _ := Open(":memory:")
	b := mustBuilding(t, s, "B-1")
	bw, _ := s.CreateWindow(MeasurementWindow{BuildingID: b.ID, Phase: PhaseBaseline, PeriodStart: "2025-01-01T00:00:00+08:00", PeriodEnd: "2025-02-01T00:00:00+08:00", CreatedBy: "insp"})
	rw, _ := s.CreateWindow(MeasurementWindow{BuildingID: b.ID, Phase: PhaseReporting, PeriodStart: "2026-01-01T00:00:00+08:00", PeriodEnd: "2026-02-01T00:00:00+08:00", CreatedBy: "insp"})
	cal, _ := s.CreateCaliber(Caliber{Code: "GB", MeterKinds: []string{"heat"}, MinCoverageRatio: 0.9, CreatedBy: "insp"})
	rep, _ := s.CreateReport(Report{BuildingID: b.ID, CaliberID: cal.ID, BaselineWindowID: bw.ID, ReportingWindowID: rw.ID, CreatedBy: "insp"})
	if _, err := s.PublishReport(rep.ID, []byte(`{}`), "insp"); err == nil {
		t.Fatal("窗口未审核时不得发布")
	}
}

func TestCaliberVersionsIncrement(t *testing.T) {
	s, _ := Open(":memory:")
	v1, _ := s.CreateCaliber(Caliber{Code: "GB", MeterKinds: []string{"heat"}, MinCoverageRatio: 0.9, CreatedBy: "insp"})
	v2, _ := s.CreateCaliber(Caliber{Code: "GB", MeterKinds: []string{"heat", "electricity"}, MinCoverageRatio: 0.8, CreatedBy: "insp"})
	if v1.Version != 1 || v2.Version != 2 {
		t.Fatalf("口径版本应递增: %d, %d", v1.Version, v2.Version)
	}
}
