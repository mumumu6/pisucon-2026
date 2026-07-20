package main

import "time"

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

func clearIsuLatestTimestampCache() {
	isuLatestTimestampCache.Lock()
	isuLatestTimestampCache.values = make(map[string]int64)
	isuLatestTimestampCache.Unlock()
}

func getCachedIsuLatestTimestamp(jiaIsuUUID string) (int64, bool) {
	isuLatestTimestampCache.RLock()
	timestamp, ok := isuLatestTimestampCache.values[jiaIsuUUID]
	isuLatestTimestampCache.RUnlock()
	return timestamp, ok
}

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

func clearGraphCache() {
	graphCache.Lock()
	graphCache.values = make(map[string]map[int64]graphCacheEntry)
	graphCache.Unlock()
}

func invalidateGraphCache(jiaIsuUUID string) {
	graphCache.Lock()
	delete(graphCache.values, jiaIsuUUID)
	graphCache.Unlock()
}

func getCachedGraph(jiaIsuUUID string, graphDate time.Time) ([]byte, bool) {
	graphCache.RLock()
	byDay, ok := graphCache.values[jiaIsuUUID]
	if !ok {
		graphCache.RUnlock()
		return nil, false
	}
	entry, ok := byDay[graphDate.Unix()]
	graphCache.RUnlock()
	if !ok {
		return nil, false
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.body, true
}

func setCachedGraph(jiaIsuUUID string, graphDate time.Time, body []byte) {
	dayEnd := graphDate.Add(24 * time.Hour)
	var expiresAt time.Time
	if dayEnd.After(time.Now()) {
		// 当日（まだ書き込みが入る窓）は短命
		expiresAt = time.Now().Add(graphCacheTodayTTL)
	}

	graphCache.Lock()
	byDay := graphCache.values[jiaIsuUUID]
	if byDay == nil {
		byDay = make(map[int64]graphCacheEntry)
		graphCache.values[jiaIsuUUID] = byDay
	}
	byDay[graphDate.Unix()] = graphCacheEntry{body: body, expiresAt: expiresAt}
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
