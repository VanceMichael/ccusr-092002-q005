package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/archive"
)

type ctxKey int

const actorKey ctxKey = iota

// authMiddleware 校验 Authorization: Bearer <token>。
// 供应商不持有任何令牌——令牌表只有 field/calibration/third_party 三类。
func authMiddleware(svc *archive.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			writeError(w, http.StatusUnauthorized, "缺少 Bearer 令牌；供应商不具有系统访问令牌")
			return
		}
		token := strings.TrimSpace(header[len(prefix):])
		actor, ok := svc.Authenticate(token)
		if !ok {
			writeError(w, http.StatusUnauthorized, "令牌无效或已停用")
			return
		}
		ctx := context.WithValue(r.Context(), actorKey, actor)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func actorFrom(r *http.Request) archive.Actor {
	a, _ := r.Context().Value(actorKey).(archive.Actor)
	return a
}

// roleGuard 限定 HTTP 方法与角色的粗粒度边界；具体操作的角色
// （field/calibration/third_party）由领域服务逐个强制。
// 业主(owner)只有只读权限；供应商没有任何令牌。
func roleGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := actorFrom(r)
		if actor.Role == archive.RoleOwner && r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusForbidden, "业主令牌仅可查询报告，不能写入或发布任何记录")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "请求体必须是 JSON："+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
