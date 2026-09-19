package httpapi

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/domain"
	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

// ---------------------------------------------------------------------------
// 测量窗口(第三方审核)
// ---------------------------------------------------------------------------

type createWindowRequest struct {
	Phase       string `json:"phase"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Note        string `json:"note"`
}

func (s *Server) createWindow(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleInspector)
	if !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	var req createWindowRequest
	if !decode(w, r, &req) {
		return
	}
	win, err := s.st.CreateWindow(store.MeasurementWindow{
		BuildingID:  b.ID,
		Phase:       req.Phase,
		PeriodStart: req.PeriodStart,
		PeriodEnd:   req.PeriodEnd,
		Note:        req.Note,
		CreatedBy:   a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, win)
}

type setWindowStatusRequest struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (s *Server) setWindowStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleInspector)
	if !ok {
		return
	}
	id, ok := pathInt64(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "VALIDATION", "窗口 id 非法")
		return
	}
	var req setWindowStatusRequest
	if !decode(w, r, &req) {
		return
	}
	win, err := s.st.SetWindowStatus(id, req.Status, a.id)
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, win)
}

func (s *Server) listWindows(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"measurement_windows": s.st.ListWindows(b.ID)})
}

// ---------------------------------------------------------------------------
// 口径
// ---------------------------------------------------------------------------

type createCaliberRequest struct {
	Code               string   `json:"code"`
	MeterKinds         []string `json:"meter_kinds"`
	NormalizeOccupancy bool     `json:"normalize_occupancy"`
	NormalizeHeating   bool     `json:"normalize_heating"`
	MinCoverageRatio   float64  `json:"min_coverage_ratio"`
	Description        string   `json:"description"`
}

func (s *Server) createCaliber(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleInspector)
	if !ok {
		return
	}
	var req createCaliberRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := s.st.CreateCaliber(store.Caliber{
		Code:               req.Code,
		MeterKinds:         req.MeterKinds,
		NormalizeOccupancy: req.NormalizeOccupancy,
		NormalizeHeating:   req.NormalizeHeating,
		MinCoverageRatio:   req.MinCoverageRatio,
		Description:        req.Description,
		CreatedBy:          a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) listCalibers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"calibers": s.st.ListCalibers()})
}

func (s *Server) retireCaliber(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, store.RoleInspector); !ok {
		return
	}
	id, ok := pathInt64(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "VALIDATION", "口径 id 非法")
		return
	}
	c, err := s.st.RetireCaliber(id)
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// ---------------------------------------------------------------------------
// 报告(第三方结论)
// ---------------------------------------------------------------------------

type createReportRequest struct {
	CaliberID         int64  `json:"caliber_id"`
	BaselineWindowID  int64  `json:"baseline_window_id"`
	ReportingWindowID int64  `json:"reporting_window_id"`
	SupersedesID      *int64 `json:"supersedes_id"`
}

func (s *Server) createReport(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleInspector)
	if !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	var req createReportRequest
	if !decode(w, r, &req) {
		return
	}
	rep, err := s.st.CreateReport(store.Report{
		BuildingID:        b.ID,
		CaliberID:         req.CaliberID,
		BaselineWindowID:  req.BaselineWindowID,
		ReportingWindowID: req.ReportingWindowID,
		SupersedesID:      req.SupersedesID,
		CreatedBy:         a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, rep)
}

// buildWindowInputs 汇总报告两个窗口的计算输入。
func (s *Server) buildWindowInputs(rep store.Report) (domain.WindowInput, domain.WindowInput, error) {
	baseWindow, err := s.st.GetWindow(rep.BaselineWindowID)
	if err != nil {
		return domain.WindowInput{}, domain.WindowInput{}, err
	}
	repWindow, err := s.st.GetWindow(rep.ReportingWindowID)
	if err != nil {
		return domain.WindowInput{}, domain.WindowInput{}, err
	}
	readings, err := s.st.ResolveReadings(rep.BuildingID)
	if err != nil {
		return domain.WindowInput{}, domain.WindowInput{}, err
	}
	env := s.st.ListEnvironmentRecords(rep.BuildingID)
	return domain.WindowInput{Window: baseWindow, Readings: readings, EnvRecords: env},
		domain.WindowInput{Window: repWindow, Readings: readings, EnvRecords: env},
		nil
}

func (s *Server) publishReport(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleInspector)
	if !ok {
		return
	}
	id, ok := pathInt64(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "VALIDATION", "报告 id 非法")
		return
	}
	rep, err := s.st.GetReport(id)
	if storeError(w, err) {
		return
	}
	caliber, err := s.st.GetCaliber(rep.CaliberID)
	if storeError(w, err) {
		return
	}
	base, reporting, err := s.buildWindowInputs(rep)
	if storeError(w, err) {
		return
	}
	result, err := domain.Compute(caliber, base, reporting, s.now())
	if storeError(w, err) {
		return
	}
	frozen, err := json.Marshal(result)
	if err != nil {
		fail(w, http.StatusInternalServerError, "INTERNAL", "结果序列化失败")
		return
	}
	published, err := s.st.PublishReport(id, frozen, a.id)
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, published)
}

// reportSummary 是报告列表项,superseded_by 让业主看到结论的更替链。
type reportSummary struct {
	store.Report
	SupersededBy *int64 `json:"superseded_by,omitempty"`
}

func (s *Server) listReports(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	reports := s.st.ListReports(b.ID)
	supersededBy := map[int64]int64{}
	for _, rep := range reports {
		if rep.SupersedesID != nil {
			supersededBy[*rep.SupersedesID] = rep.ID
		}
	}
	out := make([]reportSummary, 0, len(reports))
	for _, rep := range reports {
		summary := reportSummary{Report: rep}
		if id, ok := supersededBy[rep.ID]; ok {
			summary.SupersededBy = &id
		}
		out = append(out, summary)
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": out})
}

// getReport 返回报告全文。已发布报告带冻结的溯源结果:使用了哪些时段、
// 剔除了哪些读数、应用了哪些更正、归一化系数与最终节能量。
func (s *Server) getReport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	rep, ok := s.reportInBuilding(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) reportInBuilding(w http.ResponseWriter, r *http.Request) (store.Report, bool) {
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return store.Report{}, false
	}
	id, ok := pathInt64(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "VALIDATION", "报告 id 非法")
		return store.Report{}, false
	}
	rep, err := s.st.GetReport(id)
	if err != nil {
		storeError(w, err)
		return store.Report{}, false
	}
	if rep.BuildingID != b.ID {
		fail(w, http.StatusNotFound, "NOT_FOUND", "该建筑下不存在此报告")
		return store.Report{}, false
	}
	return rep, true
}

// ---------------------------------------------------------------------------
// 口径对比:同一批窗口在不同口径下的结果差额
// ---------------------------------------------------------------------------

type caliberComparisonRow struct {
	Caliber    domain.CaliberSnapshot `json:"caliber"`
	SavingsKWH *float64               `json:"savings_kwh,omitempty"`
	SavingsPct *float64               `json:"savings_pct,omitempty"`
	DeltaKWH   *float64               `json:"delta_kwh_vs_report_caliber,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

// caliberComparison 用当前档案数据在每一版口径下重算同一对窗口,
// 让业主看到口径变化造成的差额。重算基准是报告所引口径的当前重算值,
// 因此差额只反映口径差异,不混入发布后的数据更正。
func (s *Server) caliberComparison(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	rep, ok := s.reportInBuilding(w, r)
	if !ok {
		return
	}
	if rep.Status != store.ReportPublished {
		fail(w, http.StatusConflict, "CONFLICT", "报告尚未发布,暂无结论可对比")
		return
	}
	base, reporting, err := s.buildWindowInputs(rep)
	if storeError(w, err) {
		return
	}
	calibers := s.st.ListCalibers()
	sort.Slice(calibers, func(i, j int) bool {
		if calibers[i].Code != calibers[j].Code {
			return calibers[i].Code < calibers[j].Code
		}
		return calibers[i].Version < calibers[j].Version
	})

	rows := make([]caliberComparisonRow, 0, len(calibers))
	var ownSavings *float64
	for _, cal := range calibers {
		result, err := domain.Compute(cal, base, reporting, s.now())
		row := caliberComparisonRow{Caliber: result.Caliber}
		if err != nil {
			row.Error = err.Error()
		} else {
			savings, pct := result.SavingsKWH, result.SavingsPct
			row.SavingsKWH = &savings
			row.SavingsPct = &pct
			if cal.ID == rep.CaliberID {
				own := result.SavingsKWH
				ownSavings = &own
			}
		}
		rows = append(rows, row)
	}
	if ownSavings != nil {
		for i := range rows {
			if rows[i].SavingsKWH != nil {
				delta := math.Round((*rows[i].SavingsKWH-*ownSavings)*1000) / 1000
				rows[i].DeltaKWH = &delta
			}
		}
	}

	var publishedResult domain.Result
	_ = json.Unmarshal(rep.Result, &publishedResult)
	writeJSON(w, http.StatusOK, map[string]any{
		"report_id": rep.ID,
		"published": map[string]any{
			"caliber":     publishedResult.Caliber,
			"savings_kwh": publishedResult.SavingsKWH,
			"savings_pct": publishedResult.SavingsPct,
			"computed_at": publishedResult.ComputedAt,
		},
		"comparison_basis": "current_data",
		"rows":             rows,
	})
}
