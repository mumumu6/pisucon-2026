package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
)

// ISUの性格毎の最新のコンディション情報
func getTrend(c echo.Context) error {
	body, err := getTrendJSON()
	if err != nil {
		c.Logger().Errorf("failed to build trend response: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	return c.JSONBlob(http.StatusOK, body)
}

func getTrendJSON() ([]byte, error) {
	now := time.Now()
	trendCache.RLock()
	body := trendCache.body
	fresh := len(body) > 0 && now.Before(trendCache.expiresAt)
	stale := len(body) > 0 && now.Before(trendCache.staleUntil)
	trendCache.RUnlock()

	if fresh {
		return body, nil
	}

	// fresh期限後から MaxAge までは stale を即返し、裏で更新する。
	// それより古い値は返さない。
	if stale {
		if done, started := startTrendCacheRefresh(); started {
			go refreshTrendCache(done)
		}
		return body, nil
	}

	// stale許容期限を超えたら、このリクエストが同期で再生成する。すでに
	// バックグラウンド更新中なら、その完了を待つ。
	done, started := startTrendCacheRefresh()
	if started {
		return refreshTrendCache(done)
	}
	if done != nil {
		<-done
	}

	trendCache.RLock()
	body, err := trendCache.body, trendCache.err
	valid := len(body) > 0 && time.Now().Before(trendCache.staleUntil)
	trendCache.RUnlock()
	if valid {
		return body, nil
	}
	return nil, err
}

// trendCacheDurations は initialize からの経過で TTL を変える。
// 序盤は短くしてユーザーを増やし、終盤は長くして増加を抑える。
func trendCacheDurations() (ttl, maxAge time.Duration) {
	start := trendScheduleStart
	if start.IsZero() {
		return trendTTLMid, trendMaxAgeMid
	}
	elapsed := time.Since(start)
	switch {
	case elapsed < trendPhaseEarlyUntil:
		return trendTTLEarly, trendMaxAgeEarly
	case elapsed < trendPhaseMidUntil:
		return trendTTLMid, trendMaxAgeMid
	default:
		return trendTTLLate, trendMaxAgeLate
	}
}

func resetTrendSchedule() {
	trendScheduleStart = time.Now()
	trendCache.Lock()
	trendCache.body = nil
	trendCache.expiresAt = time.Time{}
	trendCache.staleUntil = time.Time{}
	trendCache.err = nil
	trendCache.Unlock()
}

// startTrendCacheRefreshは同時に1本だけキャッシュ更新を開始する。
// 呼び出し元の判定中にキャッシュが更新済みならnil, falseを返す。
func startTrendCacheRefresh() (chan struct{}, bool) {
	trendCache.Lock()
	defer trendCache.Unlock()

	if len(trendCache.body) > 0 && time.Now().Before(trendCache.expiresAt) {
		return nil, false
	}
	if trendCache.refreshing {
		return trendCache.done, false
	}

	trendCache.refreshing = true
	trendCache.done = make(chan struct{})
	return trendCache.done, true
}

func refreshTrendCache(done chan struct{}) ([]byte, error) {
	body, err := buildTrendJSON()

	ttl, maxAge := trendCacheDurations()
	trendCache.Lock()
	if err == nil {
		updatedAt := time.Now()
		trendCache.body = body
		trendCache.expiresAt = updatedAt.Add(ttl)
		trendCache.staleUntil = updatedAt.Add(maxAge)
	} else {
		log.Errorf("failed to refresh trend cache: %v", err)
	}
	trendCache.err = err
	trendCache.refreshing = false
	close(done)
	trendCache.Unlock()

	return body, err
}

func buildTrendJSON() ([]byte, error) {
	type trendRow struct {
		IsuID      int    `db:"isu_id"`
		JIAIsuUUID string `db:"jia_isu_uuid"`
		Character  string `db:"character"`
	}

	rows := []trendRow{}
	err := db.Select(&rows, `
		SELECT isu.id AS isu_id, isu.jia_isu_uuid, isu.character
		FROM isu`)
	if err != nil {
		return nil, fmt.Errorf("select trend rows: %w", err)
	}

	type trendItem struct {
		isuID     int
		character string
		timestamp int64
		condition string
	}
	items := make([]trendItem, 0, len(rows))
	for _, row := range rows {
		latest, ok := getCachedIsuLatestCondition(row.JIAIsuUUID)
		if !ok {
			continue
		}
		items = append(items, trendItem{
			isuID:     row.IsuID,
			character: row.Character,
			timestamp: latest.Timestamp,
			condition: latest.Condition,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].character != items[j].character {
			return items[i].character > items[j].character
		}
		return items[i].timestamp > items[j].timestamp
	})

	res := []*TrendResponse{}
	trendByCharacter := map[string]*TrendResponse{}
	for _, item := range items {
		trend, ok := trendByCharacter[item.character]
		if !ok {
			// nil slice は JSON で null になり、フロントの forEach が落ちる
			trend = &TrendResponse{
				Character: item.character,
				Info:      []*TrendCondition{},
				Warning:   []*TrendCondition{},
				Critical:  []*TrendCondition{},
			}
			trendByCharacter[item.character] = trend
			res = append(res, trend)
		}

		conditionLevel, err := calculateConditionLevel(item.condition)
		if err != nil {
			return nil, err
		}
		trendCondition := &TrendCondition{ID: item.isuID, Timestamp: item.timestamp}
		switch conditionLevel {
		case conditionLevelInfo:
			trend.Info = append(trend.Info, trendCondition)
		case conditionLevelWarning:
			trend.Warning = append(trend.Warning, trendCondition)
		case conditionLevelCritical:
			trend.Critical = append(trend.Critical, trendCondition)
		}
	}

	// character は DESC で集めたので ASC に戻す
	reverseTrendResponses(res)
	body, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("marshal trend response: %w", err)
	}

	return body, nil
}

func reverseTrendResponses(trends []*TrendResponse) {
	for left, right := 0, len(trends)-1; left < right; left, right = left+1, right-1 {
		trends[left], trends[right] = trends[right], trends[left]
	}
}
