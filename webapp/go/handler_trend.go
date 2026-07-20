package main

import (
	"encoding/json"
	"fmt"
	"net/http"
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

	// fresh期限後から生成後800msまではstaleを即返し、裏で更新する。
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

	trendCache.Lock()
	if err == nil {
		updatedAt := time.Now()
		trendCache.body = body
		trendCache.expiresAt = updatedAt.Add(trendCacheTTL)
		trendCache.staleUntil = updatedAt.Add(trendCacheMaxAge)
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
		IsuID           int       `db:"isu_id"`
		Character       string    `db:"character"`
		LatestTimestamp time.Time `db:"latest_timestamp"`
		LatestCondition string    `db:"latest_condition"`
	}

	rows := []trendRow{}
	err := db.Select(&rows, `
		SELECT isu.id AS isu_id, isu.character, isu.latest_timestamp, isu.latest_condition
		FROM isu
		WHERE isu.latest_timestamp IS NOT NULL
		ORDER BY isu.character DESC, isu.latest_timestamp DESC`)
	if err != nil {
		return nil, fmt.Errorf("select trend rows: %w", err)
	}

	res := []*TrendResponse{}
	trendByCharacter := map[string]*TrendResponse{}
	for _, row := range rows {
		trend, ok := trendByCharacter[row.Character]
		if !ok {
			// nil slice は JSON で null になり、フロントの forEach が落ちる
			trend = &TrendResponse{
				Character: row.Character,
				Info:      []*TrendCondition{},
				Warning:   []*TrendCondition{},
				Critical:  []*TrendCondition{},
			}
			trendByCharacter[row.Character] = trend
			res = append(res, trend)
		}

		conditionLevel, err := calculateConditionLevel(row.LatestCondition)
		if err != nil {
			return nil, err
		}
		trendCondition := &TrendCondition{ID: row.IsuID, Timestamp: row.LatestTimestamp.Unix()}
		switch conditionLevel {
		case conditionLevelInfo:
			trend.Info = append(trend.Info, trendCondition)
		case conditionLevelWarning:
			trend.Warning = append(trend.Warning, trendCondition)
		case conditionLevelCritical:
			trend.Critical = append(trend.Critical, trendCondition)
		}
	}

	// 複合indexを逆順走査することで各condition配列はtimestamp DESC順になる。
	// characterの並びだけ元のASC順へ戻す。
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
