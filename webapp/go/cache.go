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

func clearIsuExistenceCache() {
	isuExistenceCache.Lock()
	defer isuExistenceCache.Unlock()
	isuExistenceCache.values = make(map[string]bool)
}

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

// clearIsuLatestTimestampCache は各 ISU の仮想現在時刻キャッシュを捨てる（initialize 時）。
func clearIsuLatestTimestampCache() {
	isuLatestTimestampCache.Lock()
	isuLatestTimestampCache.values = make(map[string]int64)
	isuLatestTimestampCache.Unlock()
}

// getCachedIsuLatestTimestamp はその ISU の仮想現在時刻（最新 condition の unix）を返す。
func getCachedIsuLatestTimestamp(jiaIsuUUID string) (int64, bool) {
	isuLatestTimestampCache.RLock()
	timestamp, ok := isuLatestTimestampCache.values[jiaIsuUUID]
	isuLatestTimestampCache.RUnlock()
	return timestamp, ok
}

// setCachedIsuLatestTimestamp は仮想現在時刻を進める（より新しい timestamp のときだけ）。
func setCachedIsuLatestTimestamp(jiaIsuUUID string, timestamp int64) {
	isuLatestTimestampCache.Lock()
	if cachedTimestamp, ok := isuLatestTimestampCache.values[jiaIsuUUID]; !ok || timestamp > cachedTimestamp {
		isuLatestTimestampCache.values[jiaIsuUUID] = timestamp
	}
	isuLatestTimestampCache.Unlock()
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

// getCachedGraphEntry は ISU×日 のグラフキャッシュを取る。
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
	return entry, true
}

// setCachedGraph は日毎グラフを保存する。
// sealedThrough 未満の時間帯は確定済み（それ以降はまだ開いている / 未作成）。
func setCachedGraph(jiaIsuUUID string, graphDate time.Time, response []GraphResponse, sealedThrough time.Time) {
	dayUnix := graphCacheDay(graphDate).Unix()
	graphCache.Lock()
	byDay := graphCache.values[jiaIsuUUID]
	if byDay == nil {
		byDay = make(map[int64]graphCacheEntry)
		graphCache.values[jiaIsuUUID] = byDay
	}
	byDay[dayUnix] = graphCacheEntry{
		response:      response,
		sealedThrough: sealedThrough.Unix(),
	}
	graphCache.Unlock()
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
