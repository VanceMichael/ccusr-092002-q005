-- 既有建筑节能改造测量档案 —— 规范关系模式（数据约定）
--
-- 本文件是领域数据的规范契约（canonical schema），描述各实体、外键与不可变约束。
-- 运行时服务（internal/store）以仅追加的哈希链事件日志落盘（ledger_events），
-- 其事件类型与本表一一对应；对 meter_readings / reading_corrections /
-- third_party_conclusions / reports 的 UPDATE、DELETE 在模式层即被拒绝：
-- 原读数永远保留，更正只追加，第三方结论与已发布报告不可改写。

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT OR IGNORE INTO schema_migrations(version) VALUES ('001_bootstrap');

-- 提交方角色：现场检测团队(field)、校准人员(calibration)、第三方机构(third_party)、业主(owner)。
-- 供应商(supplier)不是系统角色，不能登录或改写任何记录，只能作为设备宣传值的署名出现。
CREATE TABLE IF NOT EXISTS actors (
    id         TEXT PRIMARY KEY,
    role       TEXT NOT NULL CHECK (role IN ('field','calibration','third_party','owner')),
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS buildings (
    id            TEXT PRIMARY KEY,            -- BLD-n
    building_ref  TEXT NOT NULL UNIQUE,        -- 业主侧建筑编号（提交方识别符）
    name          TEXT NOT NULL,
    city          TEXT NOT NULL DEFAULT '高碑店',
    building_type TEXT NOT NULL,               -- 住宅/公共建筑 等
    floor_area_m2 TEXT NOT NULL,               -- 十进制字符串
    created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
    id          TEXT PRIMARY KEY,              -- PRJ-n
    building_id TEXT NOT NULL REFERENCES buildings(id),
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('new_build','retrofit')),
    scope       TEXT NOT NULL DEFAULT '',      -- 改造/建设范围说明
    work_start  TEXT,                          -- ISO 8601 带偏移
    work_end    TEXT,
    created_at  TEXT NOT NULL
);

-- 设备档案：实测参数与供应商宣传值分栏保存，宣传值不参与任何节能计算。
CREATE TABLE IF NOT EXISTS equipment_replacements (
    id                  TEXT PRIMARY KEY,      -- EQP-n
    project_id          TEXT NOT NULL REFERENCES projects(id),
    category            TEXT NOT NULL,         -- 门窗/外墙保温/热泵/温控 等
    old_description     TEXT NOT NULL DEFAULT '',
    new_description     TEXT NOT NULL DEFAULT '',
    supplier_name       TEXT NOT NULL DEFAULT '',
    supplier_model      TEXT NOT NULL DEFAULT '',
    rated_value         TEXT,                  -- 铭牌/实测参数（口径明确时）
    rated_unit          TEXT NOT NULL DEFAULT '',
    -- 展会/产品手册宣传数值，独立列、显著标记，仅供参考
    marketing_claim     TEXT,
    marketing_claim_unit TEXT NOT NULL DEFAULT '',
    claim_source        TEXT NOT NULL DEFAULT '', -- 来源：展会/手册/网页
    replaced_on         TEXT NOT NULL,
    created_at          TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS meters (
    id           TEXT PRIMARY KEY,             -- MTR-n
    meter_ref    TEXT NOT NULL UNIQUE,         -- 仪表编号（提交方识别符）
    building_id  TEXT NOT NULL REFERENCES buildings(id),
    meter_type   TEXT NOT NULL,                -- heat/electric/gas/water
    unit         TEXT NOT NULL DEFAULT 'kWh',
    location     TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active','retired')),
    installed_on TEXT NOT NULL
);

-- 不可变现场读数。value 为原始读数字符串，永不更新、永不删除。
CREATE TABLE IF NOT EXISTS meter_readings (
    id                 TEXT PRIMARY KEY,       -- RDG-n
    meter_id           TEXT NOT NULL REFERENCES meters(id),
    period_start       TEXT NOT NULL,
    period_end         TEXT NOT NULL,
    original_value     TEXT NOT NULL,          -- 原读数，保留终身
    unit               TEXT NOT NULL DEFAULT 'kWh',
    quality            TEXT NOT NULL DEFAULT 'good'
                       CHECK (quality IN ('good','suspect')),
    source_ref         TEXT NOT NULL DEFAULT '',
    recorded_by        TEXT NOT NULL,
    recorded_at        TEXT NOT NULL,
    record_seq         INTEGER NOT NULL UNIQUE,-- 哈希链序号，窗口冻结锚点
    CHECK (period_end > period_start)
);

-- 校准更正：只追加。读数是否被更正由本表是否存在对应行决定；
-- 计算使用 corrected_value，报告同时展示 original_value 与更正原因。
CREATE TABLE IF NOT EXISTS reading_corrections (
    id              TEXT PRIMARY KEY,          -- COR-n
    reading_id      TEXT NOT NULL UNIQUE
                    REFERENCES meter_readings(id),
    original_value  TEXT NOT NULL,             -- 冗余固化原读数
    corrected_value TEXT NOT NULL,
    reason          TEXT NOT NULL,             -- 故障/倍率错误/安装错误...
    calibration_ref TEXT NOT NULL DEFAULT '',  -- 校准证书编号
    corrected_by    TEXT NOT NULL,
    corrected_at    TEXT NOT NULL
);

-- 室内环境与占用率：分时段解释能耗的伴随记录（采暖季标志、占用率）。
CREATE TABLE IF NOT EXISTS indoor_environment_records (
    id            TEXT PRIMARY KEY,            -- IND-n
    building_id   TEXT NOT NULL REFERENCES buildings(id),
    ts            TEXT NOT NULL,
    temp_c        TEXT,
    humidity_pct  TEXT,
    co2_ppm       TEXT,
    occupancy_rate TEXT CHECK (occupancy_rate IS NULL OR
                      (CAST(occupancy_rate AS REAL) >= 0 AND
                       CAST(occupancy_rate AS REAL) <= 1)),
    heating_season INTEGER NOT NULL CHECK (heating_season IN (0,1)),
    note          TEXT NOT NULL DEFAULT ''
);

-- 测量窗口：只有 finalized 窗口且绑定锁定口径(methodology_version)才可发布指标。
CREATE TABLE IF NOT EXISTS measurement_windows (
    id                TEXT PRIMARY KEY,        -- WIN-n
    project_id        TEXT NOT NULL REFERENCES projects(id),
    methodology_id    TEXT NOT NULL,           -- 绑定的口径版本
    label             TEXT NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('baseline','post')),
    period_start      TEXT NOT NULL,
    period_end        TEXT NOT NULL,
    heating_season    INTEGER NOT NULL CHECK (heating_season IN (0,1)),
    hdd               TEXT,                    -- 该窗口采暖度日（口径需度日修正时使用）
    occupancy_min     TEXT,                    -- 占用率有效区间
    occupancy_max     TEXT,
    status            TEXT NOT NULL DEFAULT 'draft'
                      CHECK (status IN ('draft','finalized')),
    created_at        TEXT NOT NULL,
    finalized_at      TEXT,
    finalize_seq      INTEGER,                 -- 终结事件链序号，读数集合冻结锚点
    CHECK (period_end > period_start)
);

CREATE TABLE IF NOT EXISTS window_exclusions (
    id           TEXT PRIMARY KEY,             -- EXC-n
    window_id    TEXT NOT NULL REFERENCES measurement_windows(id),
    reading_id   TEXT NOT NULL REFERENCES meter_readings(id),
    reason       TEXT NOT NULL,
    excluded_by  TEXT NOT NULL,
    excluded_at  TEXT NOT NULL,
    UNIQUE (window_id, reading_id)
);

-- 计算口径（版本化、锁定后不可改）。口径变化产生新版本，差额在报告中单列。
CREATE TABLE IF NOT EXISTS methodology_versions (
    id                    TEXT PRIMARY KEY,     -- MET-n
    project_id            TEXT NOT NULL REFERENCES projects(id),
    version               INTEGER NOT NULL,
    parent_id             TEXT REFERENCES methodology_versions(id),
    status                TEXT NOT NULL CHECK (status IN ('draft','locked','superseded')),
    included_meter_ids    TEXT NOT NULL,        -- JSON 数组字符串
    heating_normalization TEXT NOT NULL DEFAULT 'none'
                          CHECK (heating_normalization IN ('none','degree_day')),
    occupancy_normalization TEXT NOT NULL DEFAULT 'none'
                          CHECK (occupancy_normalization IN ('none','fixed','actual')),
    fixed_occupancy       TEXT,                 -- occupancy_normalization='fixed' 时使用
    boundary_note         TEXT NOT NULL DEFAULT '',
    change_note           TEXT NOT NULL DEFAULT '',
    created_at            TEXT NOT NULL,
    UNIQUE (project_id, version)
);

-- 已发布报告：不可变。每次发布固化窗口、读数、剔除、口径与分项结果的快照。
CREATE TABLE IF NOT EXISTS reports (
    id                  TEXT PRIMARY KEY,       -- RPT-n
    project_id          TEXT NOT NULL REFERENCES projects(id),
    baseline_window_id  TEXT NOT NULL REFERENCES measurement_windows(id),
    post_window_id      TEXT NOT NULL REFERENCES measurement_windows(id),
    methodology_id      TEXT NOT NULL REFERENCES methodology_versions(id),
    prior_report_id     TEXT REFERENCES reports(id),
    status              TEXT NOT NULL DEFAULT 'published'
                        CHECK (status IN ('published','retracted')),
    -- 结果分项（口径变化差额单列）
    baseline_raw_kwh        TEXT NOT NULL,
    post_raw_kwh            TEXT NOT NULL,
    baseline_adjusted_kwh   TEXT NOT NULL,
    post_adjusted_kwh       TEXT NOT NULL,
    savings_kwh             TEXT NOT NULL,
    savings_pct             TEXT NOT NULL,
    methodology_change_delta_kwh TEXT,           -- 相较 prior_report_id 的口径差额
    ledger_seq          INTEGER NOT NULL,       -- 发布时哈希链锚点
    ledger_head         TEXT NOT NULL,
    snapshot            TEXT NOT NULL,          -- 完整 JSON 快照
    published_by        TEXT NOT NULL,
    published_at        TEXT NOT NULL
);

-- 报告撤回：只追加撤回记录，报告编号、快照与结论均保留。
CREATE TABLE IF NOT EXISTS report_retractions (
    id           TEXT PRIMARY KEY,
    report_id    TEXT NOT NULL REFERENCES reports(id),
    reason       TEXT NOT NULL,
    retracted_by TEXT NOT NULL,
    retracted_at TEXT NOT NULL
);

-- 第三方结论：仅第三方角色可追加，任何角色（含供应商）不可改、不可删。
CREATE TABLE IF NOT EXISTS third_party_conclusions (
    id              TEXT PRIMARY KEY,           -- CON-n
    report_id       TEXT NOT NULL REFERENCES reports(id),
    organization    TEXT NOT NULL,
    inspector_ref   TEXT NOT NULL DEFAULT '',
    verdict         TEXT NOT NULL,              -- pass/conditional/fail
    finding         TEXT NOT NULL,
    attached_by     TEXT NOT NULL,
    attached_at     TEXT NOT NULL
);

-- 运行时仅追加事件日志（哈希链）。prev_hash 指向上一条记录哈希。
CREATE TABLE IF NOT EXISTS ledger_events (
    seq        INTEGER PRIMARY KEY,
    event_type TEXT NOT NULL,
    actor      TEXT NOT NULL,
    payload    TEXT NOT NULL,
    prev_hash  TEXT NOT NULL,
    hash       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

-- 不可变护栏（运行时日志层同样强制，这里声明规范意图）。
CREATE TRIGGER IF NOT EXISTS trg_readings_no_update
BEFORE UPDATE ON meter_readings
BEGIN SELECT RAISE(ABORT, 'meter_readings 不可修改：请追加 reading_corrections'); END;

CREATE TRIGGER IF NOT EXISTS trg_readings_no_delete
BEFORE DELETE ON meter_readings
BEGIN SELECT RAISE(ABORT, 'meter_readings 不可删除'); END;

CREATE TRIGGER IF NOT EXISTS trg_corrections_no_update
BEFORE UPDATE ON reading_corrections
BEGIN SELECT RAISE(ABORT, 'reading_corrections 不可修改'); END;

CREATE TRIGGER IF NOT EXISTS trg_conclusions_no_update
BEFORE UPDATE ON third_party_conclusions
BEGIN SELECT RAISE(ABORT, '第三方结论不可修改，供应商及任何角色均不得改写'); END;

CREATE TRIGGER IF NOT EXISTS trg_conclusions_no_delete
BEFORE DELETE ON third_party_conclusions
BEGIN SELECT RAISE(ABORT, '第三方结论不可删除'); END;

CREATE TRIGGER IF NOT EXISTS trg_reports_no_update
BEFORE UPDATE ON reports
BEGIN SELECT RAISE(ABORT, '已发布报告不可修改：口径或数据变化请发布新版本报告'); END;
