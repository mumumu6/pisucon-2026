package main

import (
	"net/http"
	"os"
	"os/exec"

	"github.com/labstack/echo/v4"
)

// サービスを初期化
func postInitialize(c echo.Context) error {
	var request InitializeRequest
	err := c.Bind(&request)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad request body")
	}

	cmd := exec.Command("../sql/init.sh")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	err = cmd.Run()
	if err != nil {
		c.Logger().Errorf("exec init.sh error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	clearIsuExistenceCache()
	clearIsuOwnerCache()
	clearIsuMetadataCache()
	clearIsuLatestTimestampCache()
	clearIsuIconCache()
	clearGraphCache()

	_, err = db.Exec(
		"INSERT INTO `isu_association_config` (`name`, `url`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `url` = VALUES(`url`)",
		"jia_service_url",
		request.JIAServiceURL,
	)
	if err != nil {
		c.Logger().Errorf("db error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	// 種データから仮想現在時刻とグラフを温める（以降のロジックは POST/GET 側）。
	if err := warmIsuLatestTimestamps(); err != nil {
		c.Logger().Errorf("warm latest timestamps error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	if err := warmGraphCache(); err != nil {
		c.Logger().Errorf("warm graph cache error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, InitializeResponse{
		Language: "go",
	})
}

// POST /api/auth
