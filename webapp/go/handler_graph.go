package main

import (
	"net/http"
	"sort"
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

	// 全日 seal 済みなら組み立て・Marshal を省略（GraphGood の本命）
	if body, ok := getCachedGraphJSON(jiaIsuUUID, date); ok {
		return c.JSONBlob(http.StatusOK, body)
	}

	res, err := getIsuGraphResponse(jiaIsuUUID, date)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}
	// seal で jsonBody が載った直後は再取得（二重 Marshal を避ける）
	if body, ok := getCachedGraphJSON(jiaIsuUUID, date); ok {
		return c.JSONBlob(http.StatusOK, body)
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

// graphBuildRW は ISU×日 ごとの RWMutex を返す。
// 日単位にすることで、seal 中でも他日の GET を止めない。
func graphBuildRW(jiaIsuUUID string, day time.Time) *sync.RWMutex {
	key := jiaIsuUUID + "\x00" + strconv.FormatInt(graphCacheDay(day).Unix(), 10)
	v, _ := graphBuildMu.LoadOrStore(key, &sync.RWMutex{})
	return v.(*sync.RWMutex)
}

// lockGraphBuild は seal / キャッシュ更新用の排他ロック。
func lockGraphBuild(jiaIsuUUID string, day time.Time) func() {
	mu := graphBuildRW(jiaIsuUUID, day)
	mu.Lock()
	return mu.Unlock
}

// rlockGraphBuild は組み立て済みキャッシュを読むだけの共有ロック。
func rlockGraphBuild(jiaIsuUUID string, day time.Time) func() {
	mu := graphBuildRW(jiaIsuUUID, day)
	mu.RLock()
	return mu.RUnlock
}

// getIsuGraphResponse は 1 日分のグラフを組み立てる。
//   - 仮想現在より前の時間帯: グラフキャッシュ（未確定なら大本メモリから確定）
//   - 仮想現在の時間帯: 大本メモリから都度集計
//   - 仮想現在より後: 空
// 読みだけなら RLock で並列、seal が必要なら Lock。
func getIsuGraphResponse(jiaIsuUUID string, date time.Time) ([]GraphResponse, error) {
	dayEnd := date.Add(24 * time.Hour)
	virtualNow, ok := isuVirtualNow(jiaIsuUUID)
	if !ok {
		unlock := lockGraphBuild(jiaIsuUUID, date)
		defer unlock()
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

	// すでに seal 済みなら RLock だけで返す（同一 ISU の複数日 GET を並列化）
	unlockR := rlockGraphBuild(jiaIsuUUID, date)
	entry, cached := getCachedGraphEntry(jiaIsuUUID, date)
	sealedThrough := date
	var res []GraphResponse
	if cached {
		res = entry.response
		sealedThrough = time.Unix(entry.sealedThrough, 0).In(graphCacheLocation)
		if sealedThrough.Before(date) {
			sealedThrough = date
		}
	}
	needSeal := !cached || sealedThrough.Before(sealUntil)
	if !needSeal {
		res = finishGraphDayResponse(jiaIsuUUID, res, date, dayEnd, openHour)
		unlockR()
		return res, nil
	}
	unlockR()

	unlock := lockGraphBuild(jiaIsuUUID, date)
	defer unlock()

	entry, cached = getCachedGraphEntry(jiaIsuUUID, date)
	sealedThrough = date
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

	return finishGraphDayResponse(jiaIsuUUID, res, date, dayEnd, openHour), nil
}

// finishGraphDayResponse は open hour / 未来枠を載せた日次レスポンスを返す。
func finishGraphDayResponse(jiaIsuUUID string, res []GraphResponse, date, dayEnd, openHour time.Time) []GraphResponse {
	if res == nil {
		res = emptyGraphDay(date)
	}
	// 開いている時間帯は大本メモリから集計（日次キャッシュへは書かない）
	if !openHour.Before(date) && openHour.Before(dayEnd) {
		idx := int(openHour.Sub(date) / time.Hour)
		res[idx] = getOrGenerateOpenHourGraph(jiaIsuUUID, openHour)
		for hourStart := openHour.Add(time.Hour); hourStart.Before(dayEnd); hourStart = hourStart.Add(time.Hour) {
			i := int(hourStart.Sub(date) / time.Hour)
			res[i] = GraphResponse{
				StartAt:             hourStart.Unix(),
				EndAt:               hourStart.Add(time.Hour).Unix(),
				ConditionTimestamps: []int64{},
			}
		}
	}
	return res
}

// generateIsuGraphHour は [hourStart, hourStart+1h) を大本メモリから1パスで集計する。
func generateIsuGraphHour(jiaIsuUUID string, hourStart time.Time) GraphResponse {
	hourEnd := hourStart.Add(time.Hour)
	startUnix := hourStart.Unix()
	endUnix := hourEnd.Unix()
	resp := GraphResponse{
		StartAt:             startUnix,
		EndAt:               endUnix,
		ConditionTimestamps: []int64{},
	}

	conditionStore.RLock()
	mem := conditionStore.byIsu[jiaIsuUUID]
	conditionStore.RUnlock()
	if mem == nil {
		return resp
	}

	mem.RLock()
	items := mem.items
	lo := sort.Search(len(items), func(i int) bool {
		return items[i].Timestamp >= startUnix
	})
	hi := sort.Search(len(items), func(i int) bool {
		return items[i].Timestamp >= endUnix
	})
	if lo >= hi {
		mem.RUnlock()
		return resp
	}

	n := hi - lo
	timestamps := make([]int64, n)
	rawScore := 0
	sittingCount := 0
	isBrokenCount := 0
	isDirtyCount := 0
	isOverweightCount := 0
	ok := true
	for i := lo; i < hi; i++ {
		item := items[i]
		timestamps[i-lo] = item.Timestamp
		meta, found := graphConditionByString[item.Condition]
		if !found {
			ok = false
			break
		}
		rawScore += meta.rawScore
		if item.IsSitting {
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
	mem.RUnlock()
	if !ok {
		return resp
	}

	data := GraphDataPoint{
		Score: rawScore * 100 / 3 / n,
		Percentage: ConditionsPercentage{
			Sitting:      sittingCount * 100 / n,
			IsBroken:     isBrokenCount * 100 / n,
			IsOverweight: isOverweightCount * 100 / n,
			IsDirty:      isDirtyCount * 100 / n,
		},
	}
	resp.Data = &data
	resp.ConditionTimestamps = timestamps
	return resp
}

// getOrGenerateOpenHourGraph は当日 open hour を返す。
// latest が変わっていなければ前回結果を再利用する（TodayGraph 向け）。
func getOrGenerateOpenHourGraph(jiaIsuUUID string, hourStart time.Time) GraphResponse {
	hourUnix := hourStart.Unix()
	latest, hasLatest := getCachedIsuLatestTimestamp(jiaIsuUUID)

	openHourGraphCache.RLock()
	entry, ok := openHourGraphCache.values[jiaIsuUUID]
	openHourGraphCache.RUnlock()
	if ok && entry.hourStart == hourUnix && hasLatest && entry.throughTs == latest {
		return cloneGraphResponse(entry.response)
	}

	resp := generateIsuGraphHour(jiaIsuUUID, hourStart)
	throughTs := latest
	if n := len(resp.ConditionTimestamps); n > 0 {
		throughTs = resp.ConditionTimestamps[n-1]
	}
	openHourGraphCache.Lock()
	openHourGraphCache.values[jiaIsuUUID] = openHourGraphCacheEntry{
		hourStart: hourUnix,
		throughTs: throughTs,
		response:  cloneGraphResponse(resp),
	}
	openHourGraphCache.Unlock()
	return resp
}

func cloneGraphResponse(src GraphResponse) GraphResponse {
	dst := src
	if src.ConditionTimestamps != nil {
		dst.ConditionTimestamps = append([]int64(nil), src.ConditionTimestamps...)
	}
	if src.Data != nil {
		data := *src.Data
		dst.Data = &data
	}
	return dst
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
// 日単位ロックなので、他日の GraphGood GET をブロックしない。
func sealGraphHoursInRange(jiaIsuUUID string, fromHour, toHour time.Time) {
	for hourStart := fromHour; hourStart.Before(toHour); {
		day := graphCacheDay(hourStart)
		dayEnd := day.Add(24 * time.Hour)
		rangeEnd := toHour
		if dayEnd.Before(rangeEnd) {
			rangeEnd = dayEnd
		}

		unlock := lockGraphBuild(jiaIsuUUID, day)
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

		if sealedThrough.Before(rangeEnd) {
			for h := sealedThrough; h.Before(rangeEnd); h = h.Add(time.Hour) {
				idx := int(h.Sub(day) / time.Hour)
				if idx >= 0 && idx < 24 {
					res[idx] = generateIsuGraphHour(jiaIsuUUID, h)
				}
			}
			sealedThrough = rangeEnd
			setCachedGraph(jiaIsuUUID, day, res, sealedThrough)
		}
		unlock()
		hourStart = rangeEnd
	}
}

// warmIsuLatestTimestamps は大本メモリの末尾を最新 condition にする。
func warmIsuLatestTimestamps() {
	conditionStore.RLock()
	defer conditionStore.RUnlock()
	for uuid, mem := range conditionStore.byIsu {
		mem.RLock()
		if n := len(mem.items); n > 0 {
			last := mem.items[n-1]
			setCachedIsuLatestCondition(uuid, last.Timestamp, last.IsSitting, last.Condition, last.Message)
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
