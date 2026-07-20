package main

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

// GET /api/isu/:jia_isu_uuid/graph
// 指定日の 24 時間グラフを返す。中身の組み立ては getIsuGraphResponse。
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

	res, err := getIsuGraphResponse(jiaIsuUUID, date)
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

// emptyGraphDay はデータなしの 24 スロット（1時間ごと）を作る。
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

// isuVirtualNow はその ISU の「仮想現在時刻」を返す。
// 壁時計ではなく、届いた condition の最新 timestamp（ISU 単位で単調増加）。
func isuVirtualNow(jiaIsuUUID string) (time.Time, bool) {
	ts, ok := getCachedIsuLatestTimestamp(jiaIsuUUID)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(ts, 0).In(graphCacheLocation), true
}

// lockGraphBuild は同一 ISU のグラフ組み立てを直列化する。
func lockGraphBuild(jiaIsuUUID string) func() {
	v, _ := graphBuildMu.LoadOrStore(jiaIsuUUID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// getIsuGraphResponse は 1 日分のグラフを組み立てる。
//   - 仮想現在より前の時間帯: グラフキャッシュ（未確定なら大本メモリから確定）
//   - 仮想現在の時間帯: 大本メモリから都度集計
//   - 仮想現在より後: 空
func getIsuGraphResponse(jiaIsuUUID string, date time.Time) ([]GraphResponse, error) {
	unlock := lockGraphBuild(jiaIsuUUID)
	defer unlock()

	dayEnd := date.Add(24 * time.Hour)
	virtualNow, ok := isuVirtualNow(jiaIsuUUID)
	if !ok {
		res := generateIsuGraphDayFromMem(jiaIsuUUID, date)
		setCachedGraph(jiaIsuUUID, date, res, dayEnd)
		return res, nil
	}

	openHour := graphCacheHour(virtualNow)
	progressDay := graphCacheDay(openHour)

	// 仮想現在より後の日
	if date.After(progressDay) {
		return emptyGraphDay(date), nil
	}

	sealUntil := openHour
	if date.Before(progressDay) {
		sealUntil = dayEnd // 過去日は全日確定
	}
	if sealUntil.After(dayEnd) {
		sealUntil = dayEnd
	}
	if sealUntil.Before(date) {
		sealUntil = date
	}

	entry, cached := getCachedGraphEntry(jiaIsuUUID, date)
	var res []GraphResponse
	sealedThrough := date
	if cached {
		res = entry.response
		sealedThrough = time.Unix(entry.sealedThrough, 0).In(graphCacheLocation)
		if sealedThrough.Before(date) {
			sealedThrough = date
		}
	} else {
		res = emptyGraphDay(date)
	}

	if sealedThrough.Before(sealUntil) {
		for hourStart := sealedThrough; hourStart.Before(sealUntil); hourStart = hourStart.Add(time.Hour) {
			idx := int(hourStart.Sub(date) / time.Hour)
			if idx < 0 || idx >= 24 {
				continue
			}
			res[idx] = generateIsuGraphHour(jiaIsuUUID, hourStart)
		}
		sealedThrough = sealUntil
		setCachedGraph(jiaIsuUUID, date, res, sealedThrough)
	}

	// 開いている時間帯は大本メモリから集計（キャッシュへは書かない。setCachedGraph は複製保存）
	if !openHour.Before(date) && openHour.Before(dayEnd) {
		idx := int(openHour.Sub(date) / time.Hour)
		res[idx] = generateIsuGraphHour(jiaIsuUUID, openHour)
	}

	// 仮想現在より後の時間帯は空
	if !openHour.Before(date) && openHour.Before(dayEnd) {
		for hourStart := openHour.Add(time.Hour); hourStart.Before(dayEnd); hourStart = hourStart.Add(time.Hour) {
			idx := int(hourStart.Sub(date) / time.Hour)
			res[idx] = GraphResponse{
				StartAt:             hourStart.Unix(),
				EndAt:               hourStart.Add(time.Hour).Unix(),
				ConditionTimestamps: []int64{},
			}
		}
	}

	return res, nil
}

// generateIsuGraphHour は [hourStart, hourStart+1h) を大本メモリから集計する。
func generateIsuGraphHour(jiaIsuUUID string, hourStart time.Time) GraphResponse {
	hourEnd := hourStart.Add(time.Hour)
	resp := GraphResponse{
		StartAt:             hourStart.Unix(),
		EndAt:               hourEnd.Unix(),
		ConditionTimestamps: []int64{},
	}
	rows := conditionsInHourFromMem(jiaIsuUUID, hourStart, hourEnd)
	if len(rows) == 0 {
		return resp
	}
	data, err := calculateGraphDataPoint(rows)
	if err != nil {
		return resp
	}
	timestamps := make([]int64, len(rows))
	for i := range rows {
		timestamps[i] = rows[i].Timestamp.Unix()
	}
	resp.Data = &data
	resp.ConditionTimestamps = timestamps
	return resp
}

// generateIsuGraphDayFromMem は 1 日分を大本メモリから作る（warm 用）。
func generateIsuGraphDayFromMem(jiaIsuUUID string, graphDate time.Time) []GraphResponse {
	res := emptyGraphDay(graphDate)
	for i := 0; i < 24; i++ {
		hourStart := graphDate.Add(time.Duration(i) * time.Hour)
		res[i] = generateIsuGraphHour(jiaIsuUUID, hourStart)
	}
	return res
}

// sealGraphHoursInRange は [fromHour, toHour) をグラフキャッシュに確定する。
func sealGraphHoursInRange(jiaIsuUUID string, fromHour, toHour time.Time) {
	unlock := lockGraphBuild(jiaIsuUUID)
	defer unlock()

	for hourStart := fromHour; hourStart.Before(toHour); hourStart = hourStart.Add(time.Hour) {
		day := graphCacheDay(hourStart)
		dayEnd := day.Add(24 * time.Hour)
		entry, ok := getCachedGraphEntry(jiaIsuUUID, day)
		var res []GraphResponse
		sealedThrough := day
		if ok {
			res = entry.response
			sealedThrough = time.Unix(entry.sealedThrough, 0).In(graphCacheLocation)
			if sealedThrough.Before(day) {
				sealedThrough = day
			}
		} else {
			res = emptyGraphDay(day)
		}
		next := hourStart.Add(time.Hour)
		if !sealedThrough.Before(next) {
			continue
		}
		for h := sealedThrough; h.Before(next) && h.Before(dayEnd); h = h.Add(time.Hour) {
			idx := int(h.Sub(day) / time.Hour)
			res[idx] = generateIsuGraphHour(jiaIsuUUID, h)
		}
		if next.After(sealedThrough) {
			sealedThrough = next
		}
		if sealedThrough.After(dayEnd) {
			sealedThrough = dayEnd
		}
		setCachedGraph(jiaIsuUUID, day, res, sealedThrough)
	}
}

// 複数のISUのコンディションからグラフの一つのデータ点を計算
// calculateGraphDataPoint は 1 時間帯内の condition 列から score / 割合を計算する。
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

// warmIsuLatestTimestamps は大本メモリの末尾 timestamp を仮想現在時刻にする。
func warmIsuLatestTimestamps() {
	conditionStore.RLock()
	defer conditionStore.RUnlock()
	for uuid, mem := range conditionStore.byIsu {
		mem.RLock()
		if n := len(mem.items); n > 0 {
			setCachedIsuLatestTimestamp(uuid, mem.items[n-1].Timestamp)
		}
		mem.RUnlock()
	}
}

// warmGraphCache は大本メモリから日毎グラフを載せる。
func warmGraphCache() {
	type job struct {
		uuid string
		day  time.Time
	}
	type dayKey struct {
		uuid string
		day  int64
	}
	seen := make(map[dayKey]struct{}, 4096)
	jobsList := make([]job, 0, 4096)

	conditionStore.RLock()
	for uuid, mem := range conditionStore.byIsu {
		mem.RLock()
		for _, item := range mem.items {
			day := graphCacheDay(time.Unix(item.Timestamp, 0))
			key := dayKey{uuid: uuid, day: day.Unix()}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			jobsList = append(jobsList, job{uuid: uuid, day: day})
		}
		mem.RUnlock()
	}
	conditionStore.RUnlock()

	if len(jobsList) == 0 {
		return
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
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				res := generateIsuGraphDayFromMem(j.uuid, j.day)
				dayEnd := j.day.Add(24 * time.Hour)
				sealedThrough := dayEnd
				if virtualNow, ok := isuVirtualNow(j.uuid); ok {
					openHour := graphCacheHour(virtualNow)
					progressDay := graphCacheDay(openHour)
					if j.day.Equal(progressDay) {
						sealedThrough = openHour
					} else if j.day.After(progressDay) {
						sealedThrough = j.day
					}
				}
				setCachedGraph(j.uuid, j.day, res, sealedThrough)
			}
		}()
	}
	wg.Wait()
}

// GET /api/condition/:jia_isu_uuid
