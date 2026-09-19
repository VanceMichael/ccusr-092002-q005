package httpapi

import (
	"errors"
	"net/http"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/archive"
)

// Router 组装全部路由。health 无需鉴权，其余均需 Bearer 令牌，
// 具体角色由领域服务按操作强制。
func Router(svc *archive.Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	api := http.NewServeMux()

	// 档案与设备
	api.HandleFunc("POST /v1/buildings", func(w http.ResponseWriter, r *http.Request) {
		var in archive.BuildingInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.RegisterBuilding(actorFrom(r), in)
		respond(w, v, err)
	})
	api.HandleFunc("POST /v1/projects", func(w http.ResponseWriter, r *http.Request) {
		var in archive.ProjectInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.CreateProject(actorFrom(r), in)
		respond(w, v, err)
	})
	api.HandleFunc("POST /v1/equipment", func(w http.ResponseWriter, r *http.Request) {
		var in archive.EquipmentInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.ReplaceEquipment(actorFrom(r), in)
		respond(w, v, err)
	})
	api.HandleFunc("POST /v1/meters", func(w http.ResponseWriter, r *http.Request) {
		var in archive.MeterInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.RegisterMeter(actorFrom(r), in)
		respond(w, v, err)
	})

	// 读数与校准
	api.HandleFunc("POST /v1/readings", func(w http.ResponseWriter, r *http.Request) {
		var in archive.ReadingInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.RecordReading(actorFrom(r), in)
		respond(w, v, err)
	})
	api.HandleFunc("GET /v1/readings/{id}", func(w http.ResponseWriter, r *http.Request) {
		rdg, cor, ok := svc.GetReading(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusNotFound, "读数不存在")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"reading": rdg, "correction": cor})
	})
	api.HandleFunc("POST /v1/readings/{id}/corrections", func(w http.ResponseWriter, r *http.Request) {
		var in archive.CorrectionInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.CorrectReading(actorFrom(r), r.PathValue("id"), in)
		respond(w, v, err)
	})

	// 室内环境
	api.HandleFunc("POST /v1/indoor", func(w http.ResponseWriter, r *http.Request) {
		var in archive.IndoorInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.RecordIndoor(actorFrom(r), in)
		respond(w, v, err)
	})

	// 测量窗口与剔除
	api.HandleFunc("POST /v1/windows", func(w http.ResponseWriter, r *http.Request) {
		var in archive.WindowInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.OpenWindow(actorFrom(r), in)
		respond(w, v, err)
	})
	api.HandleFunc("GET /v1/windows/{id}", func(w http.ResponseWriter, r *http.Request) {
		win, excs, ok := svc.GetWindow(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusNotFound, "测量窗口不存在")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"window": win, "exclusions": excs})
	})
	api.HandleFunc("POST /v1/windows/{id}/exclusions", func(w http.ResponseWriter, r *http.Request) {
		var in archive.ExclusionInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.AddExclusion(actorFrom(r), r.PathValue("id"), in)
		respond(w, v, err)
	})
	api.HandleFunc("POST /v1/windows/{id}/finalize", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.FinalizeWindow(actorFrom(r), r.PathValue("id"))
		respond(w, v, err)
	})

	// 口径
	api.HandleFunc("POST /v1/methodologies", func(w http.ResponseWriter, r *http.Request) {
		var in archive.MethodologyInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.CreateMethodology(actorFrom(r), in)
		respond(w, v, err)
	})
	api.HandleFunc("POST /v1/methodologies/{id}/lock", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.LockMethodology(actorFrom(r), r.PathValue("id"))
		respond(w, v, err)
	})

	// 报告
	api.HandleFunc("POST /v1/reports", func(w http.ResponseWriter, r *http.Request) {
		var in archive.ReportInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.PublishReport(actorFrom(r), in)
		respond(w, v, err)
	})
	api.HandleFunc("GET /v1/reports", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"reports": svc.ListReports(r.URL.Query().Get("project_id"))})
	})
	api.HandleFunc("GET /v1/reports/{id}", func(w http.ResponseWriter, r *http.Request) {
		rpt, cons, err := svc.GetReport(r.PathValue("id"))
		if err != nil {
			serveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"report": rpt, "third_party_conclusions": cons})
	})
	api.HandleFunc("POST /v1/reports/{id}/retract", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reason string `json:"reason"`
		}
		if !decode(w, r, &body) {
			return
		}
		v, err := svc.RetractReport(actorFrom(r), r.PathValue("id"), body.Reason)
		respond(w, v, err)
	})
	api.HandleFunc("POST /v1/reports/{id}/conclusions", func(w http.ResponseWriter, r *http.Request) {
		var in archive.ConclusionInput
		if !decode(w, r, &in) {
			return
		}
		v, err := svc.AttachConclusion(actorFrom(r), r.PathValue("id"), in)
		respond(w, v, err)
	})

	// 审计：链校验与事件浏览
	api.HandleFunc("GET /v1/audit/verify", func(w http.ResponseWriter, _ *http.Request) {
		if err := svc.VerifyChain(); err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"intact": false, "error": err.Error()})
			return
		}
		seq, head := svc.ChainHead()
		writeJSON(w, http.StatusOK, map[string]any{"intact": true, "head_seq": seq, "head_hash": head})
	})
	api.HandleFunc("GET /v1/audit/chain", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"events": svc.ChainEvents()})
	})

	mux.Handle("/v1/", authMiddleware(svc, roleGuard(api)))
	return mux
}

// respond 处理“(值, error)”形式的服务返回。
func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		serveError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func serveError(w http.ResponseWriter, err error) {
	var gate *archive.GateError
	if errors.As(err, &gate) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":    "指标发布条件不满足",
			"blockers": gate.Blockers,
		})
		return
	}
	var rule *archive.RuleError
	if errors.As(err, &rule) {
		writeError(w, rule.Status, rule.Reason)
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
