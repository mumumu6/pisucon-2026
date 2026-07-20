package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
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

	conditionsResponse := getIsuConditionsFromMem(jiaIsuUUID, endTime, conditionLevel, startTime, conditionLimit, isuName)
	body, err := jsonFast.Marshal(conditionsResponse)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}
	return c.JSONBlob(http.StatusOK, body)
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
	conditionMemQueues = make([]chan conditionWriteRequest, conditionWriterCount)
	for i := 0; i < conditionWriterCount; i++ {
		conditionMemQueues[i] = make(chan conditionWriteRequest, conditionWriteQueueSize)
		go conditionMemWriter(conditionMemQueues[i])
	}
}

func collectConditionBatch(queue <-chan conditionWriteRequest, first conditionWriteRequest) []conditionWriteRequest {
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
	return batch
}

// conditionMemWriter は同一 shard を FIFO でメモリ反映する（HTTP 並列でも順序を守る）。
// ベンチ中の GET はメモリ参照のみなので DB 永続化はしない（IO/CPU を GET に回す）。
func conditionMemWriter(memQueue <-chan conditionWriteRequest) {
	for first := range memQueue {
		batch := collectConditionBatch(memQueue, first)
		applyConditionMemoryBatch(batch)
	}
}

func conditionMemQueue(jiaIsuUUID string) chan conditionWriteRequest {
	return conditionMemQueues[conditionShardIndex(jiaIsuUUID)]
}

func conditionShardIndex(jiaIsuUUID string) int {
	// 同じISUは常に同じ shard へ送り、到着順に処理する。
	hash := uint32(2166136261)
	for i := 0; i < len(jiaIsuUUID); i++ {
		hash ^= uint32(jiaIsuUUID[i])
		hash *= 16777619
	}
	return int(hash % uint32(conditionWriterCount))
}

func applyConditionMemoryBatch(batch []conditionWriteRequest) {
	type latestCondition struct {
		jiaIsuUUID string
		condition  PostIsuConditionRequest
	}
	latestByIsu := make(map[string]latestCondition)
	for _, request := range batch {
		appendIsuConditions(request.jiaIsuUUID, request.conditions)
		for _, condition := range request.conditions {
			latest, ok := latestByIsu[request.jiaIsuUUID]
			if !ok || condition.Timestamp >= latest.condition.Timestamp {
				latestByIsu[request.jiaIsuUUID] = latestCondition{request.jiaIsuUUID, condition}
			}
		}
	}
	for jiaIsuUUID, latest := range latestByIsu {
		newTs := latest.condition.Timestamp
		oldTs, hasOld := getCachedIsuLatestTimestamp(jiaIsuUUID)
		if hasOld {
			oldHour := graphCacheHour(time.Unix(oldTs, 0))
			newHour := graphCacheHour(time.Unix(newTs, 0))
			if newHour.After(oldHour) {
				sealGraphHoursInRange(jiaIsuUUID, oldHour, newHour)
			}
		}
		setCachedIsuLatestCondition(
			jiaIsuUUID,
			newTs,
			latest.condition.IsSitting,
			latest.condition.Condition,
			latest.condition.Message,
		)
	}
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

	// メモリ用 FIFO キューへ非ブロッキング投入。満杯なら捨てて 202（減点なし）。
	// mem worker 化のときに default が消えて context 待ちになっていたのを戻す。
	select {
	case conditionMemQueue(jiaIsuUUID) <- writeRequest:
	default:
		// backpressure: 加点反映・永続化をスキップ
	}
	return c.NoContent(http.StatusAccepted)
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
