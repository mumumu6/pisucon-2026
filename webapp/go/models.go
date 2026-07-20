package main

import (
	"crypto/ecdsa"
	"database/sql"
	"fmt"
	"net/http"
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
	// trend TTL 配分（initialize 起点）
	// 序盤は触らず、長いTTLの帯を早く始めて後半を厚くする。
	// 0-5s / 5-10s / 10-20s / 20-35s / 35s-
	trendTTL0                   = 2500 * time.Millisecond
	trendMaxAge0                = 3000 * time.Millisecond
	trendTTL1                   = 2500 * time.Millisecond
	trendMaxAge1                = 3000 * time.Millisecond
	trendTTL2                   = 3800 * time.Millisecond
	trendMaxAge2                = 4000 * time.Millisecond
	trendTTL3                   = 5500 * time.Millisecond
	trendMaxAge3                = 6000 * time.Millisecond
	trendTTL4                   = 7000 * time.Millisecond
	trendMaxAge4                = 8000 * time.Millisecond
	trendPhaseUntil0            = 5 * time.Second
	trendPhaseUntil1            = 10 * time.Second
	trendPhaseUntil2            = 20 * time.Second
	trendPhaseUntil3            = 35 * time.Second
	conditionBatchMaxRequests   = 512
	conditionBatchWait          = 1 * time.Millisecond
	conditionWriterCount        = 128
	conditionWriteQueueSize     = 4096
	graphCacheWarmWorkers       = 8
)

var graphCacheLocation = time.FixedZone("Asia/Tokyo", 9*60*60)

var (
	db                  *sqlx.DB
	sessionKey          []byte
	mySQLConnectionData *MySQLConnectionEnv

	jiaJWTSigningKey *ecdsa.PublicKey

	postIsuConditionTargetBaseURL string // JIAへのactivate時に登録する，ISUがconditionを送る先のURL

	// JIA activate 用。DefaultClient は MaxIdleConnsPerHost=2 なので同時登録で接続を作り直しやすい。
	jiaHTTPClient = &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          128,
			MaxIdleConnsPerHost:   128,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	jiaServiceURLMu sync.RWMutex
	jiaServiceURL   = defaultJIAServiceURL

	defaultIconImage []byte

	trendScheduleStart time.Time

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

	// 同一 UUID の activate 同時実行を防ぐ（成功後は existence キャッシュで弾く）
	isuActivateInFlight sync.Map

	isuOwnerCache = struct {
		sync.RWMutex
		values map[string]string
	}{values: make(map[string]string)}

	isuMetadataCache = struct {
		sync.RWMutex
		values map[string]isuMetadataCacheEntry
	}{values: make(map[string]isuMetadataCacheEntry)}

	// ユーザごとの ISU 一覧（id DESC の UUID 列）と、組み立て済み一覧 JSON。
	isuListByUser = struct {
		sync.RWMutex
		order    map[string][]string
		listJSON map[string][]byte
	}{order: make(map[string][]string), listJSON: make(map[string][]byte)}

	isuLatestConditionCache = struct {
		sync.RWMutex
		values map[string]isuLatestConditionEntry
	}{values: make(map[string]isuLatestConditionEntry)}

	isuIconCache = struct {
		sync.RWMutex
		values map[string]isuIconCacheEntry
	}{values: make(map[string]isuIconCacheEntry)}

	// ISU × グラフ日(JST 0時 Unix) → レスポンス。更新は該当時間帯だけ差し替え。
	graphCache = struct {
		sync.RWMutex
		values map[string]map[int64]graphCacheEntry
	}{values: make(map[string]map[int64]graphCacheEntry)}

	// ISU 単位でグラフの seal（書き込み）を排他し、GET は並列に読めるようにする。
	graphBuildMu sync.Map // map[string]*sync.RWMutex

	// 当日 open hour のグラフ。latest が変わらなければ再利用する。
	openHourGraphCache = struct {
		sync.RWMutex
		values map[string]openHourGraphCacheEntry
	}{values: make(map[string]openHourGraphCacheEntry)}

	// 同一 ISU は同じ shard。mem は FIFO で加点反映、db は後続永続化。
	conditionMemQueues []chan conditionWriteRequest
)

type graphCacheEntry struct {
	response []GraphResponse
	// sealedThrough: この Unix 時刻未満の時間帯は確定済み。開いている時間帯は含めない。
	sealedThrough int64
	// jsonBody: 全日 seal 済みのときだけ持つ。GET は組み立て・Marshal を省略できる。
	jsonBody []byte
}

type openHourGraphCacheEntry struct {
	hourStart int64
	throughTs int64
	response  GraphResponse
}

type isuLatestConditionEntry struct {
	Timestamp int64
	IsSitting bool
	Condition string
	Message   string
}

type isuIconCacheEntry struct {
	jiaUserID string
	image     []byte
}

type isuMetadataCacheEntry struct {
	jiaUserID string
	id        int
	name      string
	character string
	// getIsuID 用。登録後不変なので JSON を保持する。
	jsonBody []byte
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

// GET /api/condition のレスポンスは condition_store で組み立てる。

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
