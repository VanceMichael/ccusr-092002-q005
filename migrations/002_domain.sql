-- 测量档案领域模式(关系模式契约,与 internal/store 的模型一一对应)。
-- 当前 Go 运行时使用内嵌快照存储自举;本文件供需要关系库部署的环境
-- 通过 sqlite3 "$(DATABASE_PATH)" < migrations/002_domain.sql 初始化。

-- 建筑
CREATE TABLE IF NOT EXISTS buildings (
    id           INTEGER PRIMARY KEY,
    building_ref TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    address      TEXT NOT NULL DEFAULT '',
    floor_area_m2 REAL NOT NULL DEFAULT 0,
    created_by   TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

-- 建筑基线(改造前使用条件)
CREATE TABLE IF NOT EXISTS baselines (
    id                  INTEGER PRIMARY KEY,
    building_id         INTEGER NOT NULL REFERENCES buildings(id),
    period_start        TEXT NOT NULL,
    period_end          TEXT NOT NULL,
    occupancy_ratio     REAL,
    heating_degree_days REAL,
    note                TEXT NOT NULL DEFAULT '',
    created_by          TEXT NOT NULL,
    created_at          TEXT NOT NULL
);

-- 仪表
CREATE TABLE IF NOT EXISTS meters (
    id           INTEGER PRIMARY KEY,
    building_id  INTEGER NOT NULL REFERENCES buildings(id),
    meter_ref    TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('electricity','heat','gas','water')),
    installed_at TEXT,
    created_by   TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    UNIQUE (building_id, meter_ref)
);

-- 分时段能耗读数(只追加,原值永不改写)
CREATE TABLE IF NOT EXISTS readings (
    id           INTEGER PRIMARY KEY,
    meter_id     INTEGER NOT NULL REFERENCES meters(id),
    period_start TEXT NOT NULL,
    period_end   TEXT NOT NULL,
    reading_kwh  REAL NOT NULL CHECK (reading_kwh >= 0),
    recorded_by  TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

-- 读数更正链(校准人员;生效值取最新一条)
CREATE TABLE IF NOT EXISTS reading_corrections (
    id            INTEGER PRIMARY KEY,
    reading_id    INTEGER NOT NULL REFERENCES readings(id),
    action        TEXT NOT NULL CHECK (action IN ('adjust','exclude')),
    corrected_kwh REAL,
    reason        TEXT NOT NULL,
    corrected_by  TEXT NOT NULL,
    created_at    TEXT NOT NULL
);

-- 设备更换
CREATE TABLE IF NOT EXISTS equipment_replacements (
    id              INTEGER PRIMARY KEY,
    building_id     INTEGER NOT NULL REFERENCES buildings(id),
    equipment_kind  TEXT NOT NULL,
    removed_model   TEXT,
    installed_model TEXT NOT NULL,
    vendor_ref      TEXT,
    replaced_at     TEXT NOT NULL,
    note            TEXT NOT NULL DEFAULT '',
    created_by      TEXT NOT NULL,
    created_at      TEXT NOT NULL
);

-- 供应商宣传值(与现场实测读数分开保存,永不进入节能计算)
CREATE TABLE IF NOT EXISTS vendor_claims (
    id             INTEGER PRIMARY KEY,
    replacement_id INTEGER NOT NULL REFERENCES equipment_replacements(id),
    claim_kind     TEXT NOT NULL,
    claim_value    REAL NOT NULL,
    unit           TEXT NOT NULL,
    submitted_by   TEXT NOT NULL,
    created_at     TEXT NOT NULL
);

-- 分时段室内环境记录
CREATE TABLE IF NOT EXISTS environment_records (
    id                  INTEGER PRIMARY KEY,
    building_id         INTEGER NOT NULL REFERENCES buildings(id),
    period_start        TEXT NOT NULL,
    period_end          TEXT NOT NULL,
    indoor_temp_c       REAL,
    indoor_humidity_pct REAL,
    occupancy_ratio     REAL,
    heating_degree_days REAL,
    recorded_by         TEXT NOT NULL,
    created_at          TEXT NOT NULL
);

-- 测量窗口(仅 validated 状态可用于发布)
CREATE TABLE IF NOT EXISTS measurement_windows (
    id           INTEGER PRIMARY KEY,
    building_id  INTEGER NOT NULL REFERENCES buildings(id),
    phase        TEXT NOT NULL CHECK (phase IN ('baseline','reporting')),
    period_start TEXT NOT NULL,
    period_end   TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','validated','invalid')),
    reviewed_by  TEXT,
    reviewed_at  TEXT,
    note         TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

-- 口径(创建后不可修改,变化只能新增版本)
CREATE TABLE IF NOT EXISTS calibers (
    id                  INTEGER PRIMARY KEY,
    code                TEXT NOT NULL,
    version             INTEGER NOT NULL,
    meter_kinds         TEXT NOT NULL, -- JSON 数组
    normalize_occupancy INTEGER NOT NULL DEFAULT 0,
    normalize_heating   INTEGER NOT NULL DEFAULT 0,
    min_coverage_ratio  REAL NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','retired')),
    created_by          TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    UNIQUE (code, version)
);

-- 报告(第三方结论,发布后整行冻结)
CREATE TABLE IF NOT EXISTS reports (
    id                  INTEGER PRIMARY KEY,
    building_id         INTEGER NOT NULL REFERENCES buildings(id),
    caliber_id          INTEGER NOT NULL REFERENCES calibers(id),
    baseline_window_id  INTEGER NOT NULL REFERENCES measurement_windows(id),
    reporting_window_id INTEGER NOT NULL REFERENCES measurement_windows(id),
    status              TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published')),
    result_json         TEXT, -- 发布时冻结的溯源结果
    supersedes_id       INTEGER REFERENCES reports(id),
    created_by          TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    published_by        TEXT,
    published_at        TEXT
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES ('002_domain');
