// Package archive 是节能改造测量档案的领域层：建筑基线、设备更换、
// 不可变读数、校准更正、分时段能耗、室内环境、测量窗口、读数剔除、
// 版本化口径、报告发布与第三方结论。
//
// 角色边界：现场检测团队(field)、校准人员(calibration)、第三方(third_party)。
// 供应商不是系统角色，其宣传数值仅作参考字段保存。
package archive

// 角色常量。
const (
	RoleField       = "field"
	RoleCalibration = "calibration"
	RoleThirdParty  = "third_party"
	RoleOwner       = "owner" // 业主：只读，可查询报告与审计，不能写任何记录
)

// Building 建筑基线档案。
type Building struct {
	ID           string `json:"id"`
	BuildingRef  string `json:"building_ref"`
	Name         string `json:"name"`
	City         string `json:"city"`
	BuildingType string `json:"building_type"`
	FloorAreaM2  string `json:"floor_area_m2"`
	CreatedAt    string `json:"created_at"`
}

// Project 新建或改造项目。
type Project struct {
	ID         string `json:"id"`
	BuildingID string `json:"building_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // new_build | retrofit
	Scope      string `json:"scope"`
	WorkStart  string `json:"work_start,omitempty"`
	WorkEnd    string `json:"work_end,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// EquipmentReplacement 设备更换记录；宣传值与实测/铭牌值分栏。
type EquipmentReplacement struct {
	ID                 string `json:"id"`
	ProjectID          string `json:"project_id"`
	Category           string `json:"category"`
	OldDescription     string `json:"old_description"`
	NewDescription     string `json:"new_description"`
	SupplierName       string `json:"supplier_name"`
	SupplierModel      string `json:"supplier_model"`
	RatedValue         string `json:"rated_value,omitempty"`
	RatedUnit          string `json:"rated_unit"`
	MarketingClaim     string `json:"marketing_claim,omitempty"`
	MarketingClaimUnit string `json:"marketing_claim_unit"`
	ClaimSource        string `json:"claim_source"`
	ReplacedOn         string `json:"replaced_on"`
	CreatedAt          string `json:"created_at"`
}

// Meter 计量仪表。
type Meter struct {
	ID          string `json:"id"`
	MeterRef    string `json:"meter_ref"`
	BuildingID  string `json:"building_id"`
	MeterType   string `json:"meter_type"` // heat | electric | gas | water
	Unit        string `json:"unit"`
	Location    string `json:"location"`
	Status      string `json:"status"` // active | retired
	InstalledOn string `json:"installed_on"`
}

// Reading 不可变现场读数。Value 为原读数，终身不改。
type Reading struct {
	ID          string `json:"id"`
	MeterID     string `json:"meter_id"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Value       string `json:"value"`
	Unit        string `json:"unit"`
	Quality     string `json:"quality"` // good | suspect
	SourceRef   string `json:"source_ref"`
	RecordedBy  string `json:"recorded_by"`
	RecordedAt  string `json:"recorded_at"`
	RecordSeq   int64  `json:"record_seq"` // 哈希链序号，用于窗口冻结判定
}

// Correction 校准更正（只追加）。
type Correction struct {
	ID             string `json:"id"`
	ReadingID      string `json:"reading_id"`
	OriginalValue  string `json:"original_value"`
	CorrectedValue string `json:"corrected_value"`
	Reason         string `json:"reason"`
	CalibrationRef string `json:"calibration_ref"`
	CorrectedBy    string `json:"corrected_by"`
	CorrectedAt    string `json:"corrected_at"`
}

// Indoor 室内环境与占用率分时段记录。
type Indoor struct {
	ID            string `json:"id"`
	BuildingID    string `json:"building_id"`
	TS            string `json:"ts"`
	TempC         string `json:"temp_c,omitempty"`
	HumidityPct   string `json:"humidity_pct,omitempty"`
	CO2Ppm        string `json:"co2_ppm,omitempty"`
	OccupancyRate string `json:"occupancy_rate,omitempty"` // 0~1
	HeatingSeason int    `json:"heating_season"`
	Note          string `json:"note"`
}

// Window 有效测量窗口。
type Window struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	MethodologyID string `json:"methodology_id"`
	Label         string `json:"label"`
	Kind          string `json:"kind"` // baseline | post
	PeriodStart   string `json:"period_start"`
	PeriodEnd     string `json:"period_end"`
	HeatingSeason int    `json:"heating_season"`
	HDD           string `json:"hdd,omitempty"`
	OccupancyMin  string `json:"occupancy_min,omitempty"`
	OccupancyMax  string `json:"occupancy_max,omitempty"`
	Status        string `json:"status"` // draft | finalized
	CreatedAt     string `json:"created_at"`
	FinalizedAt   string `json:"finalized_at,omitempty"`
	FinalizeSeq   int64  `json:"finalize_seq,omitempty"` // 终结事件链序号，读数集合冻结锚点
}

// Exclusion 窗口内被剔除的读数。
type Exclusion struct {
	ID         string `json:"id"`
	WindowID   string `json:"window_id"`
	ReadingID  string `json:"reading_id"`
	Reason     string `json:"reason"`
	ExcludedBy string `json:"excluded_by"`
	ExcludedAt string `json:"excluded_at"`
}

// Methodology 计算口径版本（锁定后不可改）。
type Methodology struct {
	ID                     string   `json:"id"`
	ProjectID              string   `json:"project_id"`
	Version                int      `json:"version"`
	ParentID               string   `json:"parent_id,omitempty"`
	Status                 string   `json:"status"` // draft | locked | superseded
	IncludedMeterIDs       []string `json:"included_meter_ids"`
	HeatingNormalization   string   `json:"heating_normalization"`
	OccupancyNormalization string   `json:"occupancy_normalization"`
	FixedOccupancy         string   `json:"fixed_occupancy,omitempty"`
	BoundaryNote           string   `json:"boundary_note"`
	ChangeNote             string   `json:"change_note"`
	CreatedAt              string   `json:"created_at"`
}

// Report 已发布节能报告（不可变）。
type Report struct {
	ID                     string  `json:"id"`
	ProjectID              string  `json:"project_id"`
	BaselineWindowID       string  `json:"baseline_window_id"`
	PostWindowID           string  `json:"post_window_id"`
	MethodologyID          string  `json:"methodology_id"`
	PriorReportID          string  `json:"prior_report_id,omitempty"`
	Status                 string  `json:"status"`
	BaselineRawKWh         string  `json:"baseline_raw_kwh"`
	PostRawKWh             string  `json:"post_raw_kwh"`
	BaselineAdjustedKWh    string  `json:"baseline_adjusted_kwh"`
	PostAdjustedKWh        string  `json:"post_adjusted_kwh"`
	SavingsKWh             string  `json:"savings_kwh"`
	SavingsPct             string  `json:"savings_pct"`
	MethodologyChangeDelta *string `json:"methodology_change_delta_kwh"`
	LedgerSeq              int64   `json:"ledger_seq"`
	LedgerHead             string  `json:"ledger_head"`
	Snapshot               any     `json:"snapshot"`
	PublishedBy            string  `json:"published_by"`
	PublishedAt            string  `json:"published_at"`
}

// Conclusion 第三方结论（只追加，不可改不可删）。
type Conclusion struct {
	ID           string `json:"id"`
	ReportID     string `json:"report_id"`
	Organization string `json:"organization"`
	InspectorRef string `json:"inspector_ref"`
	Verdict      string `json:"verdict"` // pass | conditional | fail
	Finding      string `json:"finding"`
	AttachedBy   string `json:"attached_by"`
	AttachedAt   string `json:"attached_at"`
}
