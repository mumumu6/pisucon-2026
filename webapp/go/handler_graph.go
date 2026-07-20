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

	res, err := getIsuGraphResponse(db, jiaIsuUUID, date)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}
	body, err := jsonFast.Marshal(res)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}
	return c.JSONBlob(http.StatusOK, body)
}

func emptyGraphDay(graphDate time.Time) []GraphResponse {
	res := make([]GraphResponse, 24)
	for i := 0; i < 24; i++ {
		hourStart := graphDate.Add(time.Duration(i) * time.Hour)
		res[i] = GraphResponse{
			StartAt:             hourStart.Unix(),
			EndAt:               hourStart.Add(time.Hour).Unix(),
			ConditionTimestamps: []int64{},
		}
	}
	return res
}

// 過去日は全日キャッシュ。当日は閉じた時間帯だけキャッシュし、開いている時間帯は都度読む。
func getIsuGraphResponse(db *sqlx.DB, jiaIsuUUID string, date time.Time) ([]GraphResponse, error) {
	now := time.Now().In(graphCacheLocation)
	today := graphCacheDay(now)
	dayEnd := date.Add(24 * time.Hour)

	// 過去日: 全日確定。前日の 23 台がまだならここで埋める。
	if date.Before(today) {
		if entry, ok := getCachedGraphEntry(jiaIsuUUID, date); ok {
			if entry.sealedThrough >= dayEnd.Unix() {
				return entry.response, nil
			}
			res := make([]GraphResponse, 24)
			copy(res, entry.response)
			sealedThrough := time.Unix(entry.sealedThrough, 0).In(graphCacheLocation)
			if sealedThrough.Before(date) {
				sealedThrough = date
			}
			for hourStart := sealedThrough; hourStart.Before(dayEnd); hourStart = hourStart.Add(time.Hour) {
				idx := int(hourStart.Sub(date) / time.Hour)
				slot, err := generateIsuGraphHour(db, jiaIsuUUID, hourStart)
				if err != nil {
					return nil, err
				}
				res[idx] = slot
			}
			setCachedGraph(jiaIsuUUID, date, res, dayEnd)
			return res, nil
		}
		res, err := generateIsuGraphResponse(db, jiaIsuUUID, date)
		if err != nil {
			return nil, err
		}
		setCachedGraph(jiaIsuUUID, date, res, dayEnd)
		return res, nil
	}

	// 未来日
	if date.After(today) {
		return emptyGraphDay(date), nil
	}

	// 当日: sealedThrough 未満は確定。currentHour は毎回 DB。
	currentHour := graphCacheHour(now)
	entry, ok := getCachedGraphEntry(jiaIsuUUID, date)
	var res []GraphResponse
	sealedThrough := date
	if ok {
		res = make([]GraphResponse, 24)
		copy(res, entry.response)
		sealedThrough = time.Unix(entry.sealedThrough, 0).In(graphCacheLocation)
		if sealedThrough.Before(date) {
			sealedThrough = date
		}
	} else {
		// 初回
		res = emptyGraphDay(date)
	}

	// 15:00 を過ぎたら 14:00 台をここで一度だけ確定する
	if sealedThrough.Before(currentHour) {
		for hourStart := sealedThrough; hourStart.Before(currentHour); hourStart = hourStart.Add(time.Hour) {
			idx := int(hourStart.Sub(date) / time.Hour)
			if idx < 0 || idx >= 24 {
				continue
			}
			slot, err := generateIsuGraphHour(db, jiaIsuUUID, hourStart)
			if err != nil {
				return nil, err
			}
			res[idx] = slot
		}
		sealedThrough = currentHour
		setCachedGraph(jiaIsuUUID, date, res, sealedThrough)
	}

	// 開いている時間帯だけ都度生成（キャッシュしない）
	if !currentHour.Before(date) && currentHour.Before(dayEnd) {
		idx := int(currentHour.Sub(date) / time.Hour)
		slot, err := generateIsuGraphHour(db, jiaIsuUUID, currentHour)
		if err != nil {
			return nil, err
		}
		res[idx] = slot
	}
	return res, nil
}

// 1時間帯ぶんだけ生成
func generateIsuGraphHour(db *sqlx.DB, jiaIsuUUID string, hourStart time.Time) (GraphResponse, error) {
	hourEnd := hourStart.Add(time.Hour)
	rows := make([]isuConditionGraphRow, 0, 64)
	err := db.Select(
		&rows,
		"SELECT `timestamp`, `is_sitting`, `condition` FROM `isu_condition` WHERE `jia_isu_uuid` = ? AND `timestamp` >= ? AND `timestamp` < ? ORDER BY `timestamp` ASC",
		jiaIsuUUID, hourStart, hourEnd,
	)
	if err != nil {
		return GraphResponse{}, fmt.Errorf("db error: %v", err)
	}
	resp := GraphResponse{
		StartAt:             hourStart.Unix(),
		EndAt:               hourEnd.Unix(),
		ConditionTimestamps: []int64{},
	}
	if len(rows) == 0 {
		return resp, nil
	}
	data, err := calculateGraphDataPoint(rows)
	if err != nil {
		return GraphResponse{}, err
	}
	timestamps := make([]int64, len(rows))
	for i := range rows {
		timestamps[i] = rows[i].Timestamp.Unix()
	}
	resp.Data = &data
	resp.ConditionTimestamps = timestamps
	return resp, nil
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

// initialize 後に種データのグラフを載せる。過去日は全日確定、当日は現在時より前だけ確定。
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
	now := time.Now().In(graphCacheLocation)
	today := graphCacheDay(now)
	currentHour := graphCacheHour(now)

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
				sealedThrough := j.day.Add(24 * time.Hour)
				if !j.day.Before(today) {
					// 当日: 開いている時間帯はまだ確定しない
					sealedThrough = currentHour
					if sealedThrough.Before(j.day) {
						sealedThrough = j.day
					}
					if sealedThrough.After(j.day.Add(24 * time.Hour)) {
						sealedThrough = j.day.Add(24 * time.Hour)
					}
				}
				setCachedGraph(j.uuid, j.day, res, sealedThrough)
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
