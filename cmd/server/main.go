package main

import (
	"log"
	"net/http"
	"os"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/archive"
	"github.com/vancemichael/092002-retrofit-energy-proof/internal/httpapi"
	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "data/app.log"
	}

	ledger, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("打开测量档案失败：%v", err)
	}

	// 角色令牌。供应商没有令牌，也无法通过任何环境变量获得访问权。
	actors := map[string]archive.Actor{
		envOr("FIELD_TOKEN", "dev-field-token"):             {ID: "actor-field", Role: archive.RoleField},
		envOr("CALIBRATION_TOKEN", "dev-calibration-token"): {ID: "actor-calibration", Role: archive.RoleCalibration},
		envOr("THIRD_PARTY_TOKEN", "dev-third-party-token"): {ID: "actor-third-party", Role: archive.RoleThirdParty},
		envOr("OWNER_TOKEN", "dev-owner-token"):             {ID: "actor-owner", Role: archive.RoleOwner},
	}
	if os.Getenv("FIELD_TOKEN") == "" {
		log.Print("警告：未设置 FIELD_TOKEN，使用开发用默认令牌；生产环境必须通过环境变量注入")
	}

	svc, err := archive.NewService(ledger, actors)
	if err != nil {
		log.Fatalf("加载测量档案失败：%v", err)
	}

	log.Printf("测量档案服务已启动，数据文件 %s，监听 :%s", dbPath, port)
	log.Fatal(http.ListenAndServe(":"+port, httpapi.Router(svc)))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
