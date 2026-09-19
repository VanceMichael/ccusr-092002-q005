package archive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

// 事件类型（与 migrations/001_bootstrap.sql 中的表一一对应）。
const (
	evBuildingRegistered = "building_registered"
	evProjectCreated     = "project_created"
	evEquipmentReplaced  = "equipment_replaced"
	evMeterRegistered    = "meter_registered"
	evReadingRecorded    = "reading_recorded"
	evReadingCorrected   = "reading_corrected"
	evIndoorRecorded     = "indoor_recorded"
	evWindowOpened       = "window_opened"
	evWindowFinalized    = "window_finalized"
	evReadingExcluded    = "reading_excluded"
	evMethodologyCreated = "methodology_created"
	evMethodologyLocked  = "methodology_locked"
	evReportPublished    = "report_published"
	evReportRetracted    = "report_retracted"
	evConclusionAttached = "conclusion_attached"
)

// Actor 是经令牌鉴权后的提交方身份。
type Actor struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// RuleError 是可映射为 HTTP 状态码的领域规则违反。
type RuleError struct {
	Status int
	Reason string
}

func (e *RuleError) Error() string { return e.Reason }

func badRequest(format string, a ...any) error {
	return &RuleError{Status: 400, Reason: fmt.Sprintf(format, a...)}
}
func forbidden(format string, a ...any) error {
	return &RuleError{Status: 403, Reason: fmt.Sprintf(format, a...)}
}
func notFound(format string, a ...any) error {
	return &RuleError{Status: 404, Reason: fmt.Sprintf(format, a...)}
}
func conflict(format string, a ...any) error {
	return &RuleError{Status: 409, Reason: fmt.Sprintf(format, a...)}
}

// GateError 表示发布条件不满足，422 并携带全部阻断项。
type GateError struct {
	Blockers []string
}

func (e *GateError) Error() string {
	return "发布被阻断：" + strings.Join(e.Blockers, "；")
}

// Service 在哈希链之上回放并提供全部领域操作。
type Service struct {
	ledger *store.Ledger

	mu sync.RWMutex

	buildings     map[string]*Building
	buildingByRef map[string]string
	projects      map[string]*Project
	equipment     map[string]*EquipmentReplacement
	meters        map[string]*Meter
	meterByRef    map[string]string
	readings      map[string]*Reading
	corrections   map[string]*Correction // 以 reading_id 为键
	indoor        []*Indoor
	windows       map[string]*Window
	exclusions    map[string][]*Exclusion // 以 window_id 为键
	methodologies map[string]*Methodology
	reports       map[string]*Report
	reportOrder   []string
	conclusions   map[string][]*Conclusion // 以 report_id 为键
	exclusionSeq  int
	conclusionSeq int

	actors map[string]Actor // token -> Actor
}

// NewService 回放日志构建服务状态。
func NewService(ledger *store.Ledger, actors map[string]Actor) (*Service, error) {
	s := &Service{
		ledger:        ledger,
		buildings:     map[string]*Building{},
		buildingByRef: map[string]string{},
		projects:      map[string]*Project{},
		equipment:     map[string]*EquipmentReplacement{},
		meters:        map[string]*Meter{},
		meterByRef:    map[string]string{},
		readings:      map[string]*Reading{},
		corrections:   map[string]*Correction{},
		windows:       map[string]*Window{},
		exclusions:    map[string][]*Exclusion{},
		methodologies: map[string]*Methodology{},
		reports:       map[string]*Report{},
		conclusions:   map[string][]*Conclusion{},
		actors:        actors,
	}
	for _, e := range ledger.Events() {
		if err := s.apply(e); err != nil {
			return nil, fmt.Errorf("回放事件 %d（%s）失败: %w", e.Seq, e.Type, err)
		}
	}
	return s, nil
}

// Authenticate 以 Bearer 令牌换取提交方身份。
func (s *Service) Authenticate(token string) (Actor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.actors[token]
	return a, ok
}

func (s *Service) require(actor Actor, role string) error {
	if actor.Role != role {
		return forbidden("该操作仅允许 %s 角色执行（当前身份 %s）", role, actor.Role)
	}
	return nil
}

func (s *Service) nextID(prefix string, n int) string {
	return fmt.Sprintf("%s-%d", prefix, n+1)
}

// appendEvent 固化事件并同步内存状态。
func (s *Service) appendEvent(actor Actor, typ string, payload any) (store.Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return store.Event{}, err
	}
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // 数字保持原文进入哈希链
	if err := dec.Decode(&m); err != nil {
		return store.Event{}, err
	}
	ev, err := s.ledger.Append(actor.ID, typ, m)
	if err != nil {
		return store.Event{}, err
	}
	if err := s.apply(ev); err != nil {
		return store.Event{}, fmt.Errorf("事件回放不一致: %w", err)
	}
	return ev, nil
}

func (s *Service) apply(e store.Event) error {
	decode := func(v any) error {
		raw, err := json.Marshal(e.Payload)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, v)
	}
	switch e.Type {
	case evBuildingRegistered:
		var v Building
		if err := decode(&v); err != nil {
			return err
		}
		s.buildings[v.ID] = &v
		s.buildingByRef[v.BuildingRef] = v.ID
	case evProjectCreated:
		var v Project
		if err := decode(&v); err != nil {
			return err
		}
		s.projects[v.ID] = &v
	case evEquipmentReplaced:
		var v EquipmentReplacement
		if err := decode(&v); err != nil {
			return err
		}
		s.equipment[v.ID] = &v
	case evMeterRegistered:
		var v Meter
		if err := decode(&v); err != nil {
			return err
		}
		s.meters[v.ID] = &v
		s.meterByRef[v.MeterRef] = v.ID
	case evReadingRecorded:
		var v Reading
		if err := decode(&v); err != nil {
			return err
		}
		s.readings[v.ID] = &v
	case evReadingCorrected:
		var v Correction
		if err := decode(&v); err != nil {
			return err
		}
		s.corrections[v.ReadingID] = &v
	case evIndoorRecorded:
		var v Indoor
		if err := decode(&v); err != nil {
			return err
		}
		s.indoor = append(s.indoor, &v)
	case evWindowOpened:
		var v Window
		if err := decode(&v); err != nil {
			return err
		}
		s.windows[v.ID] = &v
	case evWindowFinalized:
		var p struct {
			Window Window `json:"window"`
		}
		if err := decode(&p); err != nil {
			return err
		}
		s.windows[p.Window.ID] = &p.Window
	case evReadingExcluded:
		var v Exclusion
		if err := decode(&v); err != nil {
			return err
		}
		s.exclusions[v.WindowID] = append(s.exclusions[v.WindowID], &v)
		s.exclusionSeq++
	case evMethodologyCreated:
		var v Methodology
		if err := decode(&v); err != nil {
			return err
		}
		s.methodologies[v.ID] = &v
	case evMethodologyLocked:
		var p struct {
			Methodology   Methodology `json:"methodology"`
			SupersededIDs []string    `json:"superseded_ids"`
		}
		if err := decode(&p); err != nil {
			return err
		}
		s.methodologies[p.Methodology.ID] = &p.Methodology
		for _, id := range p.SupersededIDs {
			if m, ok := s.methodologies[id]; ok && m.ID != p.Methodology.ID {
				m.Status = "superseded"
			}
		}
	case evReportPublished:
		var v Report
		if err := decode(&v); err != nil {
			return err
		}
		s.reports[v.ID] = &v
		s.reportOrder = append(s.reportOrder, v.ID)
	case evReportRetracted:
		var p struct {
			ID string `json:"id"`
		}
		if err := decode(&p); err != nil {
			return err
		}
		if r, ok := s.reports[p.ID]; ok {
			r.Status = "retracted"
		}
	case evConclusionAttached:
		var v Conclusion
		if err := decode(&v); err != nil {
			return err
		}
		s.conclusions[v.ReportID] = append(s.conclusions[v.ReportID], &v)
		s.conclusionSeq++
	default:
		return fmt.Errorf("未知事件类型 %q", e.Type)
	}
	return nil
}

// ---------- 输入结构 ----------

type BuildingInput struct {
	BuildingRef  string `json:"building_ref"`
	Name         string `json:"name"`
	City         string `json:"city"`
	BuildingType string `json:"building_type"`
	FloorAreaM2  string `json:"floor_area_m2"`
}

type ProjectInput struct {
	BuildingID string `json:"building_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Scope      string `json:"scope"`
	WorkStart  string `json:"work_start"`
	WorkEnd    string `json:"work_end"`
}

type EquipmentInput struct {
	ProjectID          string `json:"project_id"`
	Category           string `json:"category"`
	OldDescription     string `json:"old_description"`
	NewDescription     string `json:"new_description"`
	SupplierName       string `json:"supplier_name"`
	SupplierModel      string `json:"supplier_model"`
	RatedValue         string `json:"rated_value"`
	RatedUnit          string `json:"rated_unit"`
	MarketingClaim     string `json:"marketing_claim"`
	MarketingClaimUnit string `json:"marketing_claim_unit"`
	ClaimSource        string `json:"claim_source"`
	ReplacedOn         string `json:"replaced_on"`
}

type MeterInput struct {
	MeterRef    string `json:"meter_ref"`
	BuildingID  string `json:"building_id"`
	MeterType   string `json:"meter_type"`
	Unit        string `json:"unit"`
	Location    string `json:"location"`
	InstalledOn string `json:"installed_on"`
}

type ReadingInput struct {
	MeterID     string `json:"meter_id"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Value       string `json:"value"`
	Unit        string `json:"unit"`
	Quality     string `json:"quality"`
	SourceRef   string `json:"source_ref"`
}

type CorrectionInput struct {
	CorrectedValue string `json:"corrected_value"`
	Reason         string `json:"reason"`
	CalibrationRef string `json:"calibration_ref"`
}

type IndoorInput struct {
	BuildingID    string `json:"building_id"`
	TS            string `json:"ts"`
	TempC         string `json:"temp_c"`
	HumidityPct   string `json:"humidity_pct"`
	CO2Ppm        string `json:"co2_ppm"`
	OccupancyRate string `json:"occupancy_rate"`
	HeatingSeason *int   `json:"heating_season"`
	Note          string `json:"note"`
}

type WindowInput struct {
	ProjectID     string `json:"project_id"`
	MethodologyID string `json:"methodology_id"`
	Label         string `json:"label"`
	Kind          string `json:"kind"`
	PeriodStart   string `json:"period_start"`
	PeriodEnd     string `json:"period_end"`
	HeatingSeason *int   `json:"heating_season"`
	HDD           string `json:"hdd"`
	OccupancyMin  string `json:"occupancy_min"`
	OccupancyMax  string `json:"occupancy_max"`
}

type ExclusionInput struct {
	ReadingID string `json:"reading_id"`
	Reason    string `json:"reason"`
}

type MethodologyInput struct {
	ProjectID              string   `json:"project_id"`
	IncludedMeterIDs       []string `json:"included_meter_ids"`
	HeatingNormalization   string   `json:"heating_normalization"`
	OccupancyNormalization string   `json:"occupancy_normalization"`
	FixedOccupancy         string   `json:"fixed_occupancy"`
	BoundaryNote           string   `json:"boundary_note"`
	ChangeNote             string   `json:"change_note"`
}

type ReportInput struct {
	ProjectID        string `json:"project_id"`
	BaselineWindowID string `json:"baseline_window_id"`
	PostWindowID     string `json:"post_window_id"`
	MethodologyID    string `json:"methodology_id"`
}

type ConclusionInput struct {
	Organization string `json:"organization"`
	InspectorRef string `json:"inspector_ref"`
	Verdict      string `json:"verdict"`
	Finding      string `json:"finding"`
}

// ---------- 校验辅助 ----------

func parseTime(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, badRequest("%s 不能为空", field)
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, badRequest("%s 必须是带时区偏移的 ISO 8601 时间：%v", field, err)
	}
	return t, nil
}

func optionalTime(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	_, err := parseTime(field, value)
	return err
}

func requireDecimal(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return badRequest("%s 不能为空", field)
	}
	if _, err := parseDecimal(value); err != nil {
		return badRequest("%s %v", field, err)
	}
	return nil
}

func enum(field, value string, allowed ...string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return badRequest("%s 必须是 %s 之一，当前为 %q", field, strings.Join(allowed, "/"), value)
}

// ---------- 写操作 ----------

// RegisterBuilding 建立建筑基线档案。
func (s *Service) RegisterBuilding(actor Actor, in BuildingInput) (*Building, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	in.BuildingRef = strings.TrimSpace(in.BuildingRef)
	in.Name = strings.TrimSpace(in.Name)
	in.BuildingType = strings.TrimSpace(in.BuildingType)
	if in.BuildingRef == "" || in.Name == "" || in.BuildingType == "" {
		return nil, badRequest("building_ref、name、building_type 不能为空")
	}
	if err := requireDecimal("floor_area_m2", in.FloorAreaM2); err != nil {
		return nil, err
	}
	if _, exists := s.buildingByRef[in.BuildingRef]; exists {
		return nil, conflict("building_ref %q 已存在", in.BuildingRef)
	}
	city := in.City
	if strings.TrimSpace(city) == "" {
		city = "高碑店"
	}
	b := &Building{
		ID:           s.nextID("BLD", len(s.buildings)),
		BuildingRef:  in.BuildingRef,
		Name:         in.Name,
		City:         city,
		BuildingType: in.BuildingType,
		FloorAreaM2:  in.FloorAreaM2,
		CreatedAt:    nowISO(),
	}
	if _, err := s.appendEvent(actor, evBuildingRegistered, b); err != nil {
		return nil, err
	}
	return b, nil
}

// CreateProject 登记新建或改造项目。
func (s *Service) CreateProject(actor Actor, in ProjectInput) (*Project, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.buildings[in.BuildingID]; !ok {
		return nil, badRequest("building_id %q 不存在", in.BuildingID)
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, badRequest("name 不能为空")
	}
	if err := enum("kind", in.Kind, "new_build", "retrofit"); err != nil {
		return nil, err
	}
	if err := optionalTime("work_start", in.WorkStart); err != nil {
		return nil, err
	}
	if err := optionalTime("work_end", in.WorkEnd); err != nil {
		return nil, err
	}
	p := &Project{
		ID:         s.nextID("PRJ", len(s.projects)),
		BuildingID: in.BuildingID,
		Name:       in.Name,
		Kind:       in.Kind,
		Scope:      in.Scope,
		WorkStart:  in.WorkStart,
		WorkEnd:    in.WorkEnd,
		CreatedAt:  nowISO(),
	}
	if _, err := s.appendEvent(actor, evProjectCreated, p); err != nil {
		return nil, err
	}
	return p, nil
}

// ReplaceEquipment 记录设备更换；宣传值与实测/铭牌值分栏保存。
func (s *Service) ReplaceEquipment(actor Actor, in EquipmentInput) (*EquipmentReplacement, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[in.ProjectID]; !ok {
		return nil, badRequest("project_id %q 不存在", in.ProjectID)
	}
	if strings.TrimSpace(in.Category) == "" {
		return nil, badRequest("category 不能为空")
	}
	if err := optionalTime("replaced_on", in.ReplacedOn); err != nil {
		return nil, err
	}
	// 宣传值若填写，必须标注来源；系统保证其永不进入计算（计算只取读数表）。
	if strings.TrimSpace(in.MarketingClaim) != "" && strings.TrimSpace(in.ClaimSource) == "" {
		return nil, badRequest("填写 marketing_claim（宣传值）时必须填写 claim_source；宣传值不参与节能计算")
	}
	e := &EquipmentReplacement{
		ID:                 s.nextID("EQP", len(s.equipment)),
		ProjectID:          in.ProjectID,
		Category:           in.Category,
		OldDescription:     in.OldDescription,
		NewDescription:     in.NewDescription,
		SupplierName:       in.SupplierName,
		SupplierModel:      in.SupplierModel,
		RatedValue:         in.RatedValue,
		RatedUnit:          in.RatedUnit,
		MarketingClaim:     in.MarketingClaim,
		MarketingClaimUnit: in.MarketingClaimUnit,
		ClaimSource:        in.ClaimSource,
		ReplacedOn:         in.ReplacedOn,
		CreatedAt:          nowISO(),
	}
	if _, err := s.appendEvent(actor, evEquipmentReplaced, e); err != nil {
		return nil, err
	}
	return e, nil
}

// RegisterMeter 登记计量仪表。
func (s *Service) RegisterMeter(actor Actor, in MeterInput) (*Meter, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.buildings[in.BuildingID]; !ok {
		return nil, badRequest("building_id %q 不存在", in.BuildingID)
	}
	if strings.TrimSpace(in.MeterRef) == "" {
		return nil, badRequest("meter_ref 不能为空")
	}
	if _, exists := s.meterByRef[in.MeterRef]; exists {
		return nil, conflict("meter_ref %q 已存在", in.MeterRef)
	}
	if err := enum("meter_type", in.MeterType, "heat", "electric", "gas", "water"); err != nil {
		return nil, err
	}
	if err := optionalTime("installed_on", in.InstalledOn); err != nil {
		return nil, err
	}
	unit := in.Unit
	if strings.TrimSpace(unit) == "" {
		unit = "kWh"
	}
	m := &Meter{
		ID:          s.nextID("MTR", len(s.meters)),
		MeterRef:    in.MeterRef,
		BuildingID:  in.BuildingID,
		MeterType:   in.MeterType,
		Unit:        unit,
		Location:    in.Location,
		Status:      "active",
		InstalledOn: in.InstalledOn,
	}
	if _, err := s.appendEvent(actor, evMeterRegistered, m); err != nil {
		return nil, err
	}
	return m, nil
}

// RecordReading 写入一条不可变现场读数。
func (s *Service) RecordReading(actor Actor, in ReadingInput) (*Reading, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.meters[in.MeterID]
	if !ok {
		return nil, badRequest("meter_id %q 不存在", in.MeterID)
	}
	start, err := parseTime("period_start", in.PeriodStart)
	if err != nil {
		return nil, err
	}
	end, err := parseTime("period_end", in.PeriodEnd)
	if err != nil {
		return nil, err
	}
	if !end.After(start) {
		return nil, badRequest("period_end 必须晚于 period_start")
	}
	if err := requireDecimal("value", in.Value); err != nil {
		return nil, err
	}
	quality := in.Quality
	if quality == "" {
		quality = "good"
	}
	if err := enum("quality", quality, "good", "suspect"); err != nil {
		return nil, err
	}
	unit := in.Unit
	if unit == "" {
		unit = m.Unit
	}
	r := &Reading{
		ID:          s.nextID("RDG", len(s.readings)),
		MeterID:     in.MeterID,
		PeriodStart: in.PeriodStart,
		PeriodEnd:   in.PeriodEnd,
		Value:       in.Value,
		Unit:        unit,
		Quality:     quality,
		SourceRef:   in.SourceRef,
		RecordedBy:  actor.ID,
		RecordedAt:  nowISO(),
	}
	headSeq, _ := s.ledger.Head()
	r.RecordSeq = headSeq + 1 // 本读数事件的链序号
	if _, err := s.appendEvent(actor, evReadingRecorded, r); err != nil {
		return nil, err
	}
	return r, nil
}

// CorrectReading 由校准人员对错误读数追加更正；原读数保留且不可再更正。
func (s *Service) CorrectReading(actor Actor, readingID string, in CorrectionInput) (*Correction, error) {
	if err := s.require(actor, RoleCalibration); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.readings[readingID]
	if !ok {
		return nil, notFound("读数 %q 不存在", readingID)
	}
	if _, exists := s.corrections[readingID]; exists {
		return nil, conflict("读数 %q 已存在更正；为保留证据链，一条读数至多更正一次", readingID)
	}
	if err := requireDecimal("corrected_value", in.CorrectedValue); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, badRequest("reason（更正原因）不能为空")
	}
	c := &Correction{
		ID:             s.nextID("COR", len(s.corrections)),
		ReadingID:      readingID,
		OriginalValue:  r.Value, // 固化原读数
		CorrectedValue: in.CorrectedValue,
		Reason:         in.Reason,
		CalibrationRef: in.CalibrationRef,
		CorrectedBy:    actor.ID,
		CorrectedAt:    nowISO(),
	}
	if _, err := s.appendEvent(actor, evReadingCorrected, c); err != nil {
		return nil, err
	}
	return c, nil
}

// RecordIndoor 写入分时段室内环境/占用率记录。
func (s *Service) RecordIndoor(actor Actor, in IndoorInput) (*Indoor, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.buildings[in.BuildingID]; !ok {
		return nil, badRequest("building_id %q 不存在", in.BuildingID)
	}
	if _, err := parseTime("ts", in.TS); err != nil {
		return nil, err
	}
	if in.HeatingSeason == nil {
		return nil, badRequest("heating_season 必填（0/1）")
	}
	if *in.HeatingSeason != 0 && *in.HeatingSeason != 1 {
		return nil, badRequest("heating_season 必须是 0 或 1")
	}
	if in.TempC != "" {
		if err := requireDecimal("temp_c", in.TempC); err != nil {
			return nil, err
		}
	}
	if in.OccupancyRate != "" {
		o, err := parseDecimal(in.OccupancyRate)
		if err != nil {
			return nil, badRequest("occupancy_rate %v", err)
		}
		if o.sign() < 0 || o.sub(mustParseDecimal("1")).sign() > 0 {
			return nil, badRequest("occupancy_rate 必须在 0~1 之间")
		}
	}
	v := &Indoor{
		ID:            s.nextID("IND", len(s.indoor)),
		BuildingID:    in.BuildingID,
		TS:            in.TS,
		TempC:         in.TempC,
		HumidityPct:   in.HumidityPct,
		CO2Ppm:        in.CO2Ppm,
		OccupancyRate: in.OccupancyRate,
		HeatingSeason: *in.HeatingSeason,
		Note:          in.Note,
	}
	if _, err := s.appendEvent(actor, evIndoorRecorded, v); err != nil {
		return nil, err
	}
	return v, nil
}

// OpenWindow 建立测量窗口（草稿），绑定口径版本。
func (s *Service) OpenWindow(actor Actor, in WindowInput) (*Window, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[in.ProjectID]; !ok {
		return nil, badRequest("project_id %q 不存在", in.ProjectID)
	}
	meth, ok := s.methodologies[in.MethodologyID]
	if !ok {
		return nil, badRequest("methodology_id %q 不存在", in.MethodologyID)
	}
	if meth.ProjectID != in.ProjectID {
		return nil, badRequest("口径 %s 不属于项目 %s", in.MethodologyID, in.ProjectID)
	}
	if err := enum("kind", in.Kind, "baseline", "post"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Label) == "" {
		return nil, badRequest("label 不能为空")
	}
	start, err := parseTime("period_start", in.PeriodStart)
	if err != nil {
		return nil, err
	}
	end, err := parseTime("period_end", in.PeriodEnd)
	if err != nil {
		return nil, err
	}
	if !end.After(start) {
		return nil, badRequest("period_end 必须晚于 period_start")
	}
	if in.HeatingSeason == nil {
		return nil, badRequest("heating_season 必填（0/1）")
	}
	if *in.HeatingSeason != 0 && *in.HeatingSeason != 1 {
		return nil, badRequest("heating_season 必须是 0 或 1")
	}
	if in.HDD != "" {
		if err := requireDecimal("hdd", in.HDD); err != nil {
			return nil, err
		}
	}
	w := &Window{
		ID:            s.nextID("WIN", len(s.windows)),
		ProjectID:     in.ProjectID,
		MethodologyID: in.MethodologyID,
		Label:         in.Label,
		Kind:          in.Kind,
		PeriodStart:   in.PeriodStart,
		PeriodEnd:     in.PeriodEnd,
		HeatingSeason: *in.HeatingSeason,
		HDD:           in.HDD,
		OccupancyMin:  in.OccupancyMin,
		OccupancyMax:  in.OccupancyMax,
		Status:        "draft",
		CreatedAt:     nowISO(),
	}
	if _, err := s.appendEvent(actor, evWindowOpened, w); err != nil {
		return nil, err
	}
	return w, nil
}

// AddExclusion 在草稿窗口内剔除读数，必须给出原因。
func (s *Service) AddExclusion(actor Actor, windowID string, in ExclusionInput) (*Exclusion, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.windows[windowID]
	if !ok {
		return nil, notFound("测量窗口 %q 不存在", windowID)
	}
	if w.Status != "draft" {
		return nil, conflict("窗口 %s 已终结，不能再剔除读数", windowID)
	}
	r, ok := s.readings[in.ReadingID]
	if !ok {
		return nil, badRequest("reading_id %q 不存在", in.ReadingID)
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, badRequest("reason（剔除原因）不能为空")
	}
	meth := s.methodologies[w.MethodologyID]
	if !contains(meth.IncludedMeterIDs, r.MeterID) {
		return nil, badRequest("读数 %s 的仪表 %s 不在窗口口径纳入范围内", r.ID, r.MeterID)
	}
	if overlapSeconds(r.PeriodStart, r.PeriodEnd, w.PeriodStart, w.PeriodEnd).sign() == 0 {
		return nil, badRequest("读数 %s 的时段与窗口 %s 不重叠", r.ID, windowID)
	}
	for _, ex := range s.exclusions[windowID] {
		if ex.ReadingID == in.ReadingID {
			return nil, conflict("读数 %s 在该窗口已被剔除", in.ReadingID)
		}
	}
	x := &Exclusion{
		ID:         s.nextID("EXC", s.exclusionSeq),
		WindowID:   windowID,
		ReadingID:  in.ReadingID,
		Reason:     in.Reason,
		ExcludedBy: actor.ID,
		ExcludedAt: nowISO(),
	}
	if _, err := s.appendEvent(actor, evReadingExcluded, x); err != nil {
		return nil, err
	}
	return x, nil
}

// FinalizeWindow 终结窗口；口径必须已锁定，且窗口通过覆盖度检查。
func (s *Service) FinalizeWindow(actor Actor, windowID string) (*Window, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.windows[windowID]
	if !ok {
		return nil, notFound("测量窗口 %q 不存在", windowID)
	}
	if w.Status == "finalized" {
		return w, nil
	}
	meth := s.methodologies[w.MethodologyID]
	if meth.Status != "locked" {
		return nil, conflict("窗口绑定的口径 %s 尚未 locked，窗口不能终结", w.MethodologyID)
	}
	if blockers := s.windowBlockers(w, meth); len(blockers) > 0 {
		return nil, &GateError{Blockers: blockers}
	}
	final := *w
	final.Status = "finalized"
	final.FinalizedAt = nowISO()
	headSeq, _ := s.ledger.Head()
	final.FinalizeSeq = headSeq + 1 // 终结事件链序号：读数集合冻结锚点
	if _, err := s.appendEvent(actor, evWindowFinalized, map[string]any{"window": &final}); err != nil {
		return nil, err
	}
	return &final, nil
}

// CreateMethodology 建立口径新版本（草稿），自动归并到同项目最新版本之后。
func (s *Service) CreateMethodology(actor Actor, in MethodologyInput) (*Methodology, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.projects[in.ProjectID]
	if !ok {
		return nil, badRequest("project_id %q 不存在", in.ProjectID)
	}
	if len(in.IncludedMeterIDs) == 0 {
		return nil, badRequest("included_meter_ids 至少包含一块仪表")
	}
	seen := map[string]bool{}
	for _, id := range in.IncludedMeterIDs {
		if seen[id] {
			return nil, badRequest("included_meter_ids 中 %s 重复", id)
		}
		seen[id] = true
		m, ok := s.meters[id]
		if !ok {
			return nil, badRequest("included_meter_ids 中 %s 不存在", id)
		}
		if m.BuildingID != p.BuildingID {
			return nil, badRequest("仪表 %s 不属于项目所在建筑", id)
		}
	}
	hn := in.HeatingNormalization
	if hn == "" {
		hn = "none"
	}
	on := in.OccupancyNormalization
	if on == "" {
		on = "none"
	}
	if err := enum("heating_normalization", hn, "none", "degree_day"); err != nil {
		return nil, err
	}
	if err := enum("occupancy_normalization", on, "none", "fixed", "actual"); err != nil {
		return nil, err
	}
	if on == "fixed" {
		if err := requireDecimal("fixed_occupancy", in.FixedOccupancy); err != nil {
			return nil, err
		}
		fo := mustParseDecimal(in.FixedOccupancy)
		if fo.sign() < 0 || fo.sub(mustParseDecimal("1")).sign() > 0 || fo.isZero() {
			return nil, badRequest("fixed_occupancy 必须在 (0,1] 之间")
		}
	}
	version := 1
	var parentID string
	for _, m := range s.methodologies {
		if m.ProjectID == in.ProjectID {
			if m.Version+1 > version {
				version = m.Version + 1
			}
			if m.Version == version-1 {
				parentID = m.ID
			}
		}
	}
	meth := &Methodology{
		ID:                     s.nextID("MET", len(s.methodologies)),
		ProjectID:              in.ProjectID,
		Version:                version,
		ParentID:               parentID,
		Status:                 "draft",
		IncludedMeterIDs:       append([]string{}, in.IncludedMeterIDs...),
		HeatingNormalization:   hn,
		OccupancyNormalization: on,
		FixedOccupancy:         in.FixedOccupancy,
		BoundaryNote:           in.BoundaryNote,
		ChangeNote:             in.ChangeNote,
		CreatedAt:              nowISO(),
	}
	sort.Strings(meth.IncludedMeterIDs)
	if _, err := s.appendEvent(actor, evMethodologyCreated, meth); err != nil {
		return nil, err
	}
	return meth, nil
}

// LockMethodology 锁定口径；同项目此前锁定的版本转为 superseded。
func (s *Service) LockMethodology(actor Actor, id string) (*Methodology, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.methodologies[id]
	if !ok {
		return nil, notFound("口径 %q 不存在", id)
	}
	if m.Status == "locked" {
		return m, nil
	}
	if m.Status == "superseded" {
		return nil, conflict("口径 %s 已被新版本取代，不能锁定", id)
	}
	locked := *m
	locked.Status = "locked"
	var superseded []string
	for _, other := range s.methodologies {
		if other.ProjectID == m.ProjectID && other.ID != m.ID && other.Status == "locked" {
			superseded = append(superseded, other.ID)
		}
	}
	sort.Strings(superseded)
	if _, err := s.appendEvent(actor, evMethodologyLocked, map[string]any{
		"methodology":    &locked,
		"superseded_ids": superseded,
	}); err != nil {
		return nil, err
	}
	return &locked, nil
}

// PublishReport 在发布闸门通过后发布节能指标。
func (s *Service) PublishReport(actor Actor, in ReportInput) (*Report, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	proj, ok := s.projects[in.ProjectID]
	if !ok {
		return nil, badRequest("project_id %q 不存在", in.ProjectID)
	}
	base, ok := s.windows[in.BaselineWindowID]
	if !ok {
		return nil, badRequest("baseline_window_id %q 不存在", in.BaselineWindowID)
	}
	post, ok := s.windows[in.PostWindowID]
	if !ok {
		return nil, badRequest("post_window_id %q 不存在", in.PostWindowID)
	}
	if base.Kind != "baseline" || post.Kind != "post" {
		return nil, badRequest("窗口角色错误：baseline_window_id 必须是 baseline 窗口，post_window_id 必须是 post 窗口")
	}
	if base.ProjectID != in.ProjectID || post.ProjectID != in.ProjectID {
		return nil, badRequest("两个窗口必须属于项目 %s", in.ProjectID)
	}
	if mustParseTime(post.PeriodStart).Before(mustParseTime(base.PeriodEnd)) {
		return nil, badRequest("改造后窗口不得早于基线窗口结束，避免改造措施混入基线时段（半开区间端点可相接）")
	}
	meth, ok := s.methodologies[in.MethodologyID]
	if !ok {
		return nil, badRequest("methodology_id %q 不存在", in.MethodologyID)
	}

	var blockers []string
	// 发布口径必须与窗口绑定口径相同，或属于同项目的更新版本（口径升级后
	// 可在同一对实测窗口上重算），杜绝跨项目、跨版本线套用口径。
	if !s.methodologyApplicable(meth, base.MethodologyID) || !s.methodologyApplicable(meth, post.MethodologyID) {
		blockers = append(blockers, fmt.Sprintf("发布口径 %s 与窗口绑定口径（基线 %s / 改造后 %s）不属于同一口径版本线", meth.ID, base.MethodologyID, post.MethodologyID))
	}
	if base.Status != "finalized" {
		blockers = append(blockers, fmt.Sprintf("基线窗口 %s 状态为 %s，尚未 finalized", base.ID, base.Status))
	}
	if post.Status != "finalized" {
		blockers = append(blockers, fmt.Sprintf("改造后窗口 %s 状态为 %s，尚未 finalized", post.ID, post.Status))
	}
	if meth.Status != "locked" {
		blockers = append(blockers, fmt.Sprintf("口径 %s 状态为 %s，必须 locked 才能发布", meth.ID, meth.Status))
	}
	blockers = append(blockers, s.windowBlockers(base, meth)...)
	blockers = append(blockers, s.windowBlockers(post, meth)...)
	if meth.HeatingNormalization == "none" && base.HeatingSeason != post.HeatingSeason {
		blockers = append(blockers, "两窗口分跨采暖季与非采暖季，但口径未做采暖度日修正（heating_normalization=none）")
	}
	if len(blockers) > 0 {
		return nil, &GateError{Blockers: uniqueSorted(blockers)}
	}

	calc, err := s.compute(base, post, meth)
	if err != nil {
		return nil, err
	}

	// 口径变化差额：仅当与上一版报告使用完全相同的一对窗口时可归因于口径，
	// 否则不给数字，避免把窗口或数据变化误记为口径差额。
	var delta *string
	var priorID, changeBasis string
	if prior := s.latestReport(in.ProjectID); prior != nil {
		priorID = prior.ID
		changeBasis = "windows_or_data_changed"
		if prior.BaselineWindowID == base.ID && prior.PostWindowID == post.ID {
			priorMeth := s.methodologies[prior.MethodologyID]
			if priorMeth != nil && priorMeth.ID != meth.ID {
				if priorCalc, perr := s.compute(base, post, priorMeth); perr == nil {
					d := calc.Savings.sub(priorCalc.Savings)
					ds := d.fixed(3)
					delta = &ds
					changeBasis = "same_windows_methodology_changed"
				}
			} else {
				changeBasis = "same_windows_same_methodology"
			}
		}
	}

	snapshot := s.buildSnapshot(proj, base, post, meth, calc, priorID, delta, changeBasis)

	seq, head := s.ledger.Head() // 发布锚点：发布固化时的链尾
	r := &Report{
		ID:                     s.nextID("RPT", len(s.reports)),
		ProjectID:              in.ProjectID,
		BaselineWindowID:       base.ID,
		PostWindowID:           post.ID,
		MethodologyID:          meth.ID,
		PriorReportID:          priorID,
		Status:                 "published",
		BaselineRawKWh:         calc.BaseRaw.fixed(3),
		PostRawKWh:             calc.PostRaw.fixed(3),
		BaselineAdjustedKWh:    calc.BaseAdjusted.fixed(3),
		PostAdjustedKWh:        calc.PostAdjusted.fixed(3),
		SavingsKWh:             calc.Savings.fixed(3),
		SavingsPct:             calc.SavingsPct.fixed(2),
		MethodologyChangeDelta: delta,
		LedgerSeq:              seq,
		LedgerHead:             head,
		Snapshot:               snapshot,
		PublishedBy:            actor.ID,
		PublishedAt:            nowISO(),
	}
	if _, err := s.appendEvent(actor, evReportPublished, r); err != nil {
		return nil, err
	}
	return r, nil
}

// RetractReport 撤回报告（只追加撤回事件，编号、快照与结论均保留）。
func (s *Service) RetractReport(actor Actor, id, reason string) (*Report, error) {
	if err := s.require(actor, RoleField); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reports[id]
	if !ok {
		return nil, notFound("报告 %q 不存在", id)
	}
	if r.Status == "retracted" {
		return nil, conflict("报告 %s 已撤回", id)
	}
	if strings.TrimSpace(reason) == "" {
		return nil, badRequest("撤回原因不能为空")
	}
	if _, err := s.appendEvent(actor, evReportRetracted, map[string]any{"id": id, "reason": reason, "retracted_at": nowISO()}); err != nil {
		return nil, err
	}
	r.Status = "retracted"
	return r, nil
}

// AttachConclusion 第三方对报告追加结论；系统中不存在修改或删除结论的路径。
func (s *Service) AttachConclusion(actor Actor, reportID string, in ConclusionInput) (*Conclusion, error) {
	if err := s.require(actor, RoleThirdParty); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.reports[reportID]
	if !ok {
		return nil, notFound("报告 %q 不存在", reportID)
	}
	if r.Status != "published" {
		return nil, conflict("报告 %s 已撤回，不能再附结论", reportID)
	}
	if strings.TrimSpace(in.Organization) == "" {
		return nil, badRequest("organization 不能为空")
	}
	if err := enum("verdict", in.Verdict, "pass", "conditional", "fail"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Finding) == "" {
		return nil, badRequest("finding 不能为空")
	}
	c := &Conclusion{
		ID:           s.nextID("CON", s.conclusionSeq),
		ReportID:     reportID,
		Organization: in.Organization,
		InspectorRef: in.InspectorRef,
		Verdict:      in.Verdict,
		Finding:      in.Finding,
		AttachedBy:   actor.ID,
		AttachedAt:   nowISO(),
	}
	if _, err := s.appendEvent(actor, evConclusionAttached, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ---------- 查询 ----------

func (s *Service) GetReport(id string) (*Report, []*Conclusion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.reports[id]
	if !ok {
		return nil, nil, notFound("报告 %q 不存在", id)
	}
	cp := *r
	cs := append([]*Conclusion{}, s.conclusions[id]...)
	return &cp, cs, nil
}

func (s *Service) ListReports(projectID string) []*Report {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Report, 0)
	for _, id := range s.reportOrder {
		r := s.reports[id]
		if projectID != "" && r.ProjectID != projectID {
			continue
		}
		cp := *r
		out = append(out, &cp)
	}
	return out
}

func (s *Service) GetReading(id string) (*Reading, *Correction, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.readings[id]
	if !ok {
		return nil, nil, false
	}
	rcp := *r
	var ccp *Correction
	if c := s.corrections[id]; c != nil {
		x := *c
		ccp = &x
	}
	return &rcp, ccp, true
}

func (s *Service) GetWindow(id string) (*Window, []*Exclusion, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.windows[id]
	if !ok {
		return nil, nil, false
	}
	wcp := *w
	xs := append([]*Exclusion{}, s.exclusions[id]...)
	return &wcp, xs, true
}

func (s *Service) latestReport(projectID string) *Report {
	for i := len(s.reportOrder) - 1; i >= 0; i-- {
		r := s.reports[s.reportOrder[i]]
		if r.ProjectID == projectID {
			return r
		}
	}
	return nil
}

// ---------- 计算 ----------

type readingUse struct {
	WindowKind     string      `json:"window_kind"`
	MeterID        string      `json:"meter_id"`
	Reading        Reading     `json:"reading"`
	Correction     *Correction `json:"correction,omitempty"`
	EffectiveValue string      `json:"effective_value"`
	Share          string      `json:"window_share"`
	Contribution   string      `json:"contribution_kwh"`
}

type windowCalc struct {
	Raw      decimal
	Adjusted decimal
	Used     []readingUse
	Late     []map[string]any // 窗口终结后才录入、虽与时段重叠但不参与计算的读数
	OccAvg   decimal
	OccCount int
}

func newWindowCalc() *windowCalc {
	return &windowCalc{
		Raw:  zeroDecimal(),
		Used: []readingUse{},
		Late: []map[string]any{},
	}
}

type fullCalc struct {
	BaseRaw      decimal
	PostRaw      decimal
	BaseAdjusted decimal
	PostAdjusted decimal
	Savings      decimal
	SavingsPct   decimal
	Base         *windowCalc
	Post         *windowCalc
	Unit         string
}

// effectiveValue 返回计算采用值：有更正用更正值，否则用原读数。
func (s *Service) effectiveValue(r *Reading) decimal {
	if c, ok := s.corrections[r.ID]; ok {
		return mustParseDecimal(c.CorrectedValue)
	}
	return mustParseDecimal(r.Value)
}

func (s *Service) excludedSet(windowID string) map[string]*Exclusion {
	set := map[string]*Exclusion{}
	for _, x := range s.exclusions[windowID] {
		set[x.ReadingID] = x
	}
	return set
}

// frozenOutOfWindow 判断读数是否在窗口终结冻结之后才录入：
// 以链序号为锚（终结事件序号），终结后补录的读数不得进入该窗口。
// 校准更正不受影响——更正始终反映到已入窗读数的采用值上。
func (s *Service) frozenOutOfWindow(r *Reading, w *Window) bool {
	if w.FinalizeSeq == 0 {
		return false
	}
	return r.RecordSeq > w.FinalizeSeq
}

func frozenReadingView(r *Reading, w *Window, cor *Correction) map[string]any {
	v := map[string]any{
		"reading_id":   r.ID,
		"meter_id":     r.MeterID,
		"period_start": r.PeriodStart,
		"period_end":   r.PeriodEnd,
		"value":        r.Value,
		"recorded_at":  r.RecordedAt,
		"record_seq":   r.RecordSeq,
		"finalize_seq": w.FinalizeSeq,
		"reason":       "读数在窗口终结冻结之后才录入，不参与该窗口计算",
	}
	if cor != nil {
		v["corrected_value"] = cor.CorrectedValue
	}
	return v
}

// windowBlockers 检查窗口在口径下的可计算性。
func (s *Service) windowBlockers(w *Window, meth *Methodology) []string {
	var blockers []string
	excluded := s.excludedSet(w.ID)
	buildingID := s.projects[w.ProjectID].BuildingID
	unit := ""
	for _, meterID := range meth.IncludedMeterIDs {
		m := s.meters[meterID]
		hasEffective := false
		for _, r := range s.readings {
			if r.MeterID != meterID {
				continue
			}
			if _, isExcluded := excluded[r.ID]; isExcluded {
				continue
			}
			if s.frozenOutOfWindow(r, w) {
				continue
			}
			if overlapSeconds(r.PeriodStart, r.PeriodEnd, w.PeriodStart, w.PeriodEnd).sign() == 0 {
				continue
			}
			hasEffective = true
			if unit == "" {
				unit = r.Unit
			} else if r.Unit != unit {
				blockers = append(blockers, fmt.Sprintf("窗口 %s 纳入读数存在不一致单位（%s 与 %s），无法合计", w.Label, unit, r.Unit))
			}
		}
		if !hasEffective {
			blockers = append(blockers, fmt.Sprintf("窗口 %s 中仪表 %s（%s）没有任何有效读数", w.Label, meterID, m.MeterRef))
		}
	}
	if meth.HeatingNormalization == "degree_day" {
		hdd := strings.TrimSpace(w.HDD)
		if hdd == "" {
			blockers = append(blockers, fmt.Sprintf("窗口 %s 口径要求度日修正，但缺少 hdd", w.Label))
		} else if d, err := parseDecimal(hdd); err != nil || d.sign() <= 0 {
			blockers = append(blockers, fmt.Sprintf("窗口 %s 的 hdd 必须为正数", w.Label))
		}
	}
	if meth.OccupancyNormalization != "none" {
		count := 0
		for _, rec := range s.indoor {
			if rec.BuildingID != buildingID || rec.OccupancyRate == "" {
				continue
			}
			if tsWithin(rec.TS, w.PeriodStart, w.PeriodEnd) {
				count++
			}
		}
		if count == 0 {
			blockers = append(blockers, fmt.Sprintf("窗口 %s 口径要求占用率修正，但窗口内没有 occupancy_rate 室内记录", w.Label))
		}
	}
	return uniqueSorted(blockers)
}

func (s *Service) computeWindow(w *Window, meth *Methodology) *windowCalc {
	excluded := s.excludedSet(w.ID)
	c := newWindowCalc()
	for _, meterID := range meth.IncludedMeterIDs {
		var list []*Reading
		for _, r := range s.readings {
			if r.MeterID != meterID {
				continue
			}
			list = append(list, r)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].PeriodStart < list[j].PeriodStart })
		for _, r := range list {
			if _, isExcluded := excluded[r.ID]; isExcluded {
				continue
			}
			span := overlapSeconds(r.PeriodStart, r.PeriodEnd, w.PeriodStart, w.PeriodEnd)
			if span.sign() == 0 {
				continue
			}
			if s.frozenOutOfWindow(r, w) {
				c.Late = append(c.Late, frozenReadingView(r, w, s.corrections[r.ID]))
				continue
			}
			total := secondsBetween(r.PeriodStart, r.PeriodEnd)
			share := span.quo(total)
			val := s.effectiveValue(r)
			contribution := val.mul(share)
			c.Raw = c.Raw.add(contribution)
			use := readingUse{
				WindowKind:     w.Kind,
				MeterID:        meterID,
				Reading:        *r,
				EffectiveValue: s.effectiveValue(r).fixed(3),
				Share:          share.fixed(6),
				Contribution:   contribution.fixed(3),
			}
			if cor, ok := s.corrections[r.ID]; ok {
				x := *cor
				use.Correction = &x
			}
			c.Used = append(c.Used, use)
		}
	}
	var occSum = zeroDecimal()
	occN := 0
	buildingID := s.projects[w.ProjectID].BuildingID
	for _, rec := range s.indoor {
		if rec.BuildingID != buildingID || rec.OccupancyRate == "" {
			continue
		}
		if tsWithin(rec.TS, w.PeriodStart, w.PeriodEnd) {
			occSum = occSum.add(mustParseDecimal(rec.OccupancyRate))
			occN++
		}
	}
	if occN > 0 {
		c.OccAvg = occSum.quo(mustParseDecimal(strconv.Itoa(occN)))
	}
	c.OccCount = occN
	c.Adjusted = c.Raw
	sort.Slice(c.Used, func(i, j int) bool {
		if c.Used[i].MeterID != c.Used[j].MeterID {
			return c.Used[i].MeterID < c.Used[j].MeterID
		}
		return c.Used[i].Reading.PeriodStart < c.Used[j].Reading.PeriodStart
	})
	return c
}

func (s *Service) compute(base, post *Window, meth *Methodology) (*fullCalc, error) {
	cb := s.computeWindow(base, meth)
	cp := s.computeWindow(post, meth)
	f := &fullCalc{Base: cb, Post: cp, BaseRaw: cb.Raw, PostRaw: cp.Raw, Unit: "kWh"}

	baseAdj := cb.Raw
	postAdj := cp.Raw

	// 度日修正：把改造后窗口归一到基线窗口的采暖强度。
	if meth.HeatingNormalization == "degree_day" {
		hddB := mustParseDecimal(base.HDD)
		hddP := mustParseDecimal(post.HDD)
		if hddP.sign() > 0 {
			postAdj = postAdj.mul(hddB.quo(hddP))
		}
	}

	// 占用率修正：归一到同一参考占用率。
	switch meth.OccupancyNormalization {
	case "fixed":
		ref := mustParseDecimal(meth.FixedOccupancy)
		if cb.OccCount > 0 && cb.OccAvg.sign() > 0 {
			baseAdj = baseAdj.mul(ref.quo(cb.OccAvg))
		}
		if cp.OccCount > 0 && cp.OccAvg.sign() > 0 {
			postAdj = postAdj.mul(ref.quo(cp.OccAvg))
		}
	case "actual":
		// 以基线窗口实际占用率为共同参考，基线保持不变。
		if cp.OccCount > 0 && cp.OccAvg.sign() > 0 && cb.OccAvg.sign() > 0 {
			postAdj = postAdj.mul(cb.OccAvg.quo(cp.OccAvg))
		}
	}

	cb.Adjusted = baseAdj
	cp.Adjusted = postAdj
	f.BaseAdjusted = baseAdj
	f.PostAdjusted = postAdj
	f.Savings = baseAdj.sub(postAdj)
	if baseAdj.sign() != 0 {
		f.SavingsPct = f.Savings.quo(baseAdj).mul(mustParseDecimal("100"))
	} else {
		f.SavingsPct = zeroDecimal()
	}
	return f, nil
}

// buildSnapshot 固化业主可追溯报告所需的全部要素。
func (s *Service) buildSnapshot(
	proj *Project, base, post *Window, meth *Methodology, calc *fullCalc,
	priorID string, delta *string, changeBasis string,
) map[string]any {
	b := s.buildings[proj.BuildingID]

	windowView := func(w *Window, c *windowCalc) map[string]any {
		excludedList := make([]map[string]any, 0)
		for _, x := range s.exclusions[w.ID] {
			r := s.readings[x.ReadingID]
			entry := map[string]any{
				"exclusion_id":   x.ID,
				"reading_id":     r.ID,
				"meter_id":       r.MeterID,
				"period_start":   r.PeriodStart,
				"period_end":     r.PeriodEnd,
				"original_value": r.Value,
				"unit":           r.Unit,
				"quality":        r.Quality,
				"reason":         x.Reason,
				"excluded_by":    x.ExcludedBy,
				"excluded_at":    x.ExcludedAt,
			}
			if cor, ok := s.corrections[r.ID]; ok {
				entry["corrected_value"] = cor.CorrectedValue
				entry["correction_reason"] = cor.Reason
			}
			excludedList = append(excludedList, entry)
		}
		return map[string]any{
			"window_id":                        w.ID,
			"label":                            w.Label,
			"kind":                             w.Kind,
			"bound_methodology_id":             w.MethodologyID,
			"period_start":                     w.PeriodStart,
			"period_end":                       w.PeriodEnd,
			"heating_season":                   w.HeatingSeason,
			"hdd":                              w.HDD,
			"status":                           w.Status,
			"raw_total":                        c.Raw.fixed(3),
			"adjusted_total":                   c.Adjusted.fixed(3),
			"occupancy_avg":                    ternary(c.OccCount > 0, c.OccAvg.fixed(4), ""),
			"occupancy_record_count":           c.OccCount,
			"readings_used":                    c.Used,
			"excluded_readings":                excludedList,
			"recorded_after_finalize_readings": c.Late,
			"reading_set_frozen_at":            w.FinalizedAt,
		}
	}

	metersView := make([]map[string]any, 0, len(meth.IncludedMeterIDs))
	for _, id := range meth.IncludedMeterIDs {
		m := s.meters[id]
		metersView = append(metersView, map[string]any{
			"meter_id": m.ID, "meter_ref": m.MeterRef,
			"meter_type": m.MeterType, "unit": m.Unit, "location": m.Location,
		})
	}

	change := map[string]any{
		"prior_report_id": priorID,
		"basis":           changeBasis,
		"delta_kwh":       nil,
		"note":            "delta_kwh 仅在新旧报告使用同一对窗口、仅口径变化时给出，以保证差额可归因于口径；窗口或数据变化时为空。",
	}
	if delta != nil {
		change["delta_kwh"] = *delta
	}

	return map[string]any{
		"project": map[string]any{
			"project_id": proj.ID, "name": proj.Name, "kind": proj.Kind,
			"building_id": b.ID, "building_ref": b.BuildingRef, "building_name": b.Name,
		},
		"windows": map[string]any{
			"baseline": windowView(base, calc.Base),
			"post":     windowView(post, calc.Post),
		},
		"methodology":     meth,
		"included_meters": metersView,
		"adjustment": map[string]any{
			"heating_normalization":   meth.HeatingNormalization,
			"occupancy_normalization": meth.OccupancyNormalization,
			"fixed_occupancy":         meth.FixedOccupancy,
			"baseline_hdd":            base.HDD,
			"post_hdd":                post.HDD,
		},
		"totals": map[string]any{
			"unit":              calc.Unit,
			"baseline_raw":      calc.BaseRaw.fixed(3),
			"post_raw":          calc.PostRaw.fixed(3),
			"baseline_adjusted": calc.BaseAdjusted.fixed(3),
			"post_adjusted":     calc.PostAdjusted.fixed(3),
		},
		"savings": map[string]any{
			"savings_kwh": calc.Savings.fixed(3),
			"savings_pct": calc.SavingsPct.fixed(2),
		},
		"methodology_change":     change,
		"marketing_claim_policy": "设备宣传值不参与本报告任何计算；设备档案中的 marketing_claim 仅供参考。",
	}
}

// ---------- 小工具 ----------

func nowISO() string { return time.Now().Format(time.RFC3339) }

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// methodologyApplicable 判断发布口径能否用于绑定了 boundMethodologyID 的窗口：
// 同项目且版本不早于窗口绑定版本（同版本重发或后续版本口径升级）。
func (s *Service) methodologyApplicable(meth *Methodology, boundMethodologyID string) bool {
	bound, ok := s.methodologies[boundMethodologyID]
	if !ok || bound.ProjectID != meth.ProjectID {
		return false
	}
	return meth.ID == bound.ID || meth.Version >= bound.Version
}

func uniqueSorted(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// overlapSeconds 计算读数时段与窗口时段的重叠秒数（精确有理数）。
func overlapSeconds(rStart, rEnd, wStart, wEnd string) decimal {
	rs := mustParseTime(rStart)
	re := mustParseTime(rEnd)
	ws := mustParseTime(wStart)
	we := mustParseTime(wEnd)
	lo := rs
	if ws.After(lo) {
		lo = ws
	}
	hi := re
	if we.Before(hi) {
		hi = we
	}
	if !hi.After(lo) {
		return zeroDecimal()
	}
	return ratSeconds(hi.Sub(lo))
}

func secondsBetween(start, end string) decimal {
	return ratSeconds(mustParseTime(end).Sub(mustParseTime(start)))
}

func tsWithin(ts, start, end string) bool {
	t := mustParseTime(ts)
	return !t.Before(mustParseTime(start)) && t.Before(mustParseTime(end))
}

func mustParseTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		panic(err)
	}
	return t
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
