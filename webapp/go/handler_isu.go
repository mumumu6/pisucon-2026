package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"

	"github.com/go-sql-driver/mysql"
	"github.com/labstack/echo/v4"
)

// ISUの一覧を取得
func getIsuList(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	if body, ok := getCachedIsuListJSON(jiaUserID); ok {
		return c.JSONBlob(http.StatusOK, body)
	}

	order, haveOrder := getCachedIsuListOrder(jiaUserID)
	responseList := make([]GetIsuListResponse, 0, 16)

	if haveOrder {
		responseList = make([]GetIsuListResponse, 0, len(order))
		for _, jiaIsuUUID := range order {
			entry, ok := getCachedIsuRecord(jiaIsuUUID, jiaUserID)
			if !ok {
				haveOrder = false
				break
			}
			res, err := buildIsuListItem(jiaIsuUUID, entry)
			if err != nil {
				c.Logger().Error(err)
				return c.NoContent(http.StatusInternalServerError)
			}
			responseList = append(responseList, res)
		}
	}

	if !haveOrder {
		isuList := []Isu{}
		err = db.Select(
			&isuList,
			"SELECT `id`, `jia_isu_uuid`, `name`, `character`, `latest_timestamp`, `latest_is_sitting`, `latest_condition`, `latest_message`, `jia_user_id`, `created_at`, `updated_at`"+
				" FROM `isu` WHERE `jia_user_id` = ? ORDER BY `id` DESC",
			jiaUserID)
		if err != nil {
			c.Logger().Errorf("db error: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}

		order = make([]string, 0, len(isuList))
		responseList = make([]GetIsuListResponse, 0, len(isuList))
		for _, isu := range isuList {
			setCachedIsuOwner(isu.JIAIsuUUID, jiaUserID)
			setCachedIsuMetadata(isu.JIAIsuUUID, jiaUserID, isu.ID, isu.Name, isu.Character)
			order = append(order, isu.JIAIsuUUID)
			if _, ok := getCachedIsuLatestCondition(isu.JIAIsuUUID); !ok && isu.LatestTimestamp.Valid {
				setCachedIsuLatestCondition(
					isu.JIAIsuUUID,
					isu.LatestTimestamp.Time.Unix(),
					isu.LatestIsSitting.Bool,
					isu.LatestCondition.String,
					isu.LatestMessage.String,
				)
			}
			entry, _ := getCachedIsuRecord(isu.JIAIsuUUID, jiaUserID)
			res, err := buildIsuListItem(isu.JIAIsuUUID, entry)
			if err != nil {
				c.Logger().Error(err)
				return c.NoContent(http.StatusInternalServerError)
			}
			responseList = append(responseList, res)
		}
		setIsuListOrder(jiaUserID, order)
	}

	body, err := jsonFast.Marshal(responseList)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}
	setCachedIsuListJSON(jiaUserID, body)
	return c.JSONBlob(http.StatusOK, body)
}

func buildIsuListItem(jiaIsuUUID string, entry isuMetadataCacheEntry) (GetIsuListResponse, error) {
	var formattedCondition *GetIsuConditionResponse
	if latest, ok := getCachedIsuLatestCondition(jiaIsuUUID); ok {
		conditionLevel, err := calculateConditionLevel(latest.Condition)
		if err != nil {
			return GetIsuListResponse{}, err
		}
		formattedCondition = &GetIsuConditionResponse{
			JIAIsuUUID:     jiaIsuUUID,
			IsuName:        entry.name,
			Timestamp:      latest.Timestamp,
			IsSitting:      latest.IsSitting,
			Condition:      latest.Condition,
			ConditionLevel: conditionLevel,
			Message:        latest.Message,
		}
	}
	return GetIsuListResponse{
		ID:                 entry.id,
		JIAIsuUUID:         jiaIsuUUID,
		Name:               entry.name,
		Character:          entry.character,
		LatestIsuCondition: formattedCondition,
	}, nil
}

// POST /api/isu
// ISUを登録
func postIsu(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.FormValue("jia_isu_uuid")
	isuName := c.FormValue("isu_name")

	// 既登録なら activate を送らない（JIA は2回目も成功するが target_base_url は1回目のまま）
	if exists, cached := getCachedIsuExistence(jiaIsuUUID); cached {
		if exists {
			return c.String(http.StatusConflict, "duplicated: isu")
		}
	} else {
		var existsInt int
		err = db.Get(&existsInt, "SELECT EXISTS(SELECT 1 FROM `isu` WHERE `jia_isu_uuid` = ?)", jiaIsuUUID)
		if err != nil {
			c.Logger().Errorf("db error: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
		exists := existsInt == 1
		setCachedIsuExistence(jiaIsuUUID, exists)
		if exists {
			return c.String(http.StatusConflict, "duplicated: isu")
		}
	}

	if _, loaded := isuActivateInFlight.LoadOrStore(jiaIsuUUID, struct{}{}); loaded {
		return c.String(http.StatusConflict, "duplicated: isu")
	}
	activateOK := false
	defer func() {
		if !activateOK {
			isuActivateInFlight.Delete(jiaIsuUUID)
		}
	}()

	useDefaultImage := false
	fh, err := c.FormFile("image")
	if err != nil {
		if !errors.Is(err, http.ErrMissingFile) {
			return c.String(http.StatusBadRequest, "bad format: icon")
		}
		useDefaultImage = true
	}

	var image []byte

	if useDefaultImage {
		image = defaultIconImage
	} else {
		file, err := fh.Open()
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
		defer file.Close()

		image, err = ioutil.ReadAll(file)
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
	}

	// JIAへの通信を先に完了させてから ISU 行を INSERT する。
	targetURL := getJIAServiceURL() + "/api/activate"
	body := JIAServiceRequest{postIsuConditionTargetBaseURL, jiaIsuUUID}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	reqJIA, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewBuffer(bodyJSON))
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	reqJIA.Header.Set("Content-Type", "application/json")
	res, err := jiaHTTPClient.Do(reqJIA)
	if err != nil {
		c.Logger().Errorf("failed to request to JIAService: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer res.Body.Close()

	resBody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	if res.StatusCode != http.StatusAccepted {
		c.Logger().Errorf("JIAService returned error: status code %v, message: %v", res.StatusCode, string(resBody))
		return c.String(res.StatusCode, "JIAService returned error")
	}

	var isuFromJIA IsuFromJIA
	err = json.Unmarshal(resBody, &isuFromJIA)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	result, err := db.Exec("INSERT INTO `isu`"+
		"	(`jia_isu_uuid`, `name`, `image`, `jia_user_id`, `character`) VALUES (?, ?, ?, ?, ?)",
		jiaIsuUUID, isuName, image, jiaUserID, isuFromJIA.Character)
	if err != nil {
		mysqlErr, ok := err.(*mysql.MySQLError)

		if ok && mysqlErr.Number == uint16(mysqlErrNumDuplicateEntry) {
			return c.String(http.StatusConflict, "duplicated: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	id, err := result.LastInsertId()
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	isu := Isu{
		ID:         int(id),
		JIAIsuUUID: jiaIsuUUID,
		Name:       isuName,
		Character:  isuFromJIA.Character,
	}

	setCachedIsuExistence(jiaIsuUUID, true)
	setCachedIsuOwner(jiaIsuUUID, jiaUserID)
	setCachedIsuMetadata(jiaIsuUUID, jiaUserID, isu.ID, isuName, isuFromJIA.Character)
	setCachedIsuIcon(jiaIsuUUID, jiaUserID, image)
	prependIsuListOrder(jiaUserID, jiaIsuUUID)
	activateOK = true
	isuActivateInFlight.Delete(jiaIsuUUID)

	return c.JSON(http.StatusCreated, isu)
}

// GET /api/isu/:jia_isu_uuid
// ISUの情報を取得
func getIsuID(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")

	if entry, ok := getCachedIsuRecord(jiaIsuUUID, jiaUserID); ok {
		if len(entry.jsonBody) > 0 {
			return c.JSONBlob(http.StatusOK, entry.jsonBody)
		}
		return c.JSON(http.StatusOK, Isu{
			ID:         entry.id,
			JIAIsuUUID: jiaIsuUUID,
			Name:       entry.name,
			Character:  entry.character,
		})
	}

	var res Isu
	err = db.Get(&res, "SELECT `id`, `jia_isu_uuid`, `name`, `character` FROM `isu`"+
		" WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
		jiaUserID, jiaIsuUUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.String(http.StatusNotFound, "not found: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	setCachedIsuOwner(jiaIsuUUID, jiaUserID)
	setCachedIsuMetadata(jiaIsuUUID, jiaUserID, res.ID, res.Name, res.Character)

	if entry, ok := getCachedIsuRecord(jiaIsuUUID, jiaUserID); ok && len(entry.jsonBody) > 0 {
		return c.JSONBlob(http.StatusOK, entry.jsonBody)
	}
	return c.JSON(http.StatusOK, res)
}

// GET /api/isu/:jia_isu_uuid/icon
// ISUのアイコンを取得
func getIsuIcon(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")
	// 登録後は不変。private（ユーザ間で共有しない）+ max-age で再取得自体を減らす。
	etag := `"` + jiaIsuUUID + `"`
	writeIconHeaders := func() {
		c.Response().Header().Set("Cache-Control", "private, max-age=86400")
		c.Response().Header().Set("ETag", etag)
	}
	ifNoneMatch := c.Request().Header.Get("If-None-Match")

	if image, ok := getCachedIsuIcon(jiaIsuUUID, jiaUserID); ok {
		writeIconHeaders()
		if ifNoneMatch == etag {
			return c.NoContent(http.StatusNotModified)
		}
		return c.Blob(http.StatusOK, "", image)
	}

	var image []byte
	err = db.Get(&image, "SELECT `image` FROM `isu` WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
		jiaUserID, jiaIsuUUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.String(http.StatusNotFound, "not found: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	setCachedIsuOwner(jiaIsuUUID, jiaUserID)
	setCachedIsuIconAsync(jiaIsuUUID, jiaUserID, image)

	writeIconHeaders()
	if ifNoneMatch == etag {
		return c.NoContent(http.StatusNotModified)
	}
	return c.Blob(http.StatusOK, "", image)
}

// GET /api/isu/:jia_isu_uuid/graph
