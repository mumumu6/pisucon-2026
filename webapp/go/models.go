package main

import (
	"crypto/ecdsa"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	jsoniter "github.com/json-iterator/go"
)

var jsonFast = jsoniter.ConfigFastest

const (
	sessionName                 = "isucondition_go"
	conditionLimit              = 20
	frontendContentsPath        = "../public"
	jiaJWTSigningKeyPath        = "../ec256-public.pem"
	defaultIconFilePath         = "../NoImage.jpg"
	defaultJIAServiceURL        = "http://localhost:5000"
	mysqlErrNumDuplicateEntry   = 1062
	conditionLevelInfo          = "info"
	conditionLevelWarning       = "warning"
	conditionLevelCritical      = "critical"
	scoreConditionLevelInfo     = 3
	scoreConditionLevelWarning  = 2
	scoreConditionLevelCritical = 1
	trendCacheTTL               = 400 * time.Millisecond
	trendCacheMaxAge            = 900 * time.Millisecond
	conditionBatchMaxRequests   = 128
	conditionBatchWait          = 5 * time.Millisecond
	conditionWriterCount = 4
	graphCacheWarmWorkers = 8
)

var graphCacheLocation = time.FixedZone("Asia/Tokyo", 9*60*60)

var (
	db                  *sqlx.DB
	sessionKey          []byte
	mySQLConnectionData *MySQLConnectionEnv

	jiaJWTSigningKey *ecdsa.PublicKey

	postIsuConditionTargetBaseURL string // JIAへのactivate時に登録する，ISUがconditionを送る先のURL

	trendCache = struct {
		sync.RWMutex
		body       []byte
		expiresAt  time.Time
		staleUntil time.Time
		refreshing bool
		done       chan struct{}
		err        error
	}{}

	isuExistenceCache = struct {
		sync.RWMutex
		values map[string]bool
	}{values: make(map[string]bool)}

	isuOwnerCache = struct {
		sync.RWMutex
		values map[string]string
	}{values: make(map[string]string)}

	isuMetadataCache = struct {
		sync.RWMutex
		values map[string]isuMetadataCacheEntry
	}{values: make(map[string]isuMetadataCacheEntry)}

	isuLatestTimestampCache = struct {
		sync.RWMutex
		values map[string]int64
	}{values: make(map[string]int64)}

	isuIconCache = struct {
		sync.RWMutex
		values map[string]isuIconCacheEntry
	}{values: make(map[string]isuIconCacheEntry)}

	// ISU × グラフ日(JST 0時 Unix) → レスポンス。更新は該当時間帯だけ差し替え。
	graphCache = struct {
		sync.RWMutex
		values map[string]map[int64]graphCacheEntry
	}{values: make(map[string]map[int64]graphCacheEntry)}

	conditionWriteQueues []chan conditionWriteRequest
)

type graphCacheEntry struct {
	response []GraphResponse
	// sealedThrough: この Unix 時刻未満の時間帯は確定済み。開いている時間帯は含めない。
	sealedThrough int64
}

type isuIconCacheEntry struct {
	jiaUserID string
	image     []byte
}

type isuMetadataCacheEntry struct {
	jiaUserID string
	name      string
}

type conditionWriteRequest struct {
	jiaIsuUUID string
	conditions []PostIsuConditionRequest
}

type Isu struct {
	ID              int            `db:"id" json:"id"`
	JIAIsuUUID      string         `db:"jia_isu_uuid" json:"jia_isu_uuid"`
	Name            string         `db:"name" json:"name"`
	Image           []byte         `db:"image" json:"-"`
	Character       string         `db:"character" json:"character"`
	LatestTimestamp sql.NullTime   `db:"latest_timestamp" json:"-"`
	LatestIsSitting sql.NullBool   `db:"latest_is_sitting" json:"-"`
	LatestCondition sql.NullString `db:"latest_condition" json:"-"`
	LatestMessage   sql.NullString `db:"latest_message" json:"-"`
	JIAUserID       string         `db:"jia_user_id" json:"-"`
	CreatedAt       time.Time      `db:"created_at" json:"-"`
	UpdatedAt       time.Time      `db:"updated_at" json:"-"`
}

type IsuFromJIA struct {
	Character string `json:"character"`
}

type GetIsuListResponse struct {
	ID                 int                      `json:"id"`
	JIAIsuUUID         string                   `json:"jia_isu_uuid"`
	Name               string                   `json:"name"`
	Character          string                   `json:"character"`
	LatestIsuCondition *GetIsuConditionResponse `json:"latest_isu_condition"`
}

type IsuCondition struct {
	ID         int       `db:"id"`
	JIAIsuUUID string    `db:"jia_isu_uuid"`
	Timestamp  time.Time `db:"timestamp"`
	IsSitting  bool      `db:"is_sitting"`
	Condition  string    `db:"condition"`
	Message    string    `db:"message"`
	CreatedAt  time.Time `db:"created_at"`
}

// GET /api/condition 用。必要列だけスキャンする。
type isuConditionListRow struct {
	Timestamp time.Time `db:"timestamp"`
	IsSitting bool      `db:"is_sitting"`
	Condition string    `db:"condition"`
	Message   string    `db:"message"`
}

// グラフ生成用。message は不要。
type isuConditionGraphRow struct {
	Timestamp time.Time `db:"timestamp"`
	IsSitting bool      `db:"is_sitting"`
	Condition string    `db:"condition"`
}

// condition 文字列は 8 通りしかないので、グラフ集計用メタデータを事前計算する。
type graphConditionMeta struct {
	rawScore     int
	isBroken     bool
	isDirty      bool
	isOverweight bool
}

var graphConditionByString map[string]graphConditionMeta

func init() {
	graphConditionByString = make(map[string]graphConditionMeta, 8)
	for _, isDirty := range []bool{false, true} {
		for _, isOverweight := range []bool{false, true} {
			for _, isBroken := range []bool{false, true} {
				condition := fmt.Sprintf(
					"is_dirty=%t,is_overweight=%t,is_broken=%t",
					isDirty, isOverweight, isBroken,
				)
				bad := 0
				if isDirty {
					bad++
				}
				if isOverweight {
					bad++
				}
				if isBroken {
					bad++
				}
				rawScore := scoreConditionLevelInfo
				if bad >= 3 {
					rawScore = scoreConditionLevelCritical
				} else if bad >= 1 {
					rawScore = scoreConditionLevelWarning
				}
				graphConditionByString[condition] = graphConditionMeta{
					rawScore:     rawScore,
					isBroken:     isBroken,
					isDirty:      isDirty,
					isOverweight: isOverweight,
				}
			}
		}
	}
}

type MySQLConnectionEnv struct {
	Host     string
	Port     string
	User     string
	DBName   string
	Password string
}

type InitializeRequest struct {
	JIAServiceURL string `json:"jia_service_url"`
}

type InitializeResponse struct {
	Language string `json:"language"`
}

type GetMeResponse struct {
	JIAUserID string `json:"jia_user_id"`
}

type GraphResponse struct {
	StartAt             int64           `json:"start_at"`
	EndAt               int64           `json:"end_at"`
	Data                *GraphDataPoint `json:"data"`
	ConditionTimestamps []int64         `json:"condition_timestamps"`
}

type GraphDataPoint struct {
	Score      int                  `json:"score"`
	Percentage ConditionsPercentage `json:"percentage"`
}

type ConditionsPercentage struct {
	Sitting      int `json:"sitting"`
	IsBroken     int `json:"is_broken"`
	IsDirty      int `json:"is_dirty"`
	IsOverweight int `json:"is_overweight"`
}

type GraphDataPointWithInfo struct {
	JIAIsuUUID          string
	StartAt             time.Time
	Data                GraphDataPoint
	ConditionTimestamps []int64
}

type GetIsuConditionResponse struct {
	JIAIsuUUID     string `json:"jia_isu_uuid"`
	IsuName        string `json:"isu_name"`
	Timestamp      int64  `json:"timestamp"`
	IsSitting      bool   `json:"is_sitting"`
	Condition      string `json:"condition"`
	ConditionLevel string `json:"condition_level"`
	Message        string `json:"message"`
}

type TrendResponse struct {
	Character string            `json:"character"`
	Info      []*TrendCondition `json:"info"`
	Warning   []*TrendCondition `json:"warning"`
	Critical  []*TrendCondition `json:"critical"`
}

type TrendCondition struct {
	ID        int   `json:"isu_id"`
	Timestamp int64 `json:"timestamp"`
}

type PostIsuConditionRequest struct {
	IsSitting bool   `json:"is_sitting"`
	Condition string `json:"condition"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

type JIAServiceRequest struct {
	TargetBaseURL string `json:"target_base_url"`
	IsuUUID       string `json:"isu_uuid"`
}
