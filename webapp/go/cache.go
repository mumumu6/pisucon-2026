package main

import "time"

// graphCacheDay は JST のその日 0:00（グラフの datetime キー）。
func graphCacheDay(ts time.Time) time.Time {
	t := ts.In(graphCacheLocation)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, graphCacheLocation)
}

// graphCacheHour は JST でその時刻が属する時間帯の開始（例: 14:15 → 14:00）。
func graphCacheHour(ts time.Time) time.Time {
	t := ts.In(graphCacheLocation)
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, graphCacheLocation)
}

ｃ

func getCachedIsuExistence(jiaIsuUUID string) (bool, bool) {
	isuExistenceCache.RLock()
	exists, cached := isuExistenceCache.values[jiaIsuUUID]
	isuExistenceCache.RUnlock()
	return exists, cached
}

func setCachedIsuExistence(jiaIsuUUID string, exists bool) {
	isuExistenceCache.Lock()
	isuExistenceCache.values[jiaIsuUUID] = exists
	isuExistenceCache.Unlock()
}

func clearIsuOwnerCache() {
	isuOwnerCache.Lock()
	isuOwnerCache.values = make(map[string]string)
	isuOwnerCache.Unlock()
}

func getCachedIsuOwner(jiaIsuUUID string) (string, bool) {
	isuOwnerCache.RLock()
	jiaUserID, ok := isuOwnerCache.values[jiaIsuUUID]
	isuOwnerCache.RUnlock()
	return jiaUserID, ok
}

func setCachedIsuOwner(jiaIsuUUID, jiaUserID string) {
	isuOwnerCache.Lock()
	isuOwnerCache.values[jiaIsuUUID] = jiaUserID
	isuOwnerCache.Unlock()
}

func clearIsuMetadataCache() {
	isuMetadataCache.Lock()
	isuMetadataCache.values = make(map[string]isuMetadataCacheEntry)
	isuMetadataCache.Unlock()
}

func getCachedIsuMetadata(jiaIsuUUID, jiaUserID string) (string, bool) {
	isuMetadataCache.RLock()
	entry, ok := isuMetadataCache.values[jiaIsuUUID]
	isuMetadataCache.RUnlock()
	if !ok || entry.jiaUserID != jiaUserID {
		return "", false
	}
	return entry.name, true
}

func setCachedIsuMetadata(jiaIsuUUID, jiaUserID, name string) {
	isuMetadataCache.Lock()
	isuMetadataCache.values[jiaIsuUUID] = isuMetadataCacheEntry{jiaUserID: jiaUserID, name: name}
	isuMetadataCache.Unlock()
}

// clearIsuLatestConditionCache は各 ISU の最新 condition キャッシュを捨てる（initialize 時）。
func clearIsuLatestConditionCache() {
	isuLatestConditionCache.Lock()
	isuLatestConditionCache.values = make(map[string]isuLatestConditionEntry)
	isuLatestConditionCache.Unlock()
}

// getCachedIsuLatestTimestamp はその ISU の仮想現在時刻（最新 condition の unix）を返す。
func getCachedIsuLatestTimestamp(jiaIsuUUID string) (int64, bool) {
	isuLatestConditionCache.RLock()
	entry, ok := isuLatestConditionCache.values[jiaIsuUUID]
	isuLatestConditionCache.RUnlock()
	if !ok {
		return 0, false
	}
	return entry.Timestamp, true
}

// getCachedIsuLatestCondition は一覧・trend 用の最新 condition を返す。
func getCachedIsuLatestCondition(jiaIsuUUID string) (isuLatestConditionEntry, bool) {
	isuLatestConditionCache.RLock()
	entry, ok := isuLatestConditionCache.values[jiaIsuUUID]
	isuLatestConditionCache.RUnlock()
	return entry, ok
}

// setCachedIsuLatestCondition は最新 condition をメモリに載せる（より新しいときだけ）。
func setCachedIsuLatestCondition(jiaIsuUUID string, timestamp int64, isSitting bool, condition, message string) {
	isuLatestConditionCache.Lock()
	defer isuLatestConditionCache.Unlock()
	if cached, ok := isuLatestConditionCache.values[jiaIsuUUID]; ok && timestamp < cached.Timestamp {
		return
	}
	isuLatestConditionCache.values[jiaIsuUUID] = isuLatestConditionEntry{
		Timestamp: timestamp,
		IsSitting: isSitting,
		Condition: condition,
		Message:   message,
	}
}

func clearIsuIconCache() {
	isuIconCache.Lock()
	isuIconCache.values = make(map[string]isuIconCacheEntry)
	isuIconCache.Unlock()
}

// clearGraphCache は日毎グラフキャッシュを全部捨てる（initialize 時）。
func clearGraphCache() {
	graphCache.Lock()
	graphCache.values = make(map[string]map[int64]graphCacheEntry)
	graphCache.Unlock()
}

// getCachedGraphEntry は ISU×日 のグラフキャッシュを取る（呼び出し側で安全に触れるよう複製を返す）。
func getCachedGraphEntry(jiaIsuUUID string, graphDate time.Time) (graphCacheEntry, bool) {
	dayUnix := graphCacheDay(graphDate).Unix()
	graphCache.RLock()
	byDay, ok := graphCache.values[jiaIsuUUID]
	if !ok {
		graphCache.RUnlock()
		return graphCacheEntry{}, false
	}
	entry, ok := byDay[dayUnix]
	graphCache.RUnlock()
	if !ok || len(entry.response) != 24 {
		return graphCacheEntry{}, false
	}
	return graphCacheEntry{
		response:      cloneGraphDay(entry.response),
		sealedThrough: entry.sealedThrough,
	}, true
}

// setCachedGraph は日毎グラフを保存する。
// sealedThrough 未満の時間帯は確定済み（それ以降はまだ開いている / 未作成）。
// 呼び出し元が同じスライスを後から更新してもキャッシュが壊れないよう、複製して保存する。
func setCachedGraph(jiaIsuUUID string, graphDate time.Time, response []GraphResponse, sealedThrough time.Time) {
	dayUnix := graphCacheDay(graphDate).Unix()
	cloned := cloneGraphDay(response)
	graphCache.Lock()
	byDay := graphCache.values[jiaIsuUUID]
	if byDay == nil {
		byDay = make(map[int64]graphCacheEntry)
		graphCache.values[jiaIsuUUID] = byDay
	}
	byDay[dayUnix] = graphCacheEntry{
		response:      cloned,
		sealedThrough: sealedThrough.Unix(),
	}
	graphCache.Unlock()
}

// cloneGraphDay はグラフ1日分をディープコピーする。
// ConditionTimestamps / Data を共有したままだと、キャッシュ更新中に
// start_at と timestamps が食い違ってベンチに怒られる。
func cloneGraphDay(src []GraphResponse) []GraphResponse {
	if src == nil {
		return nil
	}
	dst := make([]GraphResponse, len(src))
	for i := range src {
		dst[i] = src[i]
		if src[i].ConditionTimestamps != nil {
			dst[i].ConditionTimestamps = append([]int64(nil), src[i].ConditionTimestamps...)
		}
		if src[i].Data != nil {
			data := *src[i].Data
			dst[i].Data = &data
		}
	}
	return dst
}

func getCachedIsuIcon(jiaIsuUUID, jiaUserID string) ([]byte, bool) {
	isuIconCache.RLock()
	entry, ok := isuIconCache.values[jiaIsuUUID]
	isuIconCache.RUnlock()
	if !ok || entry.jiaUserID != jiaUserID {
		return nil, false
	}
	return entry.image, true
}

func setCachedIsuIcon(jiaIsuUUID, jiaUserID string, image []byte) {
	isuIconCache.Lock()
	isuIconCache.values[jiaIsuUUID] = isuIconCacheEntry{jiaUserID: jiaUserID, image: image}
	isuIconCache.Unlock()
}

// setCachedIsuIconAsync keeps cache population off the icon response path.
func setCachedIsuIconAsync(jiaIsuUUID, jiaUserID string, image []byte) {
	go func() {
		isuIconCache.Lock()
		defer isuIconCache.Unlock()
		isuIconCache.values[jiaIsuUUID] = isuIconCacheEntry{jiaUserID: jiaUserID, image: image}
	}()
}

// POST /initialize
