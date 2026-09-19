package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/archive"
	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

const (
	tokenField       = "dev-field-token"
	tokenCalibration = "dev-calibration-token"
	tokenThirdParty  = "dev-third-party-token"
	tokenOwner       = "dev-owner-token"
	tokenSupplier    = "supplier-expo-token" // 系统中不存在的令牌
)

func testActors() map[string]archive.Actor {
	return map[string]archive.Actor{
		tokenField:       {ID: "actor-field", Role: archive.RoleField},
		tokenCalibration: {ID: "actor-calibration", Role: archive.RoleCalibration},
		tokenThirdParty:  {ID: "actor-third-party", Role: archive.RoleThirdParty},
		tokenOwner:       {ID: "actor-owner", Role: archive.RoleOwner},
	}
}

func newTestRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.log")
	ledger, err := store.Open(path)
	if err != nil {
		t.Fatalf("打开日志失败：%v", err)
	}
	svc, err := archive.NewService(ledger, testActors())
	if err != nil {
		t.Fatalf("创建服务失败：%v", err)
	}
	return Router(svc), path
}

func reopenRouter(t *testing.T, path string) http.Handler {
	t.Helper()
	ledger, err := store.Open(path)
	if err != nil {
		t.Fatalf("重新打开日志失败：%v", err)
	}
	svc, err := archive.NewService(ledger, testActors())
	if err != nil {
		t.Fatalf("回放失败：%v", err)
	}
	return Router(svc)
}

func doReq(t *testing.T, h http.Handler, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

func mustID(t *testing.T, body map[string]any) string {
	t.Helper()
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("响应缺少 id：%v", body)
	}
	return id
}

func blockersJoined(body map[string]any) string {
	items, _ := body["blockers"].([]any)
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, it.(string))
	}
	return strings.Join(parts, "；")
}

// fixtureIDs 汇总一个完整可发布场景的资源编号。
type fixtureIDs struct {
	building    string
	project     string
	meter       string
	methodology string
	baselineWin string
	postWin     string
	baselineRDG []string
	postRDG     []string
}

type buildOpts struct {
	ref      string // building_ref
	hddBase  string // 基线窗口 HDD（空表示不填）
	hddPost  string
	finalize bool // 是否立即终结窗口
}

// buildScenario 建立高碑店住宅楼改造项目：一块热表，
// 基线（2025 采暖季，11 月，两条 15 天读数各 1000）与
// 改造后（2026 采暖季，1 月，两条 15 天读数各 700），
// 占用率室内记录，口径 v1（不做修正，已锁定）。
func buildScenario(t *testing.T, h http.Handler, o buildOpts) fixtureIDs {
	t.Helper()
	ids := fixtureIDs{}

	st, b := doReq(t, h, http.MethodPost, "/v1/buildings", tokenField, map[string]any{
		"building_ref":  o.ref,
		"name":          o.ref + " 住宅楼",
		"building_type": "住宅",
		"floor_area_m2": "4200.0",
	})
	if st != http.StatusCreated {
		t.Fatalf("建立建筑失败 %d %v", st, b)
	}
	ids.building = mustID(t, b)

	_, p := doReq(t, h, http.MethodPost, "/v1/projects", tokenField, map[string]any{
		"building_id": ids.building,
		"name":        "围护结构与热泵改造",
		"kind":        "retrofit",
	})
	ids.project = mustID(t, p)

	// 设备更换：宣传值无来源 → 400；带来源 → 成功。
	if st, eq := doReq(t, h, http.MethodPost, "/v1/equipment", tokenField, map[string]any{
		"project_id":           ids.project,
		"category":             "空气源热泵",
		"new_description":      "变频低温空气源热泵",
		"supplier_name":        "示例供应商",
		"supplier_model":       "DEMO-6P",
		"rated_value":          "COP 2.8（第三方工况实测）",
		"marketing_claim":      "COP 5.2",
		"marketing_claim_unit": "COP",
		"replaced_on":          "2025-12-10T00:00:00+08:00",
	}); st != http.StatusBadRequest || !strings.Contains(eq["error"].(string), "claim_source") {
		t.Fatalf("宣传值无来源应被 400 拒绝，得到 %d %v", st, eq)
	}
	if st, eq := doReq(t, h, http.MethodPost, "/v1/equipment", tokenField, map[string]any{
		"project_id":           ids.project,
		"category":             "空气源热泵",
		"new_description":      "变频低温空气源热泵",
		"supplier_name":        "示例供应商",
		"supplier_model":       "DEMO-6P",
		"rated_value":          "COP 2.8（第三方工况实测）",
		"marketing_claim":      "COP 5.2",
		"marketing_claim_unit": "COP",
		"claim_source":         "2026 展会展板",
		"replaced_on":          "2025-12-10T00:00:00+08:00",
	}); st != http.StatusCreated {
		t.Fatalf("带来源的设备记录应成功，得到 %d %v", st, eq)
	}

	_, m := doReq(t, h, http.MethodPost, "/v1/meters", tokenField, map[string]any{
		"meter_ref":    "HEAT-01",
		"building_id":  ids.building,
		"meter_type":   "heat",
		"installed_on": "2024-10-01T00:00:00+08:00",
	})
	ids.meter = mustID(t, m)

	addReading := func(start, end, value string) string {
		st, r := doReq(t, h, http.MethodPost, "/v1/readings", tokenField, map[string]any{
			"meter_id":     ids.meter,
			"period_start": start,
			"period_end":   end,
			"value":        value,
		})
		if st != http.StatusCreated {
			t.Fatalf("写入读数失败 %d %v", st, r)
		}
		return mustID(t, r)
	}
	ids.baselineRDG = append(ids.baselineRDG,
		addReading("2025-11-01T00:00:00+08:00", "2025-11-16T00:00:00+08:00", "1000"),
		addReading("2025-11-16T00:00:00+08:00", "2025-12-01T00:00:00+08:00", "1000"),
	)
	ids.postRDG = append(ids.postRDG,
		addReading("2026-01-01T00:00:00+08:00", "2026-01-16T00:00:00+08:00", "700"),
		addReading("2026-01-16T00:00:00+08:00", "2026-02-01T00:00:00+08:00", "700"),
	)

	for _, ts := range []string{
		"2025-11-08T12:00:00+08:00", "2025-11-23T12:00:00+08:00",
		"2026-01-08T12:00:00+08:00", "2026-01-23T12:00:00+08:00",
	} {
		if st, rec := doReq(t, h, http.MethodPost, "/v1/indoor", tokenField, map[string]any{
			"building_id": ids.building, "ts": ts, "temp_c": "20.5",
			"occupancy_rate": "1.0", "heating_season": 1,
		}); st != http.StatusCreated {
			t.Fatalf("室内记录失败 %d %v", st, rec)
		}
	}

	_, meth := doReq(t, h, http.MethodPost, "/v1/methodologies", tokenField, map[string]any{
		"project_id":              ids.project,
		"included_meter_ids":      []string{ids.meter},
		"heating_normalization":   "none",
		"occupancy_normalization": "none",
		"boundary_note":           "仅楼栋热力入口总表",
	})
	ids.methodology = mustID(t, meth)
	if st, _ := doReq(t, h, "POST", "/v1/methodologies/"+ids.methodology+"/lock", tokenField, nil); st != http.StatusCreated {
		t.Fatalf("锁定口径失败 %d", st)
	}

	openWindow := func(label, kind, start, end, hdd string) string {
		body := map[string]any{
			"project_id":     ids.project,
			"methodology_id": ids.methodology,
			"label":          label,
			"kind":           kind,
			"period_start":   start,
			"period_end":     end,
			"heating_season": 1,
		}
		if hdd != "" {
			body["hdd"] = hdd
		}
		st, w := doReq(t, h, http.MethodPost, "/v1/windows", tokenField, body)
		if st != http.StatusCreated {
			t.Fatalf("建立窗口失败 %d %v", st, w)
		}
		return mustID(t, w)
	}
	ids.baselineWin = openWindow("基线", "baseline",
		"2025-11-01T00:00:00+08:00", "2025-12-01T00:00:00+08:00", o.hddBase)
	ids.postWin = openWindow("改造后", "post",
		"2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00", o.hddPost)

	if o.finalize {
		for _, wid := range []string{ids.baselineWin, ids.postWin} {
			if st, f := doReq(t, h, "POST", "/v1/windows/"+wid+"/finalize", tokenField, nil); st != http.StatusCreated {
				t.Fatalf("终结窗口 %s 失败 %d %v", wid, st, f)
			}
		}
	}
	return ids
}

func publish(t *testing.T, h http.Handler, ids fixtureIDs, methID string) map[string]any {
	t.Helper()
	st, r := doReq(t, h, http.MethodPost, "/v1/reports", tokenField, map[string]any{
		"project_id":         ids.project,
		"baseline_window_id": ids.baselineWin,
		"post_window_id":     ids.postWin,
		"methodology_id":     methID,
	})
	if st != http.StatusCreated {
		t.Fatalf("发布报告失败 %d %v", st, r)
	}
	return r
}

func reportSnapshot(t *testing.T, h http.Handler, rptID string) map[string]any {
	t.Helper()
	st, got := doReq(t, h, http.MethodGet, "/v1/reports/"+rptID, tokenField, nil)
	if st != http.StatusOK {
		t.Fatalf("查询报告失败 %d", st)
	}
	return got["report"].(map[string]any)["snapshot"].(map[string]any)
}

func windowView(snap map[string]any, kind string) map[string]any {
	return snap["windows"].(map[string]any)[kind].(map[string]any)
}

// ---------- 测试 ----------

func TestHealthRequiresNoAuth(t *testing.T) {
	h, _ := newTestRouter(t)
	st, b := doReq(t, h, http.MethodGet, "/health", "", nil)
	if st != http.StatusOK || b["status"] != "ok" {
		t.Fatalf("健康接口异常 %d %v", st, b)
	}
}

func TestEndToEndReportShowsWindowsReadingsAndSavings(t *testing.T) {
	h, _ := newTestRouter(t)
	ids := buildScenario(t, h, buildOpts{ref: "BLD-A", finalize: true})
	r := publish(t, h, ids, ids.methodology)

	if r["savings_kwh"] != "600.000" {
		t.Errorf("节能量 = %v，期望 600.000", r["savings_kwh"])
	}
	if r["savings_pct"] != "30.00" {
		t.Errorf("节能率 = %v，期望 30.00", r["savings_pct"])
	}
	if r["baseline_raw_kwh"] != "2000.000" || r["post_raw_kwh"] != "1400.000" {
		t.Errorf("原始合计异常 baseline=%v post=%v", r["baseline_raw_kwh"], r["post_raw_kwh"])
	}
	if r["methodology_change_delta_kwh"] != nil {
		t.Errorf("首版报告不应有口径差额，得到 %v", r["methodology_change_delta_kwh"])
	}

	snap := reportSnapshot(t, h, mustID(t, r))
	base, post := windowView(snap, "baseline"), windowView(snap, "post")
	if base["period_start"] != "2025-11-01T00:00:00+08:00" ||
		post["period_end"] != "2026-02-01T00:00:00+08:00" {
		t.Errorf("快照时段不正确：%v / %v", base["period_start"], post["period_end"])
	}
	if len(base["readings_used"].([]any)) != 2 || len(post["readings_used"].([]any)) != 2 {
		t.Errorf("两个窗口各应使用 2 条读数，得到 %d / %d",
			len(base["readings_used"].([]any)), len(post["readings_used"].([]any)))
	}
	if len(base["excluded_readings"].([]any)) != 0 || len(post["excluded_readings"].([]any)) != 0 {
		t.Error("本场景不应有剔除读数")
	}
	if snap["methodology"] == nil {
		t.Error("快照缺少口径版本")
	}
	if _, ok := snap["marketing_claim_policy"].(string); !ok {
		t.Error("快照应声明宣传值不入算的策略")
	}
}

func TestCalibrationCorrectionKeepsOriginalAndFeedsCalculation(t *testing.T) {
	h, _ := newTestRouter(t)
	// 草稿场景：先补录一条错误读数，再更正，最后终结发布。
	ids := buildScenario(t, h, buildOpts{ref: "BLD-B", finalize: false})

	st, bad := doReq(t, h, http.MethodPost, "/v1/readings", tokenField, map[string]any{
		"meter_id":     ids.meter,
		"period_start": "2025-11-10T00:00:00+08:00",
		"period_end":   "2025-11-11T00:00:00+08:00",
		"value":        "1000", // 录入错误（应为 100，倍率大 10 倍）
	})
	if st != http.StatusCreated {
		t.Fatalf("写入错误读数失败 %d %v", st, bad)
	}
	badID := mustID(t, bad)

	// 现场团队无权更正。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/readings/"+badID+"/corrections", tokenField, map[string]any{
		"corrected_value": "100", "reason": "倍率错误",
	}); st != http.StatusForbidden {
		t.Fatalf("现场团队更正应 403，得到 %d", st)
	}
	// 校准人员不能建立建筑档案。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/buildings", tokenCalibration, map[string]any{
		"building_ref": "X", "name": "X", "building_type": "住宅", "floor_area_m2": "1",
	}); st != http.StatusForbidden {
		t.Fatalf("校准角色越权建档应 403，得到 %d", st)
	}
	// 供应商令牌根本不存在。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/readings/"+badID+"/corrections", tokenSupplier, map[string]any{
		"corrected_value": "100", "reason": "倍率错误",
	}); st != http.StatusUnauthorized {
		t.Fatalf("供应商令牌应 401，得到 %d", st)
	}
	// 缺原因拒绝。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/readings/"+badID+"/corrections", tokenCalibration, map[string]any{
		"corrected_value": "100",
	}); st != http.StatusBadRequest {
		t.Fatalf("无原因更正应 400，得到 %d", st)
	}
	// 校准人员更正成功。
	if st, c := doReq(t, h, http.MethodPost, "/v1/readings/"+badID+"/corrections", tokenCalibration, map[string]any{
		"corrected_value": "100",
		"reason":          "倍率设置错误（×10）",
		"calibration_ref": "CAL-2025-0087",
	}); st != http.StatusCreated {
		t.Fatalf("校准更正应成功，得到 %d %v", st, c)
	}
	// 二次更正拒绝（证据链不允许覆盖）。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/readings/"+badID+"/corrections", tokenCalibration, map[string]any{
		"corrected_value": "90", "reason": "再次调整",
	}); st != http.StatusConflict {
		t.Fatalf("重复更正应 409，得到 %d", st)
	}

	// 原读数仍可查，与更正值并列。
	st, view := doReq(t, h, http.MethodGet, "/v1/readings/"+badID, tokenField, nil)
	if st != http.StatusOK {
		t.Fatalf("查询读数失败 %d", st)
	}
	rdg := view["reading"].(map[string]any)
	cor := view["correction"].(map[string]any)
	if rdg["value"] != "1000" {
		t.Errorf("原读数被改变：%v", rdg["value"])
	}
	if cor["original_value"] != "1000" || cor["corrected_value"] != "100" || cor["reason"] == "" {
		t.Errorf("更正记录异常：%v", cor)
	}

	doReq(t, h, "POST", "/v1/windows/"+ids.baselineWin+"/finalize", tokenField, nil)
	doReq(t, h, "POST", "/v1/windows/"+ids.postWin+"/finalize", tokenField, nil)

	r := publish(t, h, ids, ids.methodology)
	if r["baseline_raw_kwh"] != "2100.000" {
		t.Errorf("基线合计应为 2100（2000 + 更正后 100），得到 %v", r["baseline_raw_kwh"])
	}
	snap := reportSnapshot(t, h, mustID(t, r))
	used := windowView(snap, "baseline")["readings_used"].([]any)
	found := false
	for _, u := range used {
		um := u.(map[string]any)
		if um["reading"].(map[string]any)["id"] == badID {
			found = true
			if um["effective_value"] != "100.000" || um["correction"] == nil {
				t.Errorf("更正读数入算异常：%v", um)
			}
		}
	}
	if !found {
		t.Error("快照中找不到被更正的读数")
	}
}

func TestPublishBlockedForDraftWindow(t *testing.T) {
	h, _ := newTestRouter(t)
	ids := buildScenario(t, h, buildOpts{ref: "BLD-C", finalize: false})
	st, body := doReq(t, h, http.MethodPost, "/v1/reports", tokenField, map[string]any{
		"project_id":         ids.project,
		"baseline_window_id": ids.baselineWin,
		"post_window_id":     ids.postWin,
		"methodology_id":     ids.methodology,
	})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("窗口未终结应 422，得到 %d %v", st, body)
	}
	joined := blockersJoined(body)
	if !strings.Contains(joined, "finalized") {
		t.Fatalf("阻断项应说明窗口未终结，得到 %v", body["blockers"])
	}
}

func TestCrossHeatingSeasonWithoutNormalizationBlocked(t *testing.T) {
	h, _ := newTestRouter(t)

	_, b := doReq(t, h, http.MethodPost, "/v1/buildings", tokenField, map[string]any{
		"building_ref": "BLD-D", "name": "D 楼",
		"building_type": "住宅", "floor_area_m2": "3000.0",
	})
	bid := mustID(t, b)
	_, p := doReq(t, h, http.MethodPost, "/v1/projects", tokenField, map[string]any{
		"building_id": bid, "name": "D 楼改造", "kind": "retrofit",
	})
	pid := mustID(t, p)
	_, m := doReq(t, h, http.MethodPost, "/v1/meters", tokenField, map[string]any{
		"meter_ref": "HEAT-D", "building_id": bid,
		"meter_type": "heat", "installed_on": "2024-01-01T00:00:00+08:00",
	})
	mid := mustID(t, m)
	_, meth := doReq(t, h, http.MethodPost, "/v1/methodologies", tokenField, map[string]any{
		"project_id": pid, "included_meter_ids": []string{mid},
	})
	metid := mustID(t, meth)
	doReq(t, h, "POST", "/v1/methodologies/"+metid+"/lock", tokenField, nil)

	mkReading := func(start, end string) {
		if st, r := doReq(t, h, http.MethodPost, "/v1/readings", tokenField, map[string]any{
			"meter_id": mid, "period_start": start, "period_end": end, "value": "500",
		}); st != http.StatusCreated {
			t.Fatalf("读数失败 %d %v", st, r)
		}
	}
	mkReading("2025-05-01T00:00:00+08:00", "2025-06-01T00:00:00+08:00")
	mkReading("2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00")

	mkWindow := func(label, kind, s, e string, hs int) string {
		st, w := doReq(t, h, http.MethodPost, "/v1/windows", tokenField, map[string]any{
			"project_id": pid, "methodology_id": metid, "label": label,
			"kind": kind, "period_start": s, "period_end": e, "heating_season": hs,
		})
		if st != http.StatusCreated {
			t.Fatalf("建窗失败 %d %v", st, w)
		}
		id := mustID(t, w)
		doReq(t, h, "POST", "/v1/windows/"+id+"/finalize", tokenField, nil)
		return id
	}
	wb := mkWindow("非采暖季基线", "baseline", "2025-05-01T00:00:00+08:00", "2025-06-01T00:00:00+08:00", 0)
	wp := mkWindow("采暖季改造后", "post", "2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00", 1)

	st, gate := doReq(t, h, http.MethodPost, "/v1/reports", tokenField, map[string]any{
		"project_id": pid, "baseline_window_id": wb,
		"post_window_id": wp, "methodology_id": metid,
	})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("跨采暖季无修正应 422，得到 %d %v", st, gate)
	}
	if !strings.Contains(blockersJoined(gate), "采暖") {
		t.Errorf("阻断项应说明采暖季问题，得到 %v", gate["blockers"])
	}
}

func TestExclusionAppearsInReportAndFinalizeEnforcesCoverage(t *testing.T) {
	h, _ := newTestRouter(t)
	ids := buildScenario(t, h, buildOpts{ref: "BLD-E", finalize: false})

	// 无原因剔除 → 400。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/windows/"+ids.postWin+"/exclusions", tokenField, map[string]any{
		"reading_id": ids.postRDG[0],
	}); st != http.StatusBadRequest {
		t.Fatalf("无原因剔除应 400，得到 %d", st)
	}
	// 正常剔除一条改造后读数。
	if st, x := doReq(t, h, http.MethodPost, "/v1/windows/"+ids.postWin+"/exclusions", tokenField, map[string]any{
		"reading_id": ids.postRDG[0], "reason": "住户装修停用 15 天，占用率为 0",
	}); st != http.StatusCreated {
		t.Fatalf("剔除读数失败 %d %v", st, x)
	}
	// 重复剔除 → 409。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/windows/"+ids.postWin+"/exclusions", tokenField, map[string]any{
		"reading_id": ids.postRDG[0], "reason": "再剔一次",
	}); st != http.StatusConflict {
		t.Fatalf("重复剔除应 409，得到 %d", st)
	}

	if st, _ := doReq(t, h, "POST", "/v1/windows/"+ids.baselineWin+"/finalize", tokenField, nil); st != http.StatusCreated {
		t.Fatalf("基线窗口终结失败 %d", st)
	}
	// 已终结窗口不能再剔除。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/windows/"+ids.baselineWin+"/exclusions", tokenField, map[string]any{
		"reading_id": ids.baselineRDG[0], "reason": "终结后剔除",
	}); st != http.StatusConflict {
		t.Fatalf("终结窗口剔除应 409，得到 %d", st)
	}
	if st, f := doReq(t, h, "POST", "/v1/windows/"+ids.postWin+"/finalize", tokenField, nil); st != http.StatusCreated {
		t.Fatalf("还剩一条有效读数时终结应成功，得到 %d %v", st, f)
	}

	r := publish(t, h, ids, ids.methodology)
	if r["post_raw_kwh"] != "700.000" {
		t.Errorf("改造后合计应仅含未剔除的 700，得到 %v", r["post_raw_kwh"])
	}
	excluded := windowView(reportSnapshot(t, h, mustID(t, r)), "post")["excluded_readings"].([]any)
	if len(excluded) != 1 {
		t.Fatalf("应展示 1 条剔除，得到 %d", len(excluded))
	}
	ex := excluded[0].(map[string]any)
	if ex["reason"] != "住户装修停用 15 天，占用率为 0" || ex["original_value"] != "700" {
		t.Errorf("剔除明细异常：%v", ex)
	}

	// 覆盖度闸门：另一草稿窗口剔光全部读数 → 终结 422。
	h2, _ := newTestRouter(t)
	ids2 := buildScenario(t, h2, buildOpts{ref: "BLD-E2", finalize: false})
	for _, rid := range ids2.baselineRDG {
		if st, _ := doReq(t, h2, http.MethodPost, "/v1/windows/"+ids2.baselineWin+"/exclusions", tokenField, map[string]any{
			"reading_id": rid, "reason": "仪表检修",
		}); st != http.StatusCreated {
			t.Fatalf("剔除失败 %d", st)
		}
	}
	st, gate := doReq(t, h2, "POST", "/v1/windows/"+ids2.baselineWin+"/finalize", tokenField, nil)
	if st != http.StatusUnprocessableEntity || !strings.Contains(blockersJoined(gate), "没有任何有效读数") {
		t.Fatalf("剔光后终结应 422 提示无有效读数，得到 %d %v", st, gate)
	}
}

func TestRecordedAfterFinalizeDoesNotEnterWindow(t *testing.T) {
	h, _ := newTestRouter(t)
	ids := buildScenario(t, h, buildOpts{ref: "BLD-F", finalize: true})

	// 终结后补录一条与基线时段重叠的读数，不得进入已冻结窗口。
	if st, r := doReq(t, h, http.MethodPost, "/v1/readings", tokenField, map[string]any{
		"meter_id":     ids.meter,
		"period_start": "2025-11-20T00:00:00+08:00",
		"period_end":   "2025-11-21T00:00:00+08:00",
		"value":        "5000",
	}); st != http.StatusCreated {
		t.Fatalf("补录读数失败 %d %v", st, r)
	}

	r := publish(t, h, ids, ids.methodology)
	if r["baseline_raw_kwh"] != "2000.000" {
		t.Errorf("冻结后补录不得改变基线合计，得到 %v", r["baseline_raw_kwh"])
	}
	late := windowView(reportSnapshot(t, h, mustID(t, r)), "baseline")["recorded_after_finalize_readings"].([]any)
	if len(late) != 1 {
		t.Fatalf("快照应单列 1 条冻结后补录读数，得到 %d", len(late))
	}
	if late[0].(map[string]any)["value"] != "5000" {
		t.Errorf("补录读数明细异常：%v", late[0])
	}
}

func TestDegreeDayMethodologyChangeDelta(t *testing.T) {
	h, _ := newTestRouter(t)
	// 窗口带 HDD，但绑定 v1（none）口径；两版口径都可用于同一对窗口。
	ids := buildScenario(t, h, buildOpts{ref: "BLD-G", hddBase: "1100", hddPost: "900", finalize: true})

	r1 := publish(t, h, ids, ids.methodology)
	if r1["savings_kwh"] != "600.000" {
		t.Fatalf("v1 不修正节能应为 600，得到 %v", r1["savings_kwh"])
	}

	_, m2 := doReq(t, h, http.MethodPost, "/v1/methodologies", tokenField, map[string]any{
		"project_id":              ids.project,
		"included_meter_ids":      []string{ids.meter},
		"heating_normalization":   "degree_day",
		"occupancy_normalization": "none",
		"change_note":             "改造后冬季偏暖，按采暖度日归一",
	})
	v2 := mustID(t, m2)
	if st, _ := doReq(t, h, "POST", "/v1/methodologies/"+v2+"/lock", tokenField, nil); st != http.StatusCreated {
		t.Fatalf("锁定 v2 失败 %d", st)
	}

	r2 := publish(t, h, ids, v2)
	// 改造后归一：1400 × 1100/900 = 1711.111；节能 288.889。
	if r2["savings_kwh"] != "288.889" {
		t.Fatalf("度日修正后节能应为 288.889，得到 %v", r2["savings_kwh"])
	}
	if r2["methodology_change_delta_kwh"] != "-311.111" {
		t.Fatalf("口径变化差额应为 -311.111，得到 %v", r2["methodology_change_delta_kwh"])
	}
	if r2["prior_report_id"] != mustID(t, r1) {
		t.Fatalf("新版报告应指向上一版 %v，得到 %v", mustID(t, r1), r2["prior_report_id"])
	}
	change := reportSnapshot(t, h, mustID(t, r2))["methodology_change"].(map[string]any)
	if change["delta_kwh"] != "-311.111" || change["basis"] != "same_windows_methodology_changed" {
		t.Fatalf("快照应说明口径差额及归因，得到 %v", change)
	}
}

func TestDegreeDayWithoutHddBlocked(t *testing.T) {
	h, _ := newTestRouter(t)
	ids := buildScenario(t, h, buildOpts{ref: "BLD-H", finalize: true})

	_, m2 := doReq(t, h, http.MethodPost, "/v1/methodologies", tokenField, map[string]any{
		"project_id":            ids.project,
		"included_meter_ids":    []string{ids.meter},
		"heating_normalization": "degree_day",
	})
	v2 := mustID(t, m2)
	doReq(t, h, "POST", "/v1/methodologies/"+v2+"/lock", tokenField, nil)

	st, gate := doReq(t, h, http.MethodPost, "/v1/reports", tokenField, map[string]any{
		"project_id":         ids.project,
		"baseline_window_id": ids.baselineWin,
		"post_window_id":     ids.postWin,
		"methodology_id":     v2,
	})
	if st != http.StatusUnprocessableEntity || !strings.Contains(blockersJoined(gate), "hdd") {
		t.Fatalf("缺 HDD 用度日口径发布应 422 且提示 hdd，得到 %d %v", st, gate)
	}
}

func TestThirdPartyConclusionsAreAppendOnly(t *testing.T) {
	h, _ := newTestRouter(t)
	ids := buildScenario(t, h, buildOpts{ref: "BLD-I", finalize: true})
	rptID := mustID(t, publish(t, h, ids, ids.methodology))

	st, c1 := doReq(t, h, http.MethodPost, "/v1/reports/"+rptID+"/conclusions", tokenThirdParty, map[string]any{
		"organization": "高碑店节能量核证中心",
		"verdict":      "pass",
		"finding":      "窗口、口径与读数链可追溯，节能率 30% 成立",
	})
	if st != http.StatusCreated {
		t.Fatalf("第三方附结论失败 %d %v", st, c1)
	}
	// 现场团队不能代替第三方下结论。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/reports/"+rptID+"/conclusions", tokenField, map[string]any{
		"organization": "伪造机构", "verdict": "pass", "finding": "x",
	}); st != http.StatusForbidden {
		t.Fatalf("现场团队附结论应 403，得到 %d", st)
	}
	// 供应商令牌无效。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/reports/"+rptID+"/conclusions", tokenSupplier, map[string]any{
		"organization": "供应商", "verdict": "pass", "finding": "x",
	}); st != http.StatusUnauthorized {
		t.Fatalf("供应商附结论应 401，得到 %d", st)
	}
	// 非法裁决值拒绝。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/reports/"+rptID+"/conclusions", tokenThirdParty, map[string]any{
		"organization": "核证中心", "verdict": "excellent", "finding": "x",
	}); st != http.StatusBadRequest {
		t.Fatalf("非法 verdict 应 400，得到 %d", st)
	}
	// 第三方可追加第二条（补充复核）。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/reports/"+rptID+"/conclusions", tokenThirdParty, map[string]any{
		"organization": "高碑店节能量核证中心", "verdict": "conditional",
		"finding": "建议下采暖季复测度日修正参数",
	}); st != http.StatusCreated {
		t.Fatalf("第三方追加第二条结论应成功，得到 %d", st)
	}
	// 不存在修改/删除路径：任何此类请求都必须失败。
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(method, "/v1/reports/"+rptID+"/conclusions/"+mustID(t, c1), nil)
		req.Header.Set("Authorization", "Bearer "+tokenThirdParty)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code < 400 {
			t.Fatalf("%s 修改/删除结论不应成功，状态码 %d", method, rec.Code)
		}
	}

	_, view := doReq(t, h, http.MethodGet, "/v1/reports/"+rptID, tokenField, nil)
	cons := view["third_party_conclusions"].([]any)
	if len(cons) != 2 {
		t.Fatalf("两条结论均应保留，得到 %d", len(cons))
	}
}

func TestRetractKeepsReportAndBlocksNewConclusion(t *testing.T) {
	h, _ := newTestRouter(t)
	ids := buildScenario(t, h, buildOpts{ref: "BLD-J", finalize: true})
	rptID := mustID(t, publish(t, h, ids, ids.methodology))

	if st, _ := doReq(t, h, "POST", "/v1/reports/"+rptID+"/retract", tokenField, map[string]any{
		"reason": "基线窗口仪表配置复核有误",
	}); st != http.StatusCreated {
		t.Fatalf("撤回应成功 %d", st)
	}
	// 撤回后第三方不能再附结论。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/reports/"+rptID+"/conclusions", tokenThirdParty, map[string]any{
		"organization": "核证中心", "verdict": "fail", "finding": "x",
	}); st != http.StatusConflict {
		t.Fatalf("撤回报告附结论应 409，得到 %d", st)
	}
	// 报告与快照仍可查。
	st, view := doReq(t, h, http.MethodGet, "/v1/reports/"+rptID, tokenField, nil)
	if st != http.StatusOK || view["report"].(map[string]any)["status"] != "retracted" {
		t.Fatalf("撤回后报告应保留且状态为 retracted：%d %v", st, view)
	}
}

func TestAuditVerifyAndTamperDetection(t *testing.T) {
	h, path := newTestRouter(t)
	ids := buildScenario(t, h, buildOpts{ref: "BLD-K", finalize: true})
	publish(t, h, ids, ids.methodology)

	st, v := doReq(t, h, http.MethodGet, "/v1/audit/verify", tokenField, nil)
	if st != http.StatusOK || v["intact"] != true {
		t.Fatalf("链校验应通过：%d %v", st, v)
	}

	// 直接篡改历史读数载荷。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), `"value":"1000"`, `"value":"1200"`, 1)
	if tampered == string(raw) {
		t.Fatal("测试准备失败：未找到待篡改内容")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	// 被篡改的日志在打开（重放校验）时即应被发现。
	if _, err := store.Open(path); err == nil {
		t.Fatal("篡改历史读数后，日志重放必须检测到哈希链断裂")
	} else if !errors.Is(err, store.ErrChainBroken) {
		t.Fatalf("期望 ErrChainBroken，得到 %v", err)
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	_, path := newTestRouter(t)
	h1 := reopenRouter(t, path)
	ids := buildScenario(t, h1, buildOpts{ref: "BLD-L", finalize: true})
	rptID := mustID(t, publish(t, h1, ids, ids.methodology))
	doReq(t, h1, http.MethodPost, "/v1/reports/"+rptID+"/conclusions", tokenThirdParty, map[string]any{
		"organization": "核证中心", "verdict": "pass", "finding": "通过",
	})

	h2 := reopenRouter(t, path)
	st, view := doReq(t, h2, http.MethodGet, "/v1/reports/"+rptID, tokenField, nil)
	if st != http.StatusOK {
		t.Fatalf("重启后报告丢失：%d", st)
	}
	rpt := view["report"].(map[string]any)
	if rpt["savings_kwh"] != "600.000" {
		t.Fatalf("重启后节能结果不一致：%v", rpt["savings_kwh"])
	}
	if len(view["third_party_conclusions"].([]any)) != 1 {
		t.Fatalf("重启后第三方结论丢失：%v", view["third_party_conclusions"])
	}
}

func TestUnauthenticatedRejected(t *testing.T) {
	h, _ := newTestRouter(t)
	if st, _ := doReq(t, h, http.MethodGet, "/v1/reports", "", nil); st != http.StatusUnauthorized {
		t.Fatalf("无令牌访问应 401，得到 %d", st)
	}
	if st, _ := doReq(t, h, http.MethodGet, "/v1/reports", tokenSupplier, nil); st != http.StatusUnauthorized {
		t.Fatalf("供应商令牌应 401，得到 %d", st)
	}
}

func TestOwnerCanReadReportButCannotWrite(t *testing.T) {
	h, _ := newTestRouter(t)
	ids := buildScenario(t, h, buildOpts{ref: "BLD-M", hddBase: "1100", hddPost: "900", finalize: true})
	r1 := publish(t, h, ids, ids.methodology)

	// 口径 v2 度日修正，制造口径差额供业主核对。
	_, m2 := doReq(t, h, http.MethodPost, "/v1/methodologies", tokenField, map[string]any{
		"project_id": ids.project, "included_meter_ids": []string{ids.meter},
		"heating_normalization": "degree_day",
	})
	v2 := mustID(t, m2)
	doReq(t, h, "POST", "/v1/methodologies/"+v2+"/lock", tokenField, nil)
	r2 := publish(t, h, ids, v2)
	doReq(t, h, http.MethodPost, "/v1/reports/"+mustID(t, r2)+"/conclusions", tokenThirdParty, map[string]any{
		"organization": "核证中心", "verdict": "pass", "finding": "通过",
	})

	// 业主只读：能查报告、时段、读数、剔除、口径差额、第三方结论。
	st, view := doReq(t, h, http.MethodGet, "/v1/reports/"+mustID(t, r2), tokenOwner, nil)
	if st != http.StatusOK {
		t.Fatalf("业主查询报告应 200，得到 %d", st)
	}
	rpt := view["report"].(map[string]any)
	snap := rpt["snapshot"].(map[string]any)
	if snap["windows"].(map[string]any)["baseline"] == nil {
		t.Fatal("业主报告缺少窗口时段")
	}
	if rpt["methodology_change_delta_kwh"] != "-311.111" {
		t.Fatalf("业主要能看到口径差额，得到 %v", rpt["methodology_change_delta_kwh"])
	}
	if len(view["third_party_conclusions"].([]any)) != 1 {
		t.Fatal("业主要能看到第三方结论")
	}
	if st, _ := doReq(t, h, http.MethodGet, "/v1/audit/verify", tokenOwner, nil); st != http.StatusOK {
		t.Fatalf("业主可做链校验查询，得到 %d", st)
	}

	// 业主不能写任何东西：建档、更正、附结论、发布全部 403。
	if st, _ := doReq(t, h, http.MethodPost, "/v1/reports", tokenOwner, map[string]any{}); st != http.StatusForbidden {
		t.Fatalf("业主发布报告应 403，得到 %d", st)
	}
	if st, _ := doReq(t, h, http.MethodPost, "/v1/readings/RDG-1/corrections", tokenOwner, map[string]any{
		"corrected_value": "1", "reason": "x",
	}); st != http.StatusForbidden {
		t.Fatalf("业主更正读数应 403，得到 %d", st)
	}
	if st, _ := doReq(t, h, http.MethodPost, "/v1/reports/"+mustID(t, r1)+"/conclusions", tokenOwner, map[string]any{
		"organization": "业主自封机构", "verdict": "pass", "finding": "x",
	}); st != http.StatusForbidden {
		t.Fatalf("业主附第三方结论应 403，得到 %d", st)
	}
}
