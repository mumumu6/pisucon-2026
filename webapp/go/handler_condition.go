package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
)

// ISUのコンディションを取得
func getIsuConditions(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")
	if jiaIsuUUID == "" {
		return c.String(http.StatusBadRequest, "missing: jia_isu_uuid")
	}

	endTimeInt64, err := strconv.ParseInt(c.QueryParam("end_time"), 10, 64)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad format: end_time")
	}
	endTime := time.Unix(endTimeInt64, 0)
	conditionLevelCSV := c.QueryParam("condition_level")
	if conditionLevelCSV == "" {
		return c.String(http.StatusBadRequest, "missing: condition_level")
	}
	conditionLevel := map[string]interface{}{}
	for _, level := range strings.Split(conditionLevelCSV, ",") {
		conditionLevel[level] = struct{}{}
	}

	startTimeStr := c.QueryParam("start_time")
	var startTime time.Time
	if startTimeStr != "" {
		startTimeInt64, err := strconv.ParseInt(startTimeStr, 10, 64)
		if err != nil {
			return c.String(http.StatusBadRequest, "bad format: start_time")
		}
		startTime = time.Unix(startTimeInt64, 0)
	}

	isuName, cached := getCachedIsuMetadata(jiaIsuUUID, jiaUserID)
	if !cached {
		err = db.Get(&isuName,
			"SELECT name FROM `isu` WHERE `jia_isu_uuid` = ? AND `jia_user_id` = ?",
			jiaIsuUUID, jiaUserID,
		)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return c.String(http.StatusNotFound, "not found: isu")
			}

			c.Logger().Errorf("db error: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
		setCachedIsuOwner(jiaIsuUUID, jiaUserID)
		setCachedIsuMetadata(jiaIsuUUID, jiaUserID, isuName)
	}

	conditionsResponse, err := getIsuConditionsFromDB(db, jiaIsuUUID, endTime, conditionLevel, startTime, conditionLimit, isuName)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	body, err := jsonFast.Marshal(conditionsResponse)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}
	return c.JSONBlob(http.StatusOK, body)
}

// ISUのコンディションをDBから取得
func getIsuConditionsFromDB(db *sqlx.DB, jiaIsuUUID string, endTime time.Time, conditionLevel map[string]interface{}, startTime time.Time,
	limit int, isuName string) ([]GetIsuConditionResponse, error) {
	allowedConditions, levelByCondition := conditionStringsForLevels(conditionLevel)
	if len(allowedConditions) == 0 {
		return []GetIsuConditionResponse{}, nil
	}

	conditions := make([]isuConditionListRow, 0, limit)
	args := []interface{}{jiaIsuUUID, endTime}
	query := "SELECT `timestamp`, `is_sitting`, `condition`, `message` FROM `isu_condition` WHERE `jia_isu_uuid` = ?" +
		"	AND `timestamp` < ?"
	if !startTime.IsZero() {
		query += "	AND ? <= `timestamp`"
		args = append(args, startTime)
	}

	if len(allowedConditions) < 8 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(allowedConditions)), ",")
		query += "	AND `condition` IN (" + placeholders + ")"
		for _, condition := range allowedConditions {
			args = append(args, condition)
		}
	}
	query += "	ORDER BY `timestamp` DESC LIMIT ?"
	args = append(args, limit)

	err := db.Select(&conditions, query, args...)
	if err != nil {
		return nil, fmt.Errorf("db error: %v", err)
	}

	conditionsResponse := make([]GetIsuConditionResponse, 0, len(conditions))
	for _, row := range conditions {
		cLevel, ok := levelByCondition[row.Condition]
		if !ok {
			var err error
			cLevel, err = calculateConditionLevel(row.Condition)
			if err != nil {
				continue
			}
		}

		conditionsResponse = append(conditionsResponse, GetIsuConditionResponse{
			JIAIsuUUID:     jiaIsuUUID,
			IsuName:        isuName,
			Timestamp:      row.Timestamp.Unix(),
			IsSitting:      row.IsSitting,
			Condition:      row.Condition,
			ConditionLevel: cLevel,
			Message:        row.Message,
		})
	}

	return conditionsResponse, nil
}

func conditionStringsForLevels(conditionLevel map[string]interface{}) ([]string, map[string]string) {
	conditions := make([]string, 0, 8)
	levelByCondition := make(map[string]string, 8)
	for _, isDirty := range []bool{false, true} {
		for _, isOverweight := range []bool{false, true} {
			for _, isBroken := range []bool{false, true} {
				condition := fmt.Sprintf(
					"is_dirty=%t,is_overweight=%t,is_broken=%t",
					isDirty, isOverweight, isBroken,
				)
				level, _ := calculateConditionLevel(condition)
				if _, ok := conditionLevel[level]; ok {
					conditions = append(conditions, condition)
					levelByCondition[condition] = level
				}
			}
		}
	}
	return conditions, levelByCondition
}

// ISUのコンディションの文字列からコンディションレベルを計算
func calculateConditionLevel(condition string) (string, error) {
	var conditionLevel string

	warnCount := strings.Count(condition, "=true")
	switch warnCount {
	case 0:
		conditionLevel = conditionLevelInfo
	case 1, 2:
		conditionLevel = conditionLevelWarning
	case 3:
		conditionLevel = conditionLevelCritical
	default:
		return "", fmt.Errorf("unexpected warn count")
	}

	return conditionLevel, nil
}

// GET /api/trend

func startConditionWriter() {
	conditionWriteQueues = make([]chan conditionWriteRequest, conditionWriterCount)
	for i := range conditionWriteQueues {
		conditionWriteQueues[i] = make(chan conditionWriteRequest, 1024)
		go conditionWriter(conditionWriteQueues[i])
	}
}

func conditionWriter(queue <-chan conditionWriteRequest) {
	for first := range queue {
		batch := []conditionWriteRequest{first}
		timer := time.NewTimer(conditionBatchWait)
	collect:
		for len(batch) < conditionBatchMaxRequests {
			select {
			case request := <-queue:
				batch = append(batch, request)
			case <-timer.C:
				break collect
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		if err := writeConditionBatch(batch); err != nil {
			log.Errorf("condition write batch error: %v", err)
		}
	}
}

func conditionWriterQueue(jiaIsuUUID string) chan conditionWriteRequest {
	// 同じISUは常に同じwriterへ送り、到着順に処理する。
	// これによりwriter間で同じisu行を競合させない。
	hash := uint32(2166136261)
	for i := 0; i < len(jiaIsuUUID); i++ {
		hash ^= uint32(jiaIsuUUID[i])
		hash *= 16777619
	}
	return conditionWriteQueues[int(hash%conditionWriterCount)]
}

func writeConditionBatch(batch []conditionWriteRequest) error {
	type latestCondition struct {
		jiaIsuUUID string
		condition  PostIsuConditionRequest
	}

	rowCount := 0
	for _, request := range batch {
		rowCount += len(request.conditions)
	}
	placeholders := make([]string, 0, rowCount)
	args := make([]interface{}, 0, rowCount*5)
	latestByIsu := make(map[string]latestCondition)
	for _, request := range batch {
		for _, condition := range request.conditions {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?)")
			args = append(args,
				request.jiaIsuUUID,
				time.Unix(condition.Timestamp, 0),
				condition.IsSitting,
				condition.Condition,
				condition.Message,
			)
			latest, ok := latestByIsu[request.jiaIsuUUID]
			if !ok || condition.Timestamp >= latest.condition.Timestamp {
				latestByIsu[request.jiaIsuUUID] = latestCondition{request.jiaIsuUUID, condition}
			}
		}
	}

	// 明示 TX は不要。1文の INSERT 自体が原子的で、autocommit ですぐロック解放される。
	// latest UPDATE は別クエリ（失敗しても次バッチで埋まる）。
	_, err := db.Exec(
		"INSERT IGNORE INTO `isu_condition`"+
			" (`jia_isu_uuid`, `timestamp`, `is_sitting`, `condition`, `message`) VALUES "+
			strings.Join(placeholders, ", "),
		args...,
	)
	if err != nil {
		return err
	}
	// 当日グラフなどが古くなるので、書いた ISU のグラフキャッシュを捨てる
	for jiaIsuUUID := range latestByIsu {
		invalidateGraphCache(jiaIsuUUID)
	}

	uuids := make([]string, 0, len(latestByIsu))
	for jiaIsuUUID := range latestByIsu {
		uuids = append(uuids, jiaIsuUUID)
	}
	sort.Strings(uuids)
	updatedLatest := make([]latestCondition, 0, len(uuids))
	for _, jiaIsuUUID := range uuids {
		latest := latestByIsu[jiaIsuUUID]
		cachedTimestamp, cached := getCachedIsuLatestTimestamp(jiaIsuUUID)
		if cached && latest.condition.Timestamp < cachedTimestamp {
			continue
		}
		updatedLatest = append(updatedLatest, latest)
	}
	if len(updatedLatest) == 0 {
		return nil
	}

	var updateQuery strings.Builder
	updateArgs := make([]interface{}, 0, len(updatedLatest)*11)
	appendCase := func(column string, value func(latestCondition) interface{}) {
		updateQuery.WriteString(" `")
		updateQuery.WriteString(column)
		updateQuery.WriteString("` = CASE `jia_isu_uuid`")
		for _, latest := range updatedLatest {
			updateQuery.WriteString(" WHEN ? THEN ?")
			updateArgs = append(updateArgs, latest.jiaIsuUUID, value(latest))
		}
		updateQuery.WriteString(" ELSE `")
		updateQuery.WriteString(column)
		updateQuery.WriteString("` END")
	}

	updateQuery.WriteString("UPDATE `isu` SET")
	appendCase("latest_timestamp", func(latest latestCondition) interface{} {
		return time.Unix(latest.condition.Timestamp, 0)
	})
	updateQuery.WriteString(",")
	appendCase("latest_is_sitting", func(latest latestCondition) interface{} {
		return latest.condition.IsSitting
	})
	updateQuery.WriteString(",")
	appendCase("latest_condition", func(latest latestCondition) interface{} {
		return latest.condition.Condition
	})
	updateQuery.WriteString(",")
	appendCase("latest_message", func(latest latestCondition) interface{} {
		return latest.condition.Message
	})

	updateQuery.WriteString(" WHERE `jia_isu_uuid` IN (")
	updateQuery.WriteString(strings.TrimRight(strings.Repeat("?,", len(updatedLatest)), ","))
	updateQuery.WriteString(") AND (`latest_timestamp` IS NULL OR `latest_timestamp` <= CASE `jia_isu_uuid`")
	for _, latest := range updatedLatest {
		updateArgs = append(updateArgs, latest.jiaIsuUUID)
	}
	for _, latest := range updatedLatest {
		updateQuery.WriteString(" WHEN ? THEN ?")
		updateArgs = append(updateArgs, latest.jiaIsuUUID, time.Unix(latest.condition.Timestamp, 0))
	}
	updateQuery.WriteString(" END)")

	if _, err = db.Exec(updateQuery.String(), updateArgs...); err != nil {
		return err
	}
	for _, latest := range updatedLatest {
		setCachedIsuLatestTimestamp(latest.jiaIsuUUID, latest.condition.Timestamp)
	}
	return nil
}

// POST /api/condition/:jia_isu_uuid
// ISUからのコンディションを受け取る
func postIsuCondition(c echo.Context) error {
	// // TODO: 一定割合リクエストを落としてしのぐようにしたが、本来は全量さばけるようにすべき
	// 全部裁くぜ
	// dropProbability := 0.1
	// if rand.Float64() <= dropProbability {
	// 	return c.NoContent(http.StatusAccepted)
	// }

	jiaIsuUUID := c.Param("jia_isu_uuid")
	if jiaIsuUUID == "" {
		return c.String(http.StatusBadRequest, "missing: jia_isu_uuid")
	}

	body, err := ioutil.ReadAll(c.Request().Body)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad request body")
	}
	req := []PostIsuConditionRequest{}
	if err := jsonFast.Unmarshal(body, &req); err != nil {
		return c.String(http.StatusBadRequest, "bad request body")
	} else if len(req) == 0 {
		return c.String(http.StatusBadRequest, "bad request body")
	}

	isuExists, cached := getCachedIsuExistence(jiaIsuUUID)
	if !cached {
		var exists int
		err = db.Get(&exists, "SELECT EXISTS(SELECT 1 FROM `isu` WHERE `jia_isu_uuid` = ?)", jiaIsuUUID)
		if err != nil {
			c.Logger().Errorf("db error: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
		isuExists = exists == 1
		setCachedIsuExistence(jiaIsuUUID, isuExists)
	}
	if !isuExists {
		return c.String(http.StatusNotFound, "not found: isu")
	}

	for i := range req {
		if !isValidConditionFormat(req[i].Condition) {
			return c.String(http.StatusBadRequest, "bad request body")
		}
	}

	writeRequest := conditionWriteRequest{
		jiaIsuUUID: jiaIsuUUID,
		conditions: req,
	}

	// DBコミットは待たず、キュー投入成功時点で202を返す。
	// JIAクライアントは短いタイムアウトなので、書き込み完了待ちはCLOSE-WAITの原因になりやすい。
	requestContext := c.Request().Context()
	select {
	case conditionWriterQueue(jiaIsuUUID) <- writeRequest:
		return c.NoContent(http.StatusAccepted)
	case <-requestContext.Done():
		return nil
	}
}

// ISUのコンディションの文字列がcsv形式になっているか検証
func isValidConditionFormat(conditionStr string) bool {

	keys := []string{"is_dirty=", "is_overweight=", "is_broken="}
	const valueTrue = "true"
	const valueFalse = "false"

	idxCondStr := 0

	for idxKeys, key := range keys {
		if !strings.HasPrefix(conditionStr[idxCondStr:], key) {
			return false
		}
		idxCondStr += len(key)

		if strings.HasPrefix(conditionStr[idxCondStr:], valueTrue) {
			idxCondStr += len(valueTrue)
		} else if strings.HasPrefix(conditionStr[idxCondStr:], valueFalse) {
			idxCondStr += len(valueFalse)
		} else {
			return false
		}

		if idxKeys < (len(keys) - 1) {
			if conditionStr[idxCondStr] != ',' {
				return false
			}
			idxCondStr++
		}
	}

	return (idxCondStr == len(conditionStr))
}
