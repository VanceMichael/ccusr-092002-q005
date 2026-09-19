package store

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// 建筑与基线
// ---------------------------------------------------------------------------

func (s *Store) CreateBuilding(b Building) (Building, error) {
	if b.BuildingRef == "" || b.Name == "" {
		return b, &ValidationError{Message: "building_ref 与 name 为必填"}
	}
	if b.FloorAreaM2 < 0 {
		return b, &ValidationError{Message: "floor_area_m2 不得为负"}
	}
	err := s.mutate(func() error {
		for _, existing := range s.data.Buildings {
			if existing.BuildingRef == b.BuildingRef {
				return &ConflictError{Message: "building_ref 已存在: " + b.BuildingRef}
			}
		}
		b.ID = s.nextIDLocked("buildings")
		b.CreatedAt = s.timestamp()
		s.data.Buildings = append(s.data.Buildings, b)
		return nil
	})
	return b, err
}

func (s *Store) ListBuildings() []Building {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedByID(s.data.Buildings)
}

func (s *Store) GetBuildingByRef(ref string) (Building, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildingByRefLocked(ref)
}

func (s *Store) buildingByRefLocked(ref string) (Building, error) {
	for _, b := range s.data.Buildings {
		if b.BuildingRef == ref {
			return b, nil
		}
	}
	return Building{}, ErrNotFound
}

func (s *Store) buildingByIDLocked(id int64) (Building, error) {
	for _, b := range s.data.Buildings {
		if b.ID == id {
			return b, nil
		}
	}
	return Building{}, ErrNotFound
}

func (s *Store) CreateBaseline(bl Baseline) (Baseline, error) {
	if _, _, err := ParsePeriod(bl.PeriodStart, bl.PeriodEnd); err != nil {
		return bl, err
	}
	if err := validateRatio("occupancy_ratio", bl.OccupancyRatio); err != nil {
		return bl, err
	}
	if err := validateNonNeg("heating_degree_days", bl.HeatingDegreeDays); err != nil {
		return bl, err
	}
	err := s.mutate(func() error {
		if _, err := s.buildingByIDLocked(bl.BuildingID); err != nil {
			return err
		}
		bl.ID = s.nextIDLocked("baselines")
		bl.CreatedAt = s.timestamp()
		s.data.Baselines = append(s.data.Baselines, bl)
		return nil
	})
	return bl, err
}

func (s *Store) ListBaselines(buildingID int64) []Baseline {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Baseline
	for _, bl := range s.data.Baselines {
		if bl.BuildingID == buildingID {
			out = append(out, bl)
		}
	}
	return sortedByID(out)
}

// ---------------------------------------------------------------------------
// 仪表与读数
// ---------------------------------------------------------------------------

func (s *Store) CreateMeter(m Meter) (Meter, error) {
	if m.MeterRef == "" {
		return m, &ValidationError{Message: "meter_ref 为必填"}
	}
	if !contains(MeterKinds, m.Kind) {
		return m, &ValidationError{Message: "kind 必须是 electricity/heat/gas/water 之一"}
	}
	if m.InstalledAt != "" {
		if _, err := time.Parse(time.RFC3339, m.InstalledAt); err != nil {
			return m, &ValidationError{Message: "installed_at 必须是带时区偏移的 ISO 8601 时间"}
		}
	}
	err := s.mutate(func() error {
		if _, err := s.buildingByIDLocked(m.BuildingID); err != nil {
			return err
		}
		for _, existing := range s.data.Meters {
			if existing.BuildingID == m.BuildingID && existing.MeterRef == m.MeterRef {
				return &ConflictError{Message: "该建筑下 meter_ref 已存在: " + m.MeterRef}
			}
		}
		m.ID = s.nextIDLocked("meters")
		m.CreatedAt = s.timestamp()
		s.data.Meters = append(s.data.Meters, m)
		return nil
	})
	return m, err
}

func (s *Store) ListMeters(buildingID int64) []Meter {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Meter
	for _, m := range s.data.Meters {
		if m.BuildingID == buildingID {
			out = append(out, m)
		}
	}
	return sortedByID(out)
}

func (s *Store) meterByIDLocked(id int64) (Meter, error) {
	for _, m := range s.data.Meters {
		if m.ID == id {
			return m, nil
		}
	}
	return Meter{}, ErrNotFound
}

// CreateReading 追加一条现场读数。读数只允许新增,数值修正走 AddCorrection。
func (s *Store) CreateReading(r Reading) (Reading, error) {
	if _, _, err := ParsePeriod(r.PeriodStart, r.PeriodEnd); err != nil {
		return r, err
	}
	if r.ReadingKWH < 0 {
		return r, &ValidationError{Message: "reading_kwh 不得为负"}
	}
	err := s.mutate(func() error {
		if _, err := s.meterByIDLocked(r.MeterID); err != nil {
			return err
		}
		r.ID = s.nextIDLocked("readings")
		r.CreatedAt = s.timestamp()
		s.data.Readings = append(s.data.Readings, r)
		return nil
	})
	return r, err
}

func (s *Store) GetReading(id int64) (Reading, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.data.Readings {
		if r.ID == id {
			return r, nil
		}
	}
	return Reading{}, ErrNotFound
}

// ListReadings 返回建筑全部读数(按 ID 升序)。
func (s *Store) ListReadings(buildingID int64) ([]Reading, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meterIDs := map[int64]bool{}
	for _, m := range s.data.Meters {
		if m.BuildingID == buildingID {
			meterIDs[m.ID] = true
		}
	}
	var out []Reading
	for _, r := range s.data.Readings {
		if meterIDs[r.MeterID] {
			out = append(out, r)
		}
	}
	return sortedByID(out), nil
}

// ---------------------------------------------------------------------------
// 读数更正(校准人员;原读数保留)
// ---------------------------------------------------------------------------

// AddCorrection 为读数追加一条更正/剔除记录。原读数行不被修改;
// 生效值由最新一条更正决定(见 ResolveReading)。
func (s *Store) AddCorrection(c Correction) (Correction, error) {
	if c.Action != CorrectionAdjust && c.Action != CorrectionExclude {
		return c, &ValidationError{Message: "action 必须是 adjust 或 exclude"}
	}
	if c.Action == CorrectionAdjust {
		if c.CorrectedKWH == nil {
			return c, &ValidationError{Message: "action 为 adjust 时 corrected_kwh 为必填"}
		}
		if *c.CorrectedKWH < 0 {
			return c, &ValidationError{Message: "corrected_kwh 不得为负"}
		}
	}
	if c.Reason == "" {
		return c, &ValidationError{Message: "更正必须填写 reason(审计要求)"}
	}
	err := s.mutate(func() error {
		found := false
		for _, r := range s.data.Readings {
			if r.ID == c.ReadingID {
				found = true
				break
			}
		}
		if !found {
			return ErrNotFound
		}
		c.ID = s.nextIDLocked("corrections")
		c.CreatedAt = s.timestamp()
		s.data.Corrections = append(s.data.Corrections, c)
		return nil
	})
	return c, err
}

func (s *Store) ListCorrections(readingID int64) []Correction {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Correction
	for _, c := range s.data.Corrections {
		if c.ReadingID == readingID {
			out = append(out, c)
		}
	}
	return sortedByID(out)
}

// ResolvedReading 是读数及其更正链解析后的视图:原值保留,生效值另列。
type ResolvedReading struct {
	Reading      Reading
	Meter        Meter
	EffectiveKWH float64
	Excluded     bool
	Corrections  []Correction // 按时间升序的完整更正链
}

// ResolveReadings 解析建筑全部读数的生效值与剔除状态。
func (s *Store) ResolveReadings(buildingID int64) ([]ResolvedReading, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meters := map[int64]Meter{}
	for _, m := range s.data.Meters {
		if m.BuildingID == buildingID {
			meters[m.ID] = m
		}
	}
	byReading := map[int64][]Correction{}
	for _, c := range s.data.Corrections {
		byReading[c.ReadingID] = append(byReading[c.ReadingID], c)
	}
	var out []ResolvedReading
	for _, r := range s.data.Readings {
		m, ok := meters[r.MeterID]
		if !ok {
			continue
		}
		chain := sortedByID(byReading[r.ID])
		rr := ResolvedReading{Reading: r, Meter: m, EffectiveKWH: r.ReadingKWH, Corrections: chain}
		if n := len(chain); n > 0 {
			last := chain[n-1]
			switch last.Action {
			case CorrectionExclude:
				rr.Excluded = true
			case CorrectionAdjust:
				if last.CorrectedKWH != nil {
					rr.EffectiveKWH = *last.CorrectedKWH
				}
			}
		}
		out = append(out, rr)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 设备更换与供应商宣传值
// ---------------------------------------------------------------------------

func (s *Store) CreateReplacement(r EquipmentReplacement) (EquipmentReplacement, error) {
	if r.EquipmentKind == "" || r.InstalledModel == "" {
		return r, &ValidationError{Message: "equipment_kind 与 installed_model 为必填"}
	}
	if _, err := time.Parse(time.RFC3339, r.ReplacedAt); err != nil {
		return r, &ValidationError{Message: "replaced_at 必须是带时区偏移的 ISO 8601 时间"}
	}
	err := s.mutate(func() error {
		if _, err := s.buildingByIDLocked(r.BuildingID); err != nil {
			return err
		}
		r.ID = s.nextIDLocked("replacements")
		r.CreatedAt = s.timestamp()
		s.data.Replacements = append(s.data.Replacements, r)
		return nil
	})
	return r, err
}

func (s *Store) ListReplacements(buildingID int64) []EquipmentReplacement {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []EquipmentReplacement
	for _, r := range s.data.Replacements {
		if r.BuildingID == buildingID {
			out = append(out, r)
		}
	}
	return sortedByID(out)
}

func (s *Store) GetReplacement(id int64) (EquipmentReplacement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.data.Replacements {
		if r.ID == id {
			return r, nil
		}
	}
	return EquipmentReplacement{}, ErrNotFound
}

// CreateClaim 保存供应商宣传值。宣传值与现场读数分开存放,不参与节能计算。
func (s *Store) CreateClaim(c VendorClaim) (VendorClaim, error) {
	if c.ClaimKind == "" || c.Unit == "" {
		return c, &ValidationError{Message: "claim_kind 与 unit 为必填"}
	}
	err := s.mutate(func() error {
		found := false
		for _, r := range s.data.Replacements {
			if r.ID == c.ReplacementID {
				found = true
				break
			}
		}
		if !found {
			return ErrNotFound
		}
		c.ID = s.nextIDLocked("claims")
		c.CreatedAt = s.timestamp()
		s.data.Claims = append(s.data.Claims, c)
		return nil
	})
	return c, err
}

func (s *Store) ListClaims(replacementID int64) []VendorClaim {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []VendorClaim
	for _, c := range s.data.Claims {
		if c.ReplacementID == replacementID {
			out = append(out, c)
		}
	}
	return sortedByID(out)
}

// ---------------------------------------------------------------------------
// 室内环境记录
// ---------------------------------------------------------------------------

func (s *Store) CreateEnvironmentRecord(r EnvironmentRecord) (EnvironmentRecord, error) {
	if _, _, err := ParsePeriod(r.PeriodStart, r.PeriodEnd); err != nil {
		return r, err
	}
	if err := validateRatio("occupancy_ratio", r.OccupancyRatio); err != nil {
		return r, err
	}
	if err := validateNonNeg("heating_degree_days", r.HeatingDegreeDays); err != nil {
		return r, err
	}
	if r.IndoorHumidityPct != nil && (*r.IndoorHumidityPct < 0 || *r.IndoorHumidityPct > 100) {
		return r, &ValidationError{Message: "indoor_humidity_pct 必须在 0 到 100 之间"}
	}
	err := s.mutate(func() error {
		if _, err := s.buildingByIDLocked(r.BuildingID); err != nil {
			return err
		}
		r.ID = s.nextIDLocked("env_records")
		r.CreatedAt = s.timestamp()
		s.data.EnvRecords = append(s.data.EnvRecords, r)
		return nil
	})
	return r, err
}

func (s *Store) ListEnvironmentRecords(buildingID int64) []EnvironmentRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []EnvironmentRecord
	for _, r := range s.data.EnvRecords {
		if r.BuildingID == buildingID {
			out = append(out, r)
		}
	}
	return sortedByID(out)
}

// ---------------------------------------------------------------------------
// 测量窗口
// ---------------------------------------------------------------------------

func (s *Store) CreateWindow(w MeasurementWindow) (MeasurementWindow, error) {
	if w.Phase != PhaseBaseline && w.Phase != PhaseReporting {
		return w, &ValidationError{Message: "phase 必须是 baseline 或 reporting"}
	}
	if _, _, err := ParsePeriod(w.PeriodStart, w.PeriodEnd); err != nil {
		return w, err
	}
	err := s.mutate(func() error {
		if _, err := s.buildingByIDLocked(w.BuildingID); err != nil {
			return err
		}
		w.ID = s.nextIDLocked("windows")
		w.Status = WindowOpen
		w.CreatedAt = s.timestamp()
		s.data.Windows = append(s.data.Windows, w)
		return nil
	})
	return w, err
}

func (s *Store) GetWindow(id int64) (MeasurementWindow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.windowByIDLocked(id)
}

func (s *Store) windowByIDLocked(id int64) (MeasurementWindow, error) {
	for _, w := range s.data.Windows {
		if w.ID == id {
			return w, nil
		}
	}
	return MeasurementWindow{}, ErrNotFound
}

func (s *Store) ListWindows(buildingID int64) []MeasurementWindow {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []MeasurementWindow
	for _, w := range s.data.Windows {
		if w.BuildingID == buildingID {
			out = append(out, w)
		}
	}
	return sortedByID(out)
}

// SetWindowStatus 由第三方审核窗口。open ↔ validated/invalid 可流转,
// 但已被已发布报告引用的窗口不得再改动,保证已发布结论的窗口依据稳定。
func (s *Store) SetWindowStatus(id int64, status, reviewer string) (MeasurementWindow, error) {
	if status != WindowValidated && status != WindowInvalid && status != WindowOpen {
		return MeasurementWindow{}, &ValidationError{Message: "status 必须是 open/validated/invalid"}
	}
	var out MeasurementWindow
	err := s.mutate(func() error {
		for _, rep := range s.data.Reports {
			if rep.Status == ReportPublished &&
				(rep.BaselineWindowID == id || rep.ReportingWindowID == id) {
				return &ConflictError{Message: fmt.Sprintf("窗口 %d 已被已发布报告 %d 引用,不得变更状态", id, rep.ID)}
			}
		}
		for i, w := range s.data.Windows {
			if w.ID == id {
				w.Status = status
				w.ReviewedBy = reviewer
				w.ReviewedAt = s.timestamp()
				s.data.Windows[i] = w
				out = w
				return nil
			}
		}
		return ErrNotFound
	})
	return out, err
}

// ---------------------------------------------------------------------------
// 口径
// ---------------------------------------------------------------------------

// CreateCaliber 新增口径版本。口径创建后不可修改;口径变化必须新增版本,
// 以便业主看到口径变化造成的差额。
func (s *Store) CreateCaliber(c Caliber) (Caliber, error) {
	if c.Code == "" {
		return c, &ValidationError{Message: "code 为必填"}
	}
	if len(c.MeterKinds) == 0 {
		return c, &ValidationError{Message: "meter_kinds 至少包含一种仪表类型"}
	}
	for _, k := range c.MeterKinds {
		if !contains(MeterKinds, k) {
			return c, &ValidationError{Message: "meter_kinds 含未知类型: " + k}
		}
	}
	if c.MinCoverageRatio <= 0 || c.MinCoverageRatio > 1 {
		return c, &ValidationError{Message: "min_coverage_ratio 必须在 (0, 1] 区间"}
	}
	err := s.mutate(func() error {
		maxVersion := 0
		for _, existing := range s.data.Calibers {
			if existing.Code == c.Code && existing.Version > maxVersion {
				maxVersion = existing.Version
			}
		}
		c.ID = s.nextIDLocked("calibers")
		c.Version = maxVersion + 1
		c.Status = CaliberActive
		c.CreatedAt = s.timestamp()
		s.data.Calibers = append(s.data.Calibers, c)
		return nil
	})
	return c, err
}

func (s *Store) GetCaliber(id int64) (Caliber, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.caliberByIDLocked(id)
}

func (s *Store) caliberByIDLocked(id int64) (Caliber, error) {
	for _, c := range s.data.Calibers {
		if c.ID == id {
			return c, nil
		}
	}
	return Caliber{}, ErrNotFound
}

func (s *Store) ListCalibers() []Caliber {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := sortedByID(s.data.Calibers)
	return out
}

// RetireCaliber 退役口径。被已发布报告引用的口径不得退役(结论依据必须可追溯)。
func (s *Store) RetireCaliber(id int64) (Caliber, error) {
	var out Caliber
	err := s.mutate(func() error {
		for _, rep := range s.data.Reports {
			if rep.CaliberID == id && rep.Status == ReportPublished {
				return &ConflictError{Message: fmt.Sprintf("口径 %d 已被已发布报告 %d 引用,不得退役", id, rep.ID)}
			}
		}
		for i, c := range s.data.Calibers {
			if c.ID == id {
				if c.Status == CaliberRetired {
					return &ConflictError{Message: "口径已处于退役状态"}
				}
				c.Status = CaliberRetired
				s.data.Calibers[i] = c
				out = c
				return nil
			}
		}
		return ErrNotFound
	})
	return out, err
}

// ---------------------------------------------------------------------------
// 报告(第三方结论)
// ---------------------------------------------------------------------------

// CreateReport 创建报告草稿。同一对窗口+口径只允许一份未废弃草稿,
// 避免重复发布口径相同的结论。
func (s *Store) CreateReport(rep Report) (Report, error) {
	err := s.mutate(func() error {
		if _, err := s.buildingByIDLocked(rep.BuildingID); err != nil {
			return err
		}
		if _, err := s.caliberByIDLocked(rep.CaliberID); err != nil {
			return &ValidationError{Message: "caliber_id 不存在"}
		}
		base, err := s.windowByIDLocked(rep.BaselineWindowID)
		if err != nil {
			return &ValidationError{Message: "baseline_window_id 不存在"}
		}
		reporting, err := s.windowByIDLocked(rep.ReportingWindowID)
		if err != nil {
			return &ValidationError{Message: "reporting_window_id 不存在"}
		}
		if base.BuildingID != rep.BuildingID || reporting.BuildingID != rep.BuildingID {
			return &ValidationError{Message: "测量窗口必须属于同一建筑"}
		}
		if base.Phase != PhaseBaseline || reporting.Phase != PhaseReporting {
			return &ValidationError{Message: "baseline_window 与 reporting_window 的 phase 不匹配"}
		}
		if rep.SupersedesID != nil {
			prev, err := s.reportByIDLocked(*rep.SupersedesID)
			if err != nil {
				return &ValidationError{Message: "supersedes_id 不存在"}
			}
			if prev.BuildingID != rep.BuildingID {
				return &ValidationError{Message: "supersedes_id 必须属于同一建筑"}
			}
			if prev.Status != ReportPublished {
				return &ConflictError{Message: "只能取代已发布的报告"}
			}
		}
		rep.ID = s.nextIDLocked("reports")
		rep.Status = ReportDraft
		rep.CreatedAt = s.timestamp()
		s.data.Reports = append(s.data.Reports, rep)
		return nil
	})
	return rep, err
}

func (s *Store) GetReport(id int64) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reportByIDLocked(id)
}

func (s *Store) reportByIDLocked(id int64) (Report, error) {
	for _, r := range s.data.Reports {
		if r.ID == id {
			return r, nil
		}
	}
	return Report{}, ErrNotFound
}

func (s *Store) ListReports(buildingID int64) []Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Report
	for _, r := range s.data.Reports {
		if r.BuildingID == buildingID {
			out = append(out, r)
		}
	}
	return sortedByID(out)
}

// PublishReport 冻结计算结果并发布。发布后报告与所引用窗口、口径均锁定。
// result 为调用方(HTTP 层)基于当前数据与口径计算出的溯源结果 JSON。
func (s *Store) PublishReport(id int64, result []byte, publisher string) (Report, error) {
	var out Report
	err := s.mutate(func() error {
		for i, rep := range s.data.Reports {
			if rep.ID != id {
				continue
			}
			if rep.Status == ReportPublished {
				return &ConflictError{Message: "报告已发布,结论不可改写;如需更正请新建报告并注明 supersedes_id"}
			}
			cal, err := s.caliberByIDLocked(rep.CaliberID)
			if err != nil {
				return err
			}
			if cal.Status != CaliberActive {
				return &ConflictError{Message: "口径已退役,指标只能在确定的(有效)口径下发布"}
			}
			for _, windowID := range []int64{rep.BaselineWindowID, rep.ReportingWindowID} {
				w, err := s.windowByIDLocked(windowID)
				if err != nil {
					return err
				}
				if w.Status != WindowValidated {
					return &ConflictError{Message: fmt.Sprintf("测量窗口 %d 未通过审核,指标只能在有效测量窗口下发布", windowID)}
				}
			}
			rep.Status = ReportPublished
			rep.Result = append([]byte(nil), result...)
			rep.PublishedBy = publisher
			rep.PublishedAt = s.timestamp()
			s.data.Reports[i] = rep
			out = rep
			return nil
		}
		return ErrNotFound
	})
	return out, err
}

// ---------------------------------------------------------------------------
// 校验辅助
// ---------------------------------------------------------------------------

func validateRatio(field string, v *float64) error {
	if v != nil && (*v < 0 || *v > 1) {
		return &ValidationError{Message: field + " 必须在 0 到 1 之间"}
	}
	return nil
}

func validateNonNeg(field string, v *float64) error {
	if v != nil && *v < 0 {
		return &ValidationError{Message: field + " 不得为负"}
	}
	return nil
}
