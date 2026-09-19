package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/vancemichael/092002-retrofit-energy-proof/internal/httpapi"
	"github.com/vancemichael/092002-retrofit-energy-proof/internal/store"
)

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "初始化数据文件后退出(等价于 make migrate)")
	flag.Parse()

	path := os.Getenv("DATABASE_PATH")
	if path == "" {
		path = "data/app.json"
	}
	st, err := store.Open(path)
	if err != nil {
		log.Fatalf("打开档案存储失败: %v", err)
	}
	if *migrateOnly {
		fmt.Printf("数据文件已就绪: %s(schema_version=%d)\n", path, store.SchemaVersion)
		return
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("测量档案服务监听 :%s,数据文件 %s", port, path)
	log.Fatal(http.ListenAndServe(":"+port, httpapi.NewHandler(st)))
}
