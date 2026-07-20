package main

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
)

// ISUのコンディショングラフ描画のための情報を取得
func getIsuGraph(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")
	datetimeStr := c.QueryParam("datetime")
	if datetimeStr == "" {
		return c.String(http.StatusBadRequest, "missing: datetime")
	}
	datetimeInt64, err := strconv.ParseInt(datetimeStr, 10, 64)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad format: datetime")
	}
	date := graphCacheDay(time.Unix(datetimeInt64, 0))

	owner, cached := getCachedIsuOwner(jiaIsuUUID)
	if cached {
		if owner != jiaUserID {
			return c.String(http.StatusNotFound, "not found: isu")
		}
	} else {
		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM `isu` WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
			jiaUserID, jiaIsuUUID)
		if err != nil {
			c.Logger().Errorf("db error: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
		if count == 0 {
			return c.String(http.StatusNotFound, "not found: isu")
		}
		setCachedIsuOwner(jiaIsuUUID, jiaUserID)
	}

	if body, ok := getCachedGraph(jiaIsuUUID, date); ok {
		return c.JSONBlob(http.StatusOK, body)
	}

	res, err := generateIsuGraphResponse(db, jiaIsuUUID, date)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	body, err := jsonFast.Marshal(res)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}
	setCachedGraph(jiaIsuUUID, date, body)
	return c.JSONBlob(http.StatusOK, body)
}

// グラフのデータ点を一日分生成
func generateIsuGraphResponse(db *sqlx.DB, jiaIsuUUID string, graphDate time.Time) ([]GraphResponse, error) {
	dataPoints := make([]GraphDataPointWithInfo, 0, 24)
	conditionsInThisHour := make([]isuConditionGraphRow, 0, 64)
	timestampsInThisHour := make([]int64, 0, 64)
	var startTimeInThisHour time.Time
	var startedHour bool
	var row isuConditionGraphRow
	endTime := graphDate.Add(time.Hour * 24)

	rows, err := db.Queryx(
		"SELECT `timestamp`, `is_sitting`, `condition` FROM `isu_condition` WHERE `jia_isu_uuid` = ? AND `timestamp` >= ? AND `timestamp` < ? ORDER BY `timestamp` ASC",
		jiaIsuUUID, graphDate, endTime,
	)
	if err != nil {
		return nil, fmt.Errorf("db error: %v", err)
	}
	defer rows.Close()

	flushHour := func() error {
		if len(conditionsInThisHour) == 0 {
			return nil
		}
		data, err := calculateGraphDataPoint(conditionsInThisHour)
		if err != nil {
			return err
		}
		// timestamps は後でレスポンスに載せるのでコピーする
		ts := make([]int64, len(timestampsInThisHour))
		copy(ts, timestampsInThisHour)
		dataPoints = append(dataPoints, GraphDataPointWithInfo{
			JIAIsuUUID:          jiaIsuUUID,
			StartAt:             startTimeInThisHour,
			Data:                data,
			ConditionTimestamps: ts,
		})
		conditionsInThisHour = conditionsInThisHour[:0]
		timestampsInThisHour = timestampsInThisHour[:0]
		return nil
	}

	for rows.Next() {
		err = rows.StructScan(&row)
		if err != nil {
			return nil, err
		}

		truncatedConditionTime := row.Timestamp.Truncate(time.Hour)
		if !startedHour || truncatedConditionTime != startTimeInThisHour {
			if err := flushHour(); err != nil {
				return nil, err
			}
			startTimeInThisHour = truncatedConditionTime
			startedHour = true
		}
		conditionsInThisHour = append(conditionsInThisHour, row)
		timestampsInThisHour = append(timestampsInThisHour, row.Timestamp.Unix())
	}

	if err := flushHour(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	responseList := make([]GraphResponse, 0, 24)
	index := 0
	dayEnd := graphDate.Add(time.Hour * 24)
	for thisTime := graphDate; thisTime.Before(dayEnd); thisTime = thisTime.Add(time.Hour) {
		var data *GraphDataPoint
		timestamps := []int64{}

		if index < len(dataPoints) {
			dataWithInfo := dataPoints[index]
			if dataWithInfo.StartAt.Equal(thisTime) {
				data = &dataWithInfo.Data
				timestamps = dataWithInfo.ConditionTimestamps
				index++
			}
		}

		responseList = append(responseList, GraphResponse{
			StartAt:             thisTime.Unix(),
			EndAt:               thisTime.Add(time.Hour).Unix(),
			Data:                data,
			ConditionTimestamps: timestamps,
		})
	}

	return responseList, nil
}

// 複数のISUのコンディションからグラフの一つのデータ点を計算
func calculateGraphDataPoint(isuConditions []isuConditionGraphRow) (GraphDataPoint, error) {
	rawScore := 0
	sittingCount := 0
	isBrokenCount := 0
	isDirtyCount := 0
	isOverweightCount := 0

	for _, condition := range isuConditions {
		meta, ok := graphConditionByString[condition.Condition]
		if !ok {
			return GraphDataPoint{}, fmt.Errorf("invalid condition format")
		}
		rawScore += meta.rawScore
		if condition.IsSitting {
			sittingCount++
		}
		if meta.isBroken {
			isBrokenCount++
		}
		if meta.isDirty {
			isDirtyCount++
		}
		if meta.isOverweight {
			isOverweightCount++
		}
	}

	n := len(isuConditions)
	return GraphDataPoint{
		Score: rawScore * 100 / 3 / n,
		Percentage: ConditionsPercentage{
			Sitting:      sittingCount * 100 / n,
			IsBroken:     isBrokenCount * 100 / n,
			IsOverweight: isOverweightCount * 100 / n,
			IsDirty:      isDirtyCount * 100 / n,
		},
	}, nil
}

// initialize 後に種データのグラフを全部載せる。以降、書いた日以外は消しません。
func warmGraphCache() error {
	type job struct {
		uuid string
		day  time.Time
	}
	// DATE() は session tz 依存なので、全行を舐めて JST 日でユニーク化する
	rows, err := db.Queryx("SELECT `jia_isu_uuid`, `timestamp` FROM `isu_condition`")
	if err != nil {
		return fmt.Errorf("select graph warm rows: %w", err)
	}
	type dayKey struct {
		uuid string
		day  int64
	}
	seen := make(map[dayKey]struct{}, 4096)
	jobsList := make([]job, 0, 4096)
	for rows.Next() {
		var uuid string
		var ts time.Time
		if err := rows.Scan(&uuid, &ts); err != nil {
			rows.Close()
			return fmt.Errorf("scan graph warm row: %w", err)
		}
		day := graphCacheDay(ts)
		key := dayKey{uuid: uuid, day: day.Unix()}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		jobsList = append(jobsList, job{uuid: uuid, day: day})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate graph warm rows: %w", err)
	}
	if len(jobsList) == 0 {
		return nil
	}

	jobs := make(chan job, len(jobsList))
	for _, j := range jobsList {
		jobs <- j
	}
	close(jobs)

	workerCount := graphCacheWarmWorkers
	if workerCount > len(jobsList) {
		workerCount = len(jobsList)
	}
	errCh := make(chan error, workerCount)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				res, err := generateIsuGraphResponse(db, j.uuid, j.day)
				if err != nil {
					errCh <- err
					return
				}
				body, err := jsonFast.Marshal(res)
				if err != nil {
					errCh <- err
					return
				}
				setCachedGraph(j.uuid, j.day, body)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// GET /api/condition/:jia_isu_uuid
