package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// 大本の condition キャッシュ。GET condition / グラフ集計の元データ。
var conditionStore = struct {
	sync.RWMutex
	byIsu map[string]*isuConditionMem
}{byIsu: make(map[string]*isuConditionMem)}

type storedCondition struct {
	Timestamp int64
	IsSitting bool
	Condition string
	Message   string
}

// 1 ISU 分。timestamp 昇順（ISU 単位で単調増加が保証されている）。
type isuConditionMem struct {
	sync.RWMutex
	items []storedCondition
}

func clearConditionStore() {
	conditionStore.Lock()
	conditionStore.byIsu = make(map[string]*isuConditionMem)
	conditionStore.Unlock()
}

func getOrCreateConditionMem(jiaIsuUUID string) *isuConditionMem {
	conditionStore.RLock()
	mem := conditionStore.byIsu[jiaIsuUUID]
	conditionStore.RUnlock()
	if mem != nil {
		return mem
	}
	conditionStore.Lock()
	mem = conditionStore.byIsu[jiaIsuUUID]
	if mem == nil {
		mem = &isuConditionMem{items: make([]storedCondition, 0, 64)}
		conditionStore.byIsu[jiaIsuUUID] = mem
	}
	conditionStore.Unlock()
	return mem
}

// warmConditionStore は種データの全 condition をメモリに載せる。
func warmConditionStore() error {
	rows, err := db.Queryx(
		"SELECT `jia_isu_uuid`, `timestamp`, `is_sitting`, `condition`, `message` FROM `isu_condition` ORDER BY `jia_isu_uuid`, `timestamp`",
	)
	if err != nil {
		return fmt.Errorf("select conditions for warm: %w", err)
	}
	defer rows.Close()

	byIsu := make(map[string]*isuConditionMem, 1024)
	var currentUUID string
	var mem *isuConditionMem
	for rows.Next() {
		var uuid string
		var ts time.Time
		var sitting bool
		var condition, message string
		if err := rows.Scan(&uuid, &ts, &sitting, &condition, &message); err != nil {
			return fmt.Errorf("scan condition warm: %w", err)
		}
		if uuid != currentUUID {
			currentUUID = uuid
			mem = &isuConditionMem{items: make([]storedCondition, 0, 256)}
			byIsu[uuid] = mem
		}
		mem.items = append(mem.items, storedCondition{
			Timestamp: ts.Unix(),
			IsSitting: sitting,
			Condition: condition,
			Message:   message,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	conditionStore.Lock()
	conditionStore.byIsu = byIsu
	conditionStore.Unlock()
	return nil
}

// appendIsuConditions は timestamp 昇順を保って追加する。
// HTTP 到着順は単調増加を保証しないため、末尾追加ではなく挿入する。
func appendIsuConditions(jiaIsuUUID string, conditions []PostIsuConditionRequest) {
	if len(conditions) == 0 {
		return
	}
	mem := getOrCreateConditionMem(jiaIsuUUID)
	mem.Lock()
	defer mem.Unlock()
	for _, c := range conditions {
		item := storedCondition{
			Timestamp: c.Timestamp,
			IsSitting: c.IsSitting,
			Condition: c.Condition,
			Message:   c.Message,
		}
		n := len(mem.items)
		if n == 0 || item.Timestamp > mem.items[n-1].Timestamp {
			mem.items = append(mem.items, item)
			continue
		}
		i := sort.Search(n, func(i int) bool {
			return mem.items[i].Timestamp >= item.Timestamp
		})
		if i < n && mem.items[i].Timestamp == item.Timestamp {
			continue // 重複は無視（DB の INSERT IGNORE と同様）
		}
		mem.items = append(mem.items, storedCondition{})
		copy(mem.items[i+1:], mem.items[i:])
		mem.items[i] = item
	}
}

// getIsuConditionsFromMem は GET /api/condition 用（timestamp DESC, limit）。
func getIsuConditionsFromMem(
	jiaIsuUUID string,
	endTime time.Time,
	conditionLevel map[string]interface{},
	startTime time.Time,
	limit int,
	isuName string,
) []GetIsuConditionResponse {
	_, levelByCondition := conditionStringsForLevels(conditionLevel)
	if len(levelByCondition) == 0 {
		return []GetIsuConditionResponse{}
	}

	conditionStore.RLock()
	mem := conditionStore.byIsu[jiaIsuUUID]
	conditionStore.RUnlock()
	if mem == nil {
		return []GetIsuConditionResponse{}
	}

	endUnix := endTime.Unix()
	var startUnix int64
	hasStart := !startTime.IsZero()
	if hasStart {
		startUnix = startTime.Unix()
	}

	mem.RLock()
	defer mem.RUnlock()
	items := mem.items
	// end_time 未満の右端を二分探索
	hi := sort.Search(len(items), func(i int) bool {
		return items[i].Timestamp >= endUnix
	})
	res := make([]GetIsuConditionResponse, 0, limit)
	for i := hi - 1; i >= 0 && len(res) < limit; i-- {
		row := items[i]
		if hasStart && row.Timestamp < startUnix {
			break
		}
		cLevel, ok := levelByCondition[row.Condition]
		if !ok {
			continue
		}
		res = append(res, GetIsuConditionResponse{
			JIAIsuUUID:     jiaIsuUUID,
			IsuName:        isuName,
			Timestamp:      row.Timestamp,
			IsSitting:      row.IsSitting,
			Condition:      row.Condition,
			ConditionLevel: cLevel,
			Message:        row.Message,
		})
	}
	return res
}

// conditionsInHourFromMem は [hourStart, hourEnd) を昇順で返す。
func conditionsInHourFromMem(jiaIsuUUID string, hourStart, hourEnd time.Time) []isuConditionGraphRow {
	conditionStore.RLock()
	mem := conditionStore.byIsu[jiaIsuUUID]
	conditionStore.RUnlock()
	if mem == nil {
		return nil
	}

	startUnix := hourStart.Unix()
	endUnix := hourEnd.Unix()

	mem.RLock()
	defer mem.RUnlock()
	items := mem.items
	lo := sort.Search(len(items), func(i int) bool {
		return items[i].Timestamp >= startUnix
	})
	hi := sort.Search(len(items), func(i int) bool {
		return items[i].Timestamp >= endUnix
	})
	if lo >= hi {
		return nil
	}
	rows := make([]isuConditionGraphRow, 0, hi-lo)
	for i := lo; i < hi; i++ {
		rows = append(rows, isuConditionGraphRow{
			Timestamp: time.Unix(items[i].Timestamp, 0),
			IsSitting: items[i].IsSitting,
			Condition: items[i].Condition,
		})
	}
	return rows
}
