package store

import "encoding/json"

// 角色(由 HTTP 层从 X-Actor-Role 解析后传入,存储层只记录操作者)。
const (
	RoleFieldTeam  = "field_team" // 现场检测团队:建档、仪表、读数、室内环境
	RoleCalibrator = "calibrator" // 校准人员:更正/剔除错误仪表读数,原读数保留
	RoleVendor     = "vendor"     // 供应商:登记设备更换与宣传值,不得改写第三方结论
	RoleInspector  = "inspector"  // 第三方检测方:窗口审核、口径定义、报告发布
	RoleOwner      = "owner"      // 业主:只读查询
)

// 仪表类型,口径按类型筛选纳入计算的读数。
var MeterKinds = []string{"electricity", "heat", "gas", "water"}

// 测量窗口阶段。
const (
	PhaseBaseline  = "baseline"  // 改造前基线窗口
	PhaseReporting = "reporting" // 改造后报告窗口
)

// 测量窗口状态。只有 validated 的窗口允许用于发布指标。
const (
	WindowOpen      = "open"
	WindowValidated = "validated"
	WindowInvalid   = "invalid"
)

// 读数更正动作。原读数行永不修改,更正以追加记录形式存在。
const (
	CorrectionAdjust  = "adjust"  // 调整数值,corrected_kwh 生效
	CorrectionExclude = "exclude" // 剔除出计算,原因必须记录
)

// 口径状态。被已发布报告引用的口径不得退役。
const (
	CaliberActive  = "active"
	CaliberRetired = "retired"
)

// 报告状态。published 之后整行(含冻结结果)不可再变。
const (
	ReportDraft     = "draft"
	ReportPublished = "published"
)

type Building struct {
	ID          int64   `json:"id"`
	BuildingRef string  `json:"building_ref"`
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	FloorAreaM2 float64 `json:"floor_area_m2"`
	CreatedBy   string  `json:"created_by"`
	CreatedAt   string  `json:"created_at"`
}

// Baseline 记录建筑改造前的使用条件(占用率、采暖度日等),与能耗读数分开保存。
type Baseline struct {
	ID                int64    `json:"id"`
	BuildingID        int64    `json:"building_id"`
	PeriodStart       string   `json:"period_start"`
	PeriodEnd         string   `json:"period_end"`
	OccupancyRatio    *float64 `json:"occupancy_ratio,omitempty"`
	HeatingDegreeDays *float64 `json:"heating_degree_days,omitempty"`
	Note              string   `json:"note"`
	CreatedBy         string   `json:"created_by"`
	CreatedAt         string   `json:"created_at"`
}

type Meter struct {
	ID          int64  `json:"id"`
	BuildingID  int64  `json:"building_id"`
	MeterRef    string `json:"meter_ref"`
	Kind        string `json:"kind"`
	InstalledAt string `json:"installed_at,omitempty"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
}

// Reading 是分时段能耗读数,只允许追加;数值修正通过 Correction 完成。
type Reading struct {
	ID          int64   `json:"id"`
	MeterID     int64   `json:"meter_id"`
	PeriodStart string  `json:"period_start"`
	PeriodEnd   string  `json:"period_end"`
	ReadingKWH  float64 `json:"reading_kwh"` // 原始读数,永不改写
	RecordedBy  string  `json:"recorded_by"`
	CreatedAt   string  `json:"created_at"`
}

// Correction 是校准人员对读数的更正/剔除记录,保留完整审计链。
type Correction struct {
	ID           int64    `json:"id"`
	ReadingID    int64    `json:"reading_id"`
	Action       string   `json:"action"` // adjust | exclude
	CorrectedKWH *float64 `json:"corrected_kwh,omitempty"`
	Reason       string   `json:"reason"`
	CorrectedBy  string   `json:"corrected_by"`
	CreatedAt    string   `json:"created_at"`
}

// EquipmentReplacement 记录设备更换事实;供应商可登记,宣传值另存 VendorClaim。
type EquipmentReplacement struct {
	ID             int64  `json:"id"`
	BuildingID     int64  `json:"building_id"`
	EquipmentKind  string `json:"equipment_kind"`
	RemovedModel   string `json:"removed_model,omitempty"`
	InstalledModel string `json:"installed_model"`
	VendorRef      string `json:"vendor_ref,omitempty"`
	ReplacedAt     string `json:"replaced_at"`
	Note           string `json:"note"`
	CreatedBy      string `json:"created_by"`
	CreatedAt      string `json:"created_at"`
}

// VendorClaim 是供应商宣传值(展会/样本数值),与现场实测读数分开保存,
// 永远不进入节能计算。
type VendorClaim struct {
	ID            int64   `json:"id"`
	ReplacementID int64   `json:"replacement_id"`
	ClaimKind     string  `json:"claim_kind"` // 如 savings_pct / cop / nominal_power_kw
	ClaimValue    float64 `json:"claim_value"`
	Unit          string  `json:"unit"`
	SubmittedBy   string  `json:"submitted_by"`
	CreatedAt     string  `json:"created_at"`
}

// EnvironmentRecord 是分时段室内环境记录,用于解释读数受采暖季与占用率的影响。
type EnvironmentRecord struct {
	ID                int64    `json:"id"`
	BuildingID        int64    `json:"building_id"`
	PeriodStart       string   `json:"period_start"`
	PeriodEnd         string   `json:"period_end"`
	IndoorTempC       *float64 `json:"indoor_temp_c,omitempty"`
	IndoorHumidityPct *float64 `json:"indoor_humidity_pct,omitempty"`
	OccupancyRatio    *float64 `json:"occupancy_ratio,omitempty"`
	HeatingDegreeDays *float64 `json:"heating_degree_days,omitempty"`
	RecordedBy        string   `json:"recorded_by"`
	CreatedAt         string   `json:"created_at"`
}

// MeasurementWindow 是第三方审核过的测量窗口;指标只能在 validated 窗口上发布。
type MeasurementWindow struct {
	ID          int64  `json:"id"`
	BuildingID  int64  `json:"building_id"`
	Phase       string `json:"phase"` // baseline | reporting
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Status      string `json:"status"` // open | validated | invalid
	ReviewedBy  string `json:"reviewed_by,omitempty"`
	ReviewedAt  string `json:"reviewed_at,omitempty"`
	Note        string `json:"note"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
}

// Caliber 是指标口径定义。创建后不可修改;口径变化只能新增版本。
type Caliber struct {
	ID                 int64    `json:"id"`
	Code               string   `json:"code"`
	Version            int      `json:"version"`
	MeterKinds         []string `json:"meter_kinds"`
	NormalizeOccupancy bool     `json:"normalize_occupancy"`
	NormalizeHeating   bool     `json:"normalize_heating"`
	MinCoverageRatio   float64  `json:"min_coverage_ratio"`
	Description        string   `json:"description"`
	Status             string   `json:"status"` // active | retired
	CreatedBy          string   `json:"created_by"`
	CreatedAt          string   `json:"created_at"`
}

// Report 是第三方发布的节能结论。发布后 Result 冻结,任何角色不得修改;
// 更正结论只能新建报告并注明 supersedes_id。
type Report struct {
	ID                int64           `json:"id"`
	BuildingID        int64           `json:"building_id"`
	CaliberID         int64           `json:"caliber_id"`
	BaselineWindowID  int64           `json:"baseline_window_id"`
	ReportingWindowID int64           `json:"reporting_window_id"`
	Status            string          `json:"status"`           // draft | published
	Result            json.RawMessage `json:"result,omitempty"` // 发布时冻结的溯源结果
	SupersedesID      *int64          `json:"supersedes_id,omitempty"`
	CreatedBy         string          `json:"created_by"`
	CreatedAt         string          `json:"created_at"`
	PublishedBy       string          `json:"published_by,omitempty"`
	PublishedAt       string          `json:"published_at,omitempty"`
}
