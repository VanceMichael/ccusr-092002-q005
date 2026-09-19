package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/domain"
	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

// Server 是测量档案的 HTTP 服务。所有写操作要求 X-Actor-Id 与
// X-Actor-Role 头;角色决定可执行的操作(见 store 包角色常量)。
type Server struct {
	st  *store.Store
	now func() time.Time
}

func NewServer(st *store.Store) *Server {
	return &Server{st: st, now: time.Now}
}

// SetClock 替换时间源,仅供测试。
func (s *Server) SetClock(now func() time.Time) { s.now = now }

// ---------------------------------------------------------------------------
// 操作者与角色
// ---------------------------------------------------------------------------

type actor struct {
	id   string
	role string
}

var knownRoles = []string{
	store.RoleFieldTeam, store.RoleCalibrator, store.RoleVendor,
	store.RoleInspector, store.RoleOwner,
}

func actorFrom(r *http.Request) actor {
	return actor{id: r.Header.Get("X-Actor-Id"), role: r.Header.Get("X-Actor-Role")}
}

// require 校验操作者身份与角色。供应商访问治理类端点(窗口、口径、报告、
// 更正)时返回专门提示:供应商不得改写第三方结论。
func (s *Server) require(w http.ResponseWriter, r *http.Request, roles ...string) (actor, bool) {
	a := actorFrom(r)
	if a.id == "" {
		fail(w, http.StatusUnauthorized, "ACTOR_REQUIRED", "缺少 X-Actor-Id 头,档案操作必须记录操作者")
		return a, false
	}
	known := false
	for _, role := range knownRoles {
		if a.role == role {
			known = true
			break
		}
	}
	if !known {
		fail(w, http.StatusForbidden, "UNKNOWN_ROLE",
			"X-Actor-Role 必须是 field_team/calibrator/vendor/inspector/owner 之一")
		return a, false
	}
	for _, role := range roles {
		if a.role == role {
			return a, true
		}
	}
	if a.role == store.RoleVendor {
		fail(w, http.StatusForbidden, "VENDOR_CANNOT_ALTER_CONCLUSIONS",
			"供应商只能登记设备更换与宣传值,不得改写第三方结论")
		return a, false
	}
	fail(w, http.StatusForbidden, "FORBIDDEN_ROLE",
		fmt.Sprintf("角色 %s 无权执行此操作", a.role))
	return a, false
}

// ---------------------------------------------------------------------------
// 请求/响应辅助
// ---------------------------------------------------------------------------

func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decode 解析请求体,拒绝未知字段以保持契约严格。
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "BAD_JSON", "请求体不是合法 JSON 或含未知字段: "+err.Error())
		return false
	}
	return true
}

// storeError 把存储层错误映射为 HTTP 响应;已处理时返回 true。
func storeError(w http.ResponseWriter, err error) bool {
	var ve *store.ValidationError
	var ce *store.ConflictError
	var comp *domain.ComputationError
	switch {
	case err == nil:
		return false
	case errors.As(err, &ve):
		fail(w, http.StatusBadRequest, "VALIDATION", ve.Message)
	case errors.As(err, &ce):
		fail(w, http.StatusConflict, "CONFLICT", ce.Message)
	case errors.As(err, &comp):
		fail(w, http.StatusConflict, "COMPUTATION", comp.Message)
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "NOT_FOUND", "记录不存在")
	default:
		fail(w, http.StatusInternalServerError, "INTERNAL", "内部错误: "+err.Error())
	}
	return true
}

func pathInt64(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil
}

// flexFloat 兼容 JSON 数值与字符串形式的数值(外部交换格式中 kwh 常以
// 字符串出现,见 contracts/entities.json)。
type flexFloat struct {
	Value float64
	Set   bool
}

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	var num float64
	if err := json.Unmarshal(b, &num); err == nil {
		f.Value, f.Set = num, true
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return errors.New("必须是数值或数值字符串")
	}
	v, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return errors.New("必须是数值或数值字符串")
	}
	f.Value, f.Set = v, true
	return nil
}

func (f flexFloat) ptr() *float64 {
	if !f.Set {
		return nil
	}
	v := f.Value
	return &v
}
