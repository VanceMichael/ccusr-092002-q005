package httpapi

import (
	"net/http"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

// ---------------------------------------------------------------------------
// 建筑与基线
// ---------------------------------------------------------------------------

type createBuildingRequest struct {
	BuildingRef string  `json:"building_ref"`
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	FloorAreaM2 float64 `json:"floor_area_m2"`
}

func (s *Server) createBuilding(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleFieldTeam, store.RoleInspector)
	if !ok {
		return
	}
	var req createBuildingRequest
	if !decode(w, r, &req) {
		return
	}
	b, err := s.st.CreateBuilding(store.Building{
		BuildingRef: req.BuildingRef,
		Name:        req.Name,
		Address:     req.Address,
		FloorAreaM2: req.FloorAreaM2,
		CreatedBy:   a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) listBuildings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"buildings": s.st.ListBuildings()})
}

func (s *Server) getBuilding(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, b)
}

type createBaselineRequest struct {
	PeriodStart       string    `json:"period_start"`
	PeriodEnd         string    `json:"period_end"`
	OccupancyRatio    flexFloat `json:"occupancy_ratio"`
	HeatingDegreeDays flexFloat `json:"heating_degree_days"`
	Note              string    `json:"note"`
}

func (s *Server) createBaseline(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleFieldTeam, store.RoleInspector)
	if !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	var req createBaselineRequest
	if !decode(w, r, &req) {
		return
	}
	bl, err := s.st.CreateBaseline(store.Baseline{
		BuildingID:        b.ID,
		PeriodStart:       req.PeriodStart,
		PeriodEnd:         req.PeriodEnd,
		OccupancyRatio:    req.OccupancyRatio.ptr(),
		HeatingDegreeDays: req.HeatingDegreeDays.ptr(),
		Note:              req.Note,
		CreatedBy:         a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, bl)
}

func (s *Server) listBaselines(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"baselines": s.st.ListBaselines(b.ID)})
}

// ---------------------------------------------------------------------------
// 仪表与读数
// ---------------------------------------------------------------------------

type createMeterRequest struct {
	MeterRef    string `json:"meter_ref"`
	Kind        string `json:"kind"`
	InstalledAt string `json:"installed_at"`
}

func (s *Server) createMeter(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleFieldTeam, store.RoleInspector)
	if !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	var req createMeterRequest
	if !decode(w, r, &req) {
		return
	}
	m, err := s.st.CreateMeter(store.Meter{
		BuildingID:  b.ID,
		MeterRef:    req.MeterRef,
		Kind:        req.Kind,
		InstalledAt: req.InstalledAt,
		CreatedBy:   a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) listMeters(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"meters": s.st.ListMeters(b.ID)})
}

type createReadingRequest struct {
	MeterRef    string    `json:"meter_ref"`
	PeriodStart string    `json:"period_start"`
	PeriodEnd   string    `json:"period_end"`
	ReadingKWH  flexFloat `json:"reading_kwh"`
}

func (s *Server) createReading(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleFieldTeam, store.RoleInspector)
	if !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	var req createReadingRequest
	if !decode(w, r, &req) {
		return
	}
	if !req.ReadingKWH.Set {
		fail(w, http.StatusBadRequest, "VALIDATION", "reading_kwh 为必填")
		return
	}
	var meter *store.Meter
	for _, m := range s.st.ListMeters(b.ID) {
		if m.MeterRef == req.MeterRef {
			found := m
			meter = &found
			break
		}
	}
	if meter == nil {
		fail(w, http.StatusNotFound, "NOT_FOUND", "该建筑下不存在仪表: "+req.MeterRef)
		return
	}
	rd, err := s.st.CreateReading(store.Reading{
		MeterID:     meter.ID,
		PeriodStart: req.PeriodStart,
		PeriodEnd:   req.PeriodEnd,
		ReadingKWH:  req.ReadingKWH.Value,
		RecordedBy:  a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, rd)
}

// readingView 是读数的对外视图:原读数、生效值、剔除状态与完整更正链。
type readingView struct {
	ReadingID    int64              `json:"reading_id"`
	MeterRef     string             `json:"meter_ref"`
	MeterKind    string             `json:"meter_kind"`
	PeriodStart  string             `json:"period_start"`
	PeriodEnd    string             `json:"period_end"`
	OriginalKWH  float64            `json:"original_kwh"`
	EffectiveKWH float64            `json:"effective_kwh"`
	Excluded     bool               `json:"excluded"`
	Corrections  []store.Correction `json:"corrections"`
	RecordedBy   string             `json:"recorded_by"`
	CreatedAt    string             `json:"created_at"`
}

func (s *Server) listReadings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	resolved, err := s.st.ResolveReadings(b.ID)
	if storeError(w, err) {
		return
	}
	views := make([]readingView, 0, len(resolved))
	for _, rr := range resolved {
		corrections := rr.Corrections
		if corrections == nil {
			corrections = []store.Correction{}
		}
		views = append(views, readingView{
			ReadingID:    rr.Reading.ID,
			MeterRef:     rr.Meter.MeterRef,
			MeterKind:    rr.Meter.Kind,
			PeriodStart:  rr.Reading.PeriodStart,
			PeriodEnd:    rr.Reading.PeriodEnd,
			OriginalKWH:  rr.Reading.ReadingKWH,
			EffectiveKWH: rr.EffectiveKWH,
			Excluded:     rr.Excluded,
			Corrections:  corrections,
			RecordedBy:   rr.Reading.RecordedBy,
			CreatedAt:    rr.Reading.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"readings": views})
}

// ---------------------------------------------------------------------------
// 读数更正(校准人员)
// ---------------------------------------------------------------------------

type addCorrectionRequest struct {
	Action       string    `json:"action"`
	CorrectedKWH flexFloat `json:"corrected_kwh"`
	Reason       string    `json:"reason"`
}

func (s *Server) addCorrection(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleCalibrator)
	if !ok {
		return
	}
	readingID, ok := pathInt64(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "VALIDATION", "读数 id 非法")
		return
	}
	var req addCorrectionRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := s.st.AddCorrection(store.Correction{
		ReadingID:    readingID,
		Action:       req.Action,
		CorrectedKWH: req.CorrectedKWH.ptr(),
		Reason:       req.Reason,
		CorrectedBy:  a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) listCorrections(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	readingID, ok := pathInt64(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "VALIDATION", "读数 id 非法")
		return
	}
	if _, err := s.st.GetReading(readingID); storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"corrections": s.st.ListCorrections(readingID)})
}

// ---------------------------------------------------------------------------
// 设备更换与供应商宣传值
// ---------------------------------------------------------------------------

type createReplacementRequest struct {
	EquipmentKind  string `json:"equipment_kind"`
	RemovedModel   string `json:"removed_model"`
	InstalledModel string `json:"installed_model"`
	VendorRef      string `json:"vendor_ref"`
	ReplacedAt     string `json:"replaced_at"`
	Note           string `json:"note"`
}

func (s *Server) createReplacement(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleVendor, store.RoleFieldTeam, store.RoleInspector)
	if !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	var req createReplacementRequest
	if !decode(w, r, &req) {
		return
	}
	rep, err := s.st.CreateReplacement(store.EquipmentReplacement{
		BuildingID:     b.ID,
		EquipmentKind:  req.EquipmentKind,
		RemovedModel:   req.RemovedModel,
		InstalledModel: req.InstalledModel,
		VendorRef:      req.VendorRef,
		ReplacedAt:     req.ReplacedAt,
		Note:           req.Note,
		CreatedBy:      a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, rep)
}

func (s *Server) listReplacements(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"equipment_replacements": s.st.ListReplacements(b.ID)})
}

type addClaimRequest struct {
	ClaimKind  string    `json:"claim_kind"`
	ClaimValue flexFloat `json:"claim_value"`
	Unit       string    `json:"unit"`
}

func (s *Server) addClaim(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleVendor)
	if !ok {
		return
	}
	replacementID, ok := pathInt64(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "VALIDATION", "设备更换 id 非法")
		return
	}
	var req addClaimRequest
	if !decode(w, r, &req) {
		return
	}
	if !req.ClaimValue.Set {
		fail(w, http.StatusBadRequest, "VALIDATION", "claim_value 为必填")
		return
	}
	c, err := s.st.CreateClaim(store.VendorClaim{
		ReplacementID: replacementID,
		ClaimKind:     req.ClaimKind,
		ClaimValue:    req.ClaimValue.Value,
		Unit:          req.Unit,
		SubmittedBy:   a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) listClaims(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	replacementID, ok := pathInt64(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "VALIDATION", "设备更换 id 非法")
		return
	}
	if _, err := s.st.GetReplacement(replacementID); storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vendor_claims": s.st.ListClaims(replacementID)})
}

// ---------------------------------------------------------------------------
// 室内环境记录
// ---------------------------------------------------------------------------

type createEnvRecordRequest struct {
	PeriodStart       string    `json:"period_start"`
	PeriodEnd         string    `json:"period_end"`
	IndoorTempC       flexFloat `json:"indoor_temp_c"`
	IndoorHumidityPct flexFloat `json:"indoor_humidity_pct"`
	OccupancyRatio    flexFloat `json:"occupancy_ratio"`
	HeatingDegreeDays flexFloat `json:"heating_degree_days"`
}

func (s *Server) createEnvRecord(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r, store.RoleFieldTeam, store.RoleInspector)
	if !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	var req createEnvRecordRequest
	if !decode(w, r, &req) {
		return
	}
	rec, err := s.st.CreateEnvironmentRecord(store.EnvironmentRecord{
		BuildingID:        b.ID,
		PeriodStart:       req.PeriodStart,
		PeriodEnd:         req.PeriodEnd,
		IndoorTempC:       req.IndoorTempC.ptr(),
		IndoorHumidityPct: req.IndoorHumidityPct.ptr(),
		OccupancyRatio:    req.OccupancyRatio.ptr(),
		HeatingDegreeDays: req.HeatingDegreeDays.ptr(),
		RecordedBy:        a.id,
	})
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) listEnvRecords(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, knownRoles...); !ok {
		return
	}
	b, err := s.st.GetBuildingByRef(r.PathValue("ref"))
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"environment_records": s.st.ListEnvironmentRecords(b.ID)})
}
