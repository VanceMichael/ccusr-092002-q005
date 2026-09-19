package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

// NewHandler 构建完整路由。健康检查不要求操作者头,其余接口一律要求
// X-Actor-Id 与 X-Actor-Role。
func NewHandler(st *store.Store) http.Handler {
	s := NewServer(st)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]string{"status": "ok"})
	})

	// 建筑与基线
	mux.HandleFunc("POST /buildings", s.createBuilding)
	mux.HandleFunc("GET /buildings", s.listBuildings)
	mux.HandleFunc("GET /buildings/{ref}", s.getBuilding)
	mux.HandleFunc("POST /buildings/{ref}/baselines", s.createBaseline)
	mux.HandleFunc("GET /buildings/{ref}/baselines", s.listBaselines)

	// 仪表与分时段读数
	mux.HandleFunc("POST /buildings/{ref}/meters", s.createMeter)
	mux.HandleFunc("GET /buildings/{ref}/meters", s.listMeters)
	mux.HandleFunc("POST /buildings/{ref}/readings", s.createReading)
	mux.HandleFunc("GET /buildings/{ref}/readings", s.listReadings)

	// 读数更正(校准人员,原读数保留)
	mux.HandleFunc("POST /readings/{id}/corrections", s.addCorrection)
	mux.HandleFunc("GET /readings/{id}/corrections", s.listCorrections)

	// 设备更换与供应商宣传值(与实测读数分开保存)
	mux.HandleFunc("POST /buildings/{ref}/equipment-replacements", s.createReplacement)
	mux.HandleFunc("GET /buildings/{ref}/equipment-replacements", s.listReplacements)
	mux.HandleFunc("POST /equipment-replacements/{id}/claims", s.addClaim)
	mux.HandleFunc("GET /equipment-replacements/{id}/claims", s.listClaims)

	// 室内环境记录
	mux.HandleFunc("POST /buildings/{ref}/environment-records", s.createEnvRecord)
	mux.HandleFunc("GET /buildings/{ref}/environment-records", s.listEnvRecords)

	// 测量窗口(第三方审核)
	mux.HandleFunc("POST /buildings/{ref}/windows", s.createWindow)
	mux.HandleFunc("GET /buildings/{ref}/windows", s.listWindows)
	mux.HandleFunc("POST /windows/{id}/status", s.setWindowStatus)

	// 口径(创建后不可改,变化只能新增版本)
	mux.HandleFunc("POST /calibers", s.createCaliber)
	mux.HandleFunc("GET /calibers", s.listCalibers)
	mux.HandleFunc("POST /calibers/{id}/retire", s.retireCaliber)

	// 报告(第三方结论,发布后冻结)
	mux.HandleFunc("POST /buildings/{ref}/reports", s.createReport)
	mux.HandleFunc("GET /buildings/{ref}/reports", s.listReports)
	mux.HandleFunc("GET /buildings/{ref}/reports/{id}", s.getReport)
	mux.HandleFunc("GET /buildings/{ref}/reports/{id}/caliber-comparison", s.caliberComparison)
	mux.HandleFunc("POST /reports/{id}/publish", s.publishReport)

	return mux
}
