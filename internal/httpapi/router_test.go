package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	NewHandler(mustStore(t)).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("健康接口状态码为 %d", response.Code)
	}
}

func mustStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	return st
}

// call 发送一次带操作者头的 JSON 请求。
func call(t *testing.T, h http.Handler, method, path, role, actorID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if actorID != "" {
		req.Header.Set("X-Actor-Id", actorID)
	}
	if role != "" {
		req.Header.Set("X-Actor-Role", role)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("响应不是合法 JSON: %v\n%s", err, rec.Body.String())
	}
}

func mustCreate(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("%s 失败,状态码 %d: %s", what, rec.Code, rec.Body.String())
	}
}

// setupArchive 建立完整档案:建筑、基线、仪表、读数(含一条待更正、一条待剔除)、
// 室内环境记录、设备更换与宣传值、两个已审核窗口、两版口径。
func setupArchive(t *testing.T, h http.Handler) (readingAdjustID, readingExcludeID, reportCaliberV1, caliberV2 int64) {
	t.Helper()
	mustCreate(t, call(t, h, "POST", "/buildings", "field_team", "ft-1", map[string]any{
		"building_ref": "GBD-DEMO-1", "name": "示范住宅楼", "address": "高碑店市", "floor_area_m2": 3200,
	}), "创建建筑")
	mustCreate(t, call(t, h, "POST", "/buildings/GBD-DEMO-1/baselines", "field_team", "ft-1", map[string]any{
		"period_start": "2025-01-01T00:00:00+08:00", "period_end": "2025-02-01T00:00:00+08:00",
		"occupancy_ratio": 0.8, "heating_degree_days": 600, "note": "改造前采暖季",
	}), "创建基线")
	mustCreate(t, call(t, h, "POST", "/buildings/GBD-DEMO-1/meters", "field_team", "ft-1", map[string]any{
		"meter_ref": "METER-HEAT", "kind": "heat",
	}), "创建仪表")

	post := func(body map[string]any) int64 {
		rec := call(t, h, "POST", "/buildings/GBD-DEMO-1/readings", "field_team", "ft-1", body)
		mustCreate(t, rec, "创建读数")
		var resp store.Reading
		decodeBody(t, rec, &resp)
		return resp.ID
	}
	readingAdjustID = post(map[string]any{"meter_ref": "METER-HEAT", "period_start": "2025-01-01T00:00:00+08:00", "period_end": "2025-01-16T00:00:00+08:00", "reading_kwh": 500})
	_ = post(map[string]any{"meter_ref": "METER-HEAT", "period_start": "2025-01-16T00:00:00+08:00", "period_end": "2025-02-01T00:00:00+08:00", "reading_kwh": 520})
	readingExcludeID = post(map[string]any{"meter_ref": "METER-HEAT", "period_start": "2025-01-01T00:00:00+08:00", "period_end": "2025-01-16T00:00:00+08:00", "reading_kwh": 999})
	_ = post(map[string]any{"meter_ref": "METER-HEAT", "period_start": "2026-01-01T00:00:00+08:00", "period_end": "2026-02-01T00:00:00+08:00", "reading_kwh": 700})

	// 校准人员:更正一条、剔除一条,原读数保留
	mustCreate(t, call(t, h, "POST", fmt.Sprintf("/readings/%d/corrections", readingAdjustID), "calibrator", "cal-1", map[string]any{
		"action": "adjust", "corrected_kwh": 480, "reason": "表计倍率错误",
	}), "更正读数")
	mustCreate(t, call(t, h, "POST", fmt.Sprintf("/readings/%d/corrections", readingExcludeID), "calibrator", "cal-1", map[string]any{
		"action": "exclude", "reason": "仪表故障期间读数",
	}), "剔除读数")

	env := func(start, end string, occ, hdd float64) {
		mustCreate(t, call(t, h, "POST", "/buildings/GBD-DEMO-1/environment-records", "field_team", "ft-1", map[string]any{
			"period_start": start, "period_end": end, "indoor_temp_c": 21.5,
			"occupancy_ratio": occ, "heating_degree_days": hdd,
		}), "创建环境记录")
	}
	env("2025-01-01T00:00:00+08:00", "2025-02-01T00:00:00+08:00", 0.8, 600)
	env("2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00", 0.9, 700)

	// 供应商登记设备更换与宣传值
	rec := call(t, h, "POST", "/buildings/GBD-DEMO-1/equipment-replacements", "vendor", "vendor-1", map[string]any{
		"equipment_kind": "heat_pump", "removed_model": "OLD-BOILER", "installed_model": "HP-3000",
		"vendor_ref": "VENDOR-X", "replaced_at": "2025-06-15T00:00:00+08:00",
	})
	mustCreate(t, rec, "创建设备更换")
	var replacement store.EquipmentReplacement
	decodeBody(t, rec, &replacement)
	mustCreate(t, call(t, h, "POST", fmt.Sprintf("/equipment-replacements/%d/claims", replacement.ID), "vendor", "vendor-1", map[string]any{
		"claim_kind": "savings_pct", "claim_value": 55, "unit": "percent",
	}), "创建宣传值")

	// 第三方:两个窗口并审核通过
	mkWindow := func(phase, start, end string) int64 {
		rec := call(t, h, "POST", "/buildings/GBD-DEMO-1/windows", "inspector", "insp-1", map[string]any{
			"phase": phase, "period_start": start, "period_end": end,
		})
		mustCreate(t, rec, "创建窗口")
		var win store.MeasurementWindow
		decodeBody(t, rec, &win)
		mustCreate(t, call(t, h, "POST", fmt.Sprintf("/windows/%d/status", win.ID), "inspector", "insp-1", map[string]any{
			"status": "validated",
		}), "审核窗口")
		return win.ID
	}
	baseWin := mkWindow("baseline", "2025-01-01T00:00:00+08:00", "2025-02-01T00:00:00+08:00")
	repWin := mkWindow("reporting", "2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00")

	// 两版口径:v1 不归一化,v2 增加占用率与采暖度日归一化
	mkCaliber := func(normalize bool) int64 {
		rec := call(t, h, "POST", "/calibers", "inspector", "insp-1", map[string]any{
			"code": "GB-SAVINGS", "meter_kinds": []string{"heat"},
			"normalize_occupancy": normalize, "normalize_heating": normalize,
			"min_coverage_ratio": 0.9, "description": "采暖能耗节能量",
		})
		mustCreate(t, rec, "创建口径")
		var cal store.Caliber
		decodeBody(t, rec, &cal)
		return cal.ID
	}
	reportCaliberV1 = mkCaliber(false)
	caliberV2 = mkCaliber(true)

	// 保存窗口 id 供后续用例复用(通过包级变量会串测试,改用返回值外的查询)
	windowsRec := call(t, h, "GET", "/buildings/GBD-DEMO-1/windows", "owner", "owner-1", nil)
	if windowsRec.Code != http.StatusOK {
		t.Fatalf("查询窗口失败: %s", windowsRec.Body.String())
	}
	_ = baseWin
	_ = repWin
	return readingAdjustID, readingExcludeID, reportCaliberV1, caliberV2
}

func publishFirstReport(t *testing.T, h http.Handler, caliberID int64) int64 {
	t.Helper()
	var windows struct {
		Windows []store.MeasurementWindow `json:"measurement_windows"`
	}
	decodeBody(t, call(t, h, "GET", "/buildings/GBD-DEMO-1/windows", "inspector", "insp-1", nil), &windows)
	var baseWin, repWin int64
	for _, win := range windows.Windows {
		if win.Phase == "baseline" {
			baseWin = win.ID
		} else {
			repWin = win.ID
		}
	}
	rec := call(t, h, "POST", "/buildings/GBD-DEMO-1/reports", "inspector", "insp-1", map[string]any{
		"caliber_id": caliberID, "baseline_window_id": baseWin, "reporting_window_id": repWin,
	})
	mustCreate(t, rec, "创建报告")
	var report store.Report
	decodeBody(t, rec, &report)
	mustCreate(t, call(t, h, "POST", fmt.Sprintf("/reports/%d/publish", report.ID), "inspector", "insp-1", nil), "发布报告")
	return report.ID
}

func TestFullWorkflowAndOwnerVisibility(t *testing.T) {
	h := NewHandler(mustStore(t))
	readingAdjustID, readingExcludeID, caliberV1, _ := setupArchive(t, h)
	reportID := publishFirstReport(t, h, caliberV1)

	// 业主查询报告:能看出口径、时段、剔除读数、更正记录
	rec := call(t, h, "GET", fmt.Sprintf("/buildings/GBD-DEMO-1/reports/%d", reportID), "owner", "owner-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("业主查询报告失败: %d %s", rec.Code, rec.Body.String())
	}
	var report struct {
		Status string `json:"status"`
		Result struct {
			Caliber struct {
				Code    string `json:"code"`
				Version int    `json:"version"`
			} `json:"caliber"`
			Baseline struct {
				PeriodStart      string  `json:"period_start"`
				PeriodEnd        string  `json:"period_end"`
				EnergyKWH        float64 `json:"energy_kwh"`
				ExcludedReadings []struct {
					ReadingID   int64   `json:"reading_id"`
					OriginalKWH float64 `json:"original_kwh"`
					Reason      string  `json:"reason"`
				} `json:"excluded_readings"`
				CorrectionsApplied []struct {
					ReadingID    int64   `json:"reading_id"`
					OriginalKWH  float64 `json:"original_kwh"`
					CorrectedKWH float64 `json:"corrected_kwh"`
				} `json:"corrections_applied"`
			} `json:"baseline"`
			SavingsKWH float64 `json:"savings_kwh"`
			SavingsPct float64 `json:"savings_pct"`
		} `json:"result"`
	}
	decodeBody(t, rec, &report)
	if report.Status != "published" {
		t.Fatalf("报告状态应为 published: %s", report.Status)
	}
	if report.Result.Caliber.Code != "GB-SAVINGS" || report.Result.Caliber.Version != 1 {
		t.Fatalf("报告未固定口径: %+v", report.Result.Caliber)
	}
	if report.Result.Baseline.PeriodStart != "2025-01-01T00:00:00+08:00" {
		t.Fatalf("报告应标明使用的时段: %+v", report.Result.Baseline)
	}
	// 基线能耗 = 480(更正后)+ 520 = 1000;节能量 = 1000 - 700 = 300
	if report.Result.Baseline.EnergyKWH != 1000 || report.Result.SavingsKWH != 300 {
		t.Fatalf("节能计算错误: baseline=%v savings=%v", report.Result.Baseline.EnergyKWH, report.Result.SavingsKWH)
	}
	if report.Result.SavingsPct != 30 {
		t.Fatalf("节能率错误: %v", report.Result.SavingsPct)
	}
	if len(report.Result.Baseline.ExcludedReadings) != 1 ||
		report.Result.Baseline.ExcludedReadings[0].ReadingID != readingExcludeID ||
		report.Result.Baseline.ExcludedReadings[0].OriginalKWH != 999 {
		t.Fatalf("业主应看到被剔除的读数及原值: %+v", report.Result.Baseline.ExcludedReadings)
	}
	if len(report.Result.Baseline.CorrectionsApplied) != 1 ||
		report.Result.Baseline.CorrectionsApplied[0].ReadingID != readingAdjustID ||
		report.Result.Baseline.CorrectionsApplied[0].OriginalKWH != 500 {
		t.Fatalf("业主应看到更正记录及原值: %+v", report.Result.Baseline.CorrectionsApplied)
	}
}

func TestCaliberComparisonShowsDelta(t *testing.T) {
	h := NewHandler(mustStore(t))
	_, _, caliberV1, _ := setupArchive(t, h)
	reportID := publishFirstReport(t, h, caliberV1)

	rec := call(t, h, "GET", fmt.Sprintf("/buildings/GBD-DEMO-1/reports/%d/caliber-comparison", reportID), "owner", "owner-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("口径对比失败: %d %s", rec.Code, rec.Body.String())
	}
	var comparison struct {
		Published struct {
			SavingsKWH float64 `json:"savings_kwh"`
		} `json:"published"`
		Rows []struct {
			Caliber struct {
				Version int `json:"version"`
			} `json:"caliber"`
			SavingsKWH *float64 `json:"savings_kwh"`
			DeltaKWH   *float64 `json:"delta_kwh_vs_report_caliber"`
		} `json:"rows"`
	}
	decodeBody(t, rec, &comparison)
	if comparison.Published.SavingsKWH != 300 {
		t.Fatalf("发布值应为 300: %v", comparison.Published.SavingsKWH)
	}
	if len(comparison.Rows) != 2 {
		t.Fatalf("应有两版口径参与对比: %+v", comparison.Rows)
	}
	var v1, v2 struct {
		savings float64
		delta   float64
	}
	for _, row := range comparison.Rows {
		if row.SavingsKWH == nil || row.DeltaKWH == nil {
			t.Fatalf("对比行缺少数值: %+v", row)
		}
		switch row.Caliber.Version {
		case 1:
			v1.savings, v1.delta = *row.SavingsKWH, *row.DeltaKWH
		case 2:
			v2.savings, v2.delta = *row.SavingsKWH, *row.DeltaKWH
		}
	}
	if v1.savings != 300 || v1.delta != 0 {
		t.Fatalf("v1 口径重算应一致且差额为 0: %v / %v", v1.savings, v1.delta)
	}
	// v2 归一化:700 × (0.8/0.9) × (600/700) = 533.333,节能量 466.667,差额 +166.667
	if v2.savings != 466.667 {
		t.Fatalf("v2 口径节能量错误: %v", v2.savings)
	}
	if v2.delta != 166.667 {
		t.Fatalf("口径变化造成的差额错误: %v", v2.delta)
	}
}

func TestVendorCannotAlterConclusions(t *testing.T) {
	h := NewHandler(mustStore(t))
	_, _, caliberV1, _ := setupArchive(t, h)

	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{"供应商不得创建窗口", "POST", "/buildings/GBD-DEMO-1/windows", map[string]any{"phase": "baseline", "period_start": "2025-01-01T00:00:00+08:00", "period_end": "2025-02-01T00:00:00+08:00"}},
		{"供应商不得审核窗口", "POST", "/windows/1/status", map[string]any{"status": "validated"}},
		{"供应商不得定义口径", "POST", "/calibers", map[string]any{"code": "X", "meter_kinds": []string{"heat"}, "min_coverage_ratio": 0.5}},
		{"供应商不得创建报告", "POST", "/buildings/GBD-DEMO-1/reports", map[string]any{"caliber_id": caliberV1, "baseline_window_id": 1, "reporting_window_id": 2}},
		{"供应商不得发布报告", "POST", "/reports/1/publish", nil},
		{"供应商不得更正读数", "POST", "/readings/1/corrections", map[string]any{"action": "exclude", "reason": "x"}},
	}
	for _, tc := range cases {
		rec := call(t, h, tc.method, tc.path, "vendor", "vendor-1", tc.body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s:期望 403,实际 %d", tc.name, rec.Code)
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		decodeBody(t, rec, &body)
		if body.Error.Code != "VENDOR_CANNOT_ALTER_CONCLUSIONS" {
			t.Fatalf("%s:错误码应为 VENDOR_CANNOT_ALTER_CONCLUSIONS,实际 %s", tc.name, body.Error.Code)
		}
	}

	// 供应商可以读取已发布结论(透明),但不能修改
	reportID := publishFirstReport(t, h, caliberV1)
	rec := call(t, h, "GET", fmt.Sprintf("/buildings/GBD-DEMO-1/reports/%d", reportID), "vendor", "vendor-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("供应商应能读取结论: %d", rec.Code)
	}
}

func TestPublishRequiresValidatedWindow(t *testing.T) {
	h := NewHandler(mustStore(t))
	_, _, caliberV1, _ := setupArchive(t, h)

	// 新建一对窗口但不审核
	mkOpen := func(phase, start, end string) int64 {
		rec := call(t, h, "POST", "/buildings/GBD-DEMO-1/windows", "inspector", "insp-1", map[string]any{
			"phase": phase, "period_start": start, "period_end": end,
		})
		mustCreate(t, rec, "创建窗口")
		var win store.MeasurementWindow
		decodeBody(t, rec, &win)
		return win.ID
	}
	baseWin := mkOpen("baseline", "2025-03-01T00:00:00+08:00", "2025-04-01T00:00:00+08:00")
	repWin := mkOpen("reporting", "2026-03-01T00:00:00+08:00", "2026-04-01T00:00:00+08:00")

	rec := call(t, h, "POST", "/buildings/GBD-DEMO-1/reports", "inspector", "insp-1", map[string]any{
		"caliber_id": caliberV1, "baseline_window_id": baseWin, "reporting_window_id": repWin,
	})
	mustCreate(t, rec, "创建报告")
	var report store.Report
	decodeBody(t, rec, &report)

	rec = call(t, h, "POST", fmt.Sprintf("/reports/%d/publish", report.ID), "inspector", "insp-1", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("窗口未审核时发布应返回 409,实际 %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPublishedReportIsImmutable(t *testing.T) {
	h := NewHandler(mustStore(t))
	_, _, caliberV1, _ := setupArchive(t, h)
	reportID := publishFirstReport(t, h, caliberV1)

	rec := call(t, h, "POST", fmt.Sprintf("/reports/%d/publish", reportID), "inspector", "insp-1", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("重复发布应被拒绝: %d", rec.Code)
	}

	// 更正结论的正确方式:新建报告并注明 supersedes_id
	var windows struct {
		Windows []store.MeasurementWindow `json:"measurement_windows"`
	}
	decodeBody(t, call(t, h, "GET", "/buildings/GBD-DEMO-1/windows", "inspector", "insp-1", nil), &windows)
	rec = call(t, h, "POST", "/buildings/GBD-DEMO-1/reports", "inspector", "insp-2", map[string]any{
		"caliber_id": caliberV1, "baseline_window_id": windows.Windows[0].ID,
		"reporting_window_id": windows.Windows[1].ID, "supersedes_id": reportID,
	})
	mustCreate(t, rec, "创建取代报告")

	// 旧报告仍可查询(历史保留)
	rec = call(t, h, "GET", fmt.Sprintf("/buildings/GBD-DEMO-1/reports/%d", reportID), "owner", "owner-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("被取代的报告应保留可查: %d", rec.Code)
	}
}

func TestReadingListPreservesOriginalAfterCorrection(t *testing.T) {
	h := NewHandler(mustStore(t))
	readingAdjustID, _, _, _ := setupArchive(t, h)

	rec := call(t, h, "GET", "/buildings/GBD-DEMO-1/readings", "owner", "owner-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询读数失败: %d", rec.Code)
	}
	var list struct {
		Readings []struct {
			ReadingID    int64   `json:"reading_id"`
			OriginalKWH  float64 `json:"original_kwh"`
			EffectiveKWH float64 `json:"effective_kwh"`
		} `json:"readings"`
	}
	decodeBody(t, rec, &list)
	for _, r := range list.Readings {
		if r.ReadingID == readingAdjustID {
			if r.OriginalKWH != 500 || r.EffectiveKWH != 480 {
				t.Fatalf("原读数必须保留且生效值另列: %+v", r)
			}
			return
		}
	}
	t.Fatal("读数列表缺少被更正的读数")
}

func TestActorRequired(t *testing.T) {
	h := NewHandler(mustStore(t))
	rec := call(t, h, "POST", "/buildings", "", "", map[string]any{"building_ref": "X", "name": "x"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("缺少操作者头应返回 401,实际 %d", rec.Code)
	}
	rec = call(t, h, "GET", "/buildings", "owner", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("查询也需要操作者身份: %d", rec.Code)
	}
	rec = call(t, h, "GET", "/buildings", "alien", "x-1", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("未知角色应返回 403,实际 %d", rec.Code)
	}
}

func TestOwnerIsReadOnly(t *testing.T) {
	h := NewHandler(mustStore(t))
	rec := call(t, h, "POST", "/buildings", "owner", "owner-1", map[string]any{"building_ref": "X", "name": "x"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("业主不得写入档案: %d", rec.Code)
	}
}
