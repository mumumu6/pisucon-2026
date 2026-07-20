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
	isuExistenceCache.values = make(map[string]bool)
	isuExistenceCache.Unlock()
	isuActivateInFlight.Range(func(key, _ interface{}) bool {
		isuActivateInFlight.Delete(key)
		return true
	})
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
	isuListByUser.Lock()
	isuListByUser.order = make(map[string][]string)
	isuListByUser.listJSON = make(map[string][]byte)
	isuListByUser.Unlock()
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

func getCachedIsuRecord(jiaIsuUUID, jiaUserID string) (isuMetadataCacheEntry, bool) {
	isuMetadataCache.RLock()
	entry, ok := isuMetadataCache.values[jiaIsuUUID]
	isuMetadataCache.RUnlock()
	if !ok || entry.jiaUserID != jiaUserID || entry.id == 0 {
		return isuMetadataCacheEntry{}, false
	}
	return entry, true
}

func setCachedIsuMetadata(jiaIsuUUID, jiaUserID string, id int, name, character string) {
	var jsonBody []byte
	if id > 0 {
		if body, err := jsonFast.Marshal(Isu{
			ID:         id,
			JIAIsuUUID: jiaIsuUUID,
			Name:       name,
			Character:  character,
		}); err == nil {
			jsonBody = body
		}
	}
	isuMetadataCache.Lock()
	isuMetadataCache.values[jiaIsuUUID] = isuMetadataCacheEntry{
		jiaUserID: jiaUserID,
		id:        id,
		name:      name,
		character: character,
		jsonBody:  jsonBody,
	}
	isuMetadataCache.Unlock()
}

func getCachedIsuListOrder(jiaUserID string) ([]string, bool) {
	isuListByUser.RLock()
	order, ok := isuListByUser.order[jiaUserID]
	isuListByUser.RUnlock()
	if !ok {
		return nil, false
	}
	out := make([]string, len(order))
	copy(out, order)
	return out, true
}

func getCachedIsuListJSON(jiaUserID string) ([]byte, bool) {
	isuListByUser.RLock()
	body, ok := isuListByUser.listJSON[jiaUserID]
	isuListByUser.RUnlock()
	if !ok || len(body) == 0 {
		return nil, false
	}
	return body, true
}

func setCachedIsuListJSON(jiaUserID string, body []byte) {
	isuListByUser.Lock()
	isuListByUser.listJSON[jiaUserID] = body
	isuListByUser.Unlock()
}

func invalidateIsuListJSON(jiaUserID string) {
	isuListByUser.Lock()
	delete(isuListByUser.listJSON, jiaUserID)
	isuListByUser.Unlock()
}

func invalidateIsuListJSONByIsu(jiaIsuUUID string) {
	owner, ok := getCachedIsuOwner(jiaIsuUUID)
	if !ok {
		return
	}
	invalidateIsuListJSON(owner)
}

// prependIsuListOrder は新規登録 ISU を一覧先頭（id DESC）に追加し、一覧 JSON を捨てる。
func prependIsuListOrder(jiaUserID, jiaIsuUUID string) {
	isuListByUser.Lock()
	order := isuListByUser.order[jiaUserID]
	next := make([]string, 0, len(order)+1)
	next = append(next, jiaIsuUUID)
	next = append(next, order...)
	isuListByUser.order[jiaUserID] = next
	delete(isuListByUser.listJSON, jiaUserID)
	isuListByUser.Unlock()
}

func setIsuListOrder(jiaUserID string, order []string) {
	copied := make([]string, len(order))
	copy(copied, order)
	isuListByUser.Lock()
	isuListByUser.order[jiaUserID] = copied
	delete(isuListByUser.listJSON, jiaUserID)
	isuListByUser.Unlock()
}

// warmIsuMetadataCache は initialize 後に一覧・詳細用メタデータを載せる。
func warmIsuMetadataCache() error {
	type row struct {
		ID         int    `db:"id"`
		JIAIsuUUID string `db:"jia_isu_uuid"`
		Name       string `db:"name"`
		Character  string `db:"character"`
		JIAUserID  string `db:"jia_user_id"`
	}
	rows := []row{}
	if err := db.Select(&rows,
		"SELECT `id`, `jia_isu_uuid`, `name`, `character`, `jia_user_id` FROM `isu` ORDER BY `id` DESC"); err != nil {
		return err
	}
	orders := make(map[string][]string, 256)
	for _, r := range rows {
		setCachedIsuExistence(r.JIAIsuUUID, true)
		setCachedIsuOwner(r.JIAIsuUUID, r.JIAUserID)
		setCachedIsuMetadata(r.JIAIsuUUID, r.JIAUserID, r.ID, r.Name, r.Character)
		orders[r.JIAUserID] = append(orders[r.JIAUserID], r.JIAIsuUUID)
	}
	isuListByUser.Lock()
	isuListByUser.order = orders
	isuListByUser.listJSON = make(map[string][]byte)
	isuListByUser.Unlock()
	return nil
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
	if cached, ok := isuLatestConditionCache.values[jiaIsuUUID]; ok && timestamp < cached.Timestamp {
		isuLatestConditionCache.Unlock()
		return
	}
	isuLatestConditionCache.values[jiaIsuUUID] = isuLatestConditionEntry{
		Timestamp: timestamp,
		IsSitting: isSitting,
		Condition: condition,
		Message:   message,
	}
	isuLatestConditionCache.Unlock()
	// 一覧 JSON は latest を含むので捨てる
	invalidateIsuListJSONByIsu(jiaIsuUUID)
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

// getCachedGraphJSON は全日確定済みグラフの JSON を返す（不変なので共有してよい）。
func getCachedGraphJSON(jiaIsuUUID string, graphDate time.Time) ([]byte, bool) {
	dayUnix := graphCacheDay(graphDate).Unix()
	graphCache.RLock()
	byDay, ok := graphCache.values[jiaIsuUUID]
	if !ok {
		graphCache.RUnlock()
		return nil, false
	}
	entry, ok := byDay[dayUnix]
	body := entry.jsonBody
	graphCache.RUnlock()
	if !ok || len(body) == 0 {
		return nil, false
	}
	return body, true
}

// getCachedGraphEntry は ISU×日 のグラフキャッシュを取る。
// 返す response は浅いコピー（スロット差し替え用）。Data/Timestamps は seal 済み不変前提で共有する。
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
		response:      cloneGraphDayShallow(entry.response),
		sealedThrough: entry.sealedThrough,
		jsonBody:      entry.jsonBody,
	}, true
}

// setCachedGraph は日毎グラフを保存する。
// sealedThrough 未満の時間帯は確定済み（それ以降はまだ開いている / 未作成）。
// 呼び出し元が同じスライスを後から更新してもキャッシュが壊れないよう、複製して保存する。
func setCachedGraph(jiaIsuUUID string, graphDate time.Time, response []GraphResponse, sealedThrough time.Time) {
	dayUnix := graphCacheDay(graphDate).Unix()
	dayEndUnix := graphDate.Add(24 * time.Hour).Unix()
	cloned := cloneGraphDay(response)
	var jsonBody []byte
	if sealedThrough.Unix() >= dayEndUnix {
		if body, err := jsonFast.Marshal(cloned); err == nil {
			jsonBody = body
		}
	}
	graphCache.Lock()
	byDay := graphCache.values[jiaIsuUUID]
	if byDay == nil {
		byDay = make(map[int64]graphCacheEntry)
		graphCache.values[jiaIsuUUID] = byDay
	}
	byDay[dayUnix] = graphCacheEntry{
		response:      cloned,
		sealedThrough: sealedThrough.Unix(),
		jsonBody:      jsonBody,
	}
	graphCache.Unlock()
}

// cloneGraphDayShallow は 24 スロットのヘッダだけコピーする。
// スロット全体の差し替えだけする前提で、Data/Timestamps ポインタは共有する。
func cloneGraphDayShallow(src []GraphResponse) []GraphResponse {
	if src == nil {
		return nil
	}
	dst := make([]GraphResponse, len(src))
	copy(dst, src)
	return dst
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
