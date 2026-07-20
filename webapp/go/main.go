package main

import (
	"fmt"
	"net"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
)

func main() {
	e := echo.New()
	e.Debug = false
	e.Logger.SetLevel(log.ERROR)

	e.Use(middleware.Recover())

	e.POST("/initialize", postInitialize)

	e.POST("/api/auth", postAuthentication)
	e.POST("/api/signout", postSignout)
	e.GET("/api/user/me", getMe)
	e.GET("/api/isu", getIsuList)
	e.POST("/api/isu", postIsu)
	e.GET("/api/isu/:jia_isu_uuid", getIsuID)
	e.GET("/api/isu/:jia_isu_uuid/icon", getIsuIcon)
	e.GET("/api/isu/:jia_isu_uuid/graph", getIsuGraph)
	e.GET("/api/condition/:jia_isu_uuid", getIsuConditions)
	e.GET("/api/trend", getTrend)

	e.POST("/api/condition/:jia_isu_uuid", postIsuCondition)

	e.GET("/", getIndex)
	e.GET("/isu/:jia_isu_uuid", getIndex)
	e.GET("/isu/:jia_isu_uuid/condition", getIndex)
	e.GET("/isu/:jia_isu_uuid/graph", getIndex)
	e.GET("/register", getIndex)
	e.Static("/assets", frontendContentsPath+"/assets")

	mySQLConnectionData = NewMySQLConnectionEnv()

	var err error
	db, err = mySQLConnectionData.ConnectDB()
	if err != nil {
		e.Logger.Fatalf("failed to connect db: %v", err)
		return
	}
	// Authentication is cookie based, and several endpoints can issue database
	// queries concurrently.  The default of ten connections made otherwise
	// independent requests wait for a free connection under benchmark load.
	db.SetMaxOpenConns(200)
	db.SetMaxIdleConns(200)
	defer db.Close()
	startConditionWriter()

	postIsuConditionTargetBaseURL = os.Getenv("POST_ISUCONDITION_TARGET_BASE_URL")
	if postIsuConditionTargetBaseURL == "" {
		e.Logger.Fatalf("missing: POST_ISUCONDITION_TARGET_BASE_URL")
		return
	}

	if sock := os.Getenv("SERVER_APP_SOCK"); sock != "" {
		_ = os.Remove(sock)
		ln, err := net.Listen("unix", sock)
		if err != nil {
			e.Logger.Fatalf("listen unix %s: %v", sock, err)
			return
		}
		// nginx (www-data) から繋ぐため世界読み書きにする。
		if err := os.Chmod(sock, 0o666); err != nil {
			e.Logger.Fatalf("chmod unix %s: %v", sock, err)
			return
		}
		e.Listener = ln
		e.Logger.Fatal(e.Start(""))
		return
	}

	serverPort := fmt.Sprintf(":%v", getEnv("SERVER_APP_PORT", "3000"))
	e.Logger.Fatal(e.Start(serverPort))
}
