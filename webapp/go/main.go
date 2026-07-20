package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
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
	trendCacheTTL               = 600 * time.Millisecond
	trendCacheMaxAge            = 900 * time.Millisecond
	conditionBatchMaxRequests   = 64
	conditionBatchWait          = 5 * time.Millisecond
	conditionWriterCount        = 6
)

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

	conditionWriteQueues []chan conditionWriteRequest
)

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

func getEnv(key string, defaultValue string) string {
	val := os.Getenv(key)
	if val != "" {
		return val
	}
	return defaultValue
}

func NewMySQLConnectionEnv() *MySQLConnectionEnv {
	return &MySQLConnectionEnv{
		Host:     getEnv("MYSQL_HOST", "127.0.0.1"),
		Port:     getEnv("MYSQL_PORT", "3306"),
		User:     getEnv("MYSQL_USER", "isucon"),
		DBName:   getEnv("MYSQL_DBNAME", "isucondition"),
		Password: getEnv("MYSQL_PASS", "isucon"),
	}
}

func (mc *MySQLConnectionEnv) ConnectDB() (*sqlx.DB, error) {
	dsn := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v?parseTime=true&loc=Asia%%2FTokyo&interpolateParams=true", mc.User, mc.Password, mc.Host, mc.Port, mc.DBName)
	return sqlx.Open("mysql", dsn)
}

func init() {
	sessionKey = []byte(getEnv("SESSION_KEY", "isucondition"))

	key, err := ioutil.ReadFile(jiaJWTSigningKeyPath)
	if err != nil {
		log.Fatalf("failed to read file: %v", err)
	}
	jiaJWTSigningKey, err = jwt.ParseECPublicKeyFromPEM(key)
	if err != nil {
		log.Fatalf("failed to parse ECDSA public key: %v", err)
	}
}

func main() {
	e := echo.New()
	e.Debug = false
	e.Logger.SetLevel(log.ERROR)

	e.Use(middleware.Recover())

	e.POST("/initialize", postInitialize)

	e.POST("/api/auth", postAuthentication)
	e.POST("/api/signout", postSignout)
	e.GET("/api/user/me", getMe)
	e.GET("/api/isu", getIsuList)
	e.POST("/api/isu", postIsu)
	e.GET("/api/isu/:jia_isu_uuid", getIsuID)
	e.GET("/api/isu/:jia_isu_uuid/icon", getIsuIcon)
	e.GET("/api/isu/:jia_isu_uuid/graph", getIsuGraph)
	e.GET("/api/condition/:jia_isu_uuid", getIsuConditions)
	e.GET("/api/trend", getTrend)

	e.POST("/api/condition/:jia_isu_uuid", postIsuCondition)

	e.GET("/", getIndex)
	e.GET("/isu/:jia_isu_uuid", getIndex)
	e.GET("/isu/:jia_isu_uuid/condition", getIndex)
	e.GET("/isu/:jia_isu_uuid/graph", getIndex)
	e.GET("/register", getIndex)
	e.Static("/assets", frontendContentsPath+"/assets")

	mySQLConnectionData = NewMySQLConnectionEnv()

	var err error
	db, err = mySQLConnectionData.ConnectDB()
	if err != nil {
		e.Logger.Fatalf("failed to connect db: %v", err)
		return
	}
	// Authentication is cookie based, and several endpoints can issue database
	// queries concurrently.  The default of ten connections made otherwise
	// independent requests wait for a free connection under benchmark load.
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(50)
	defer db.Close()
	startConditionWriter()

	postIsuConditionTargetBaseURL = os.Getenv("POST_ISUCONDITION_TARGET_BASE_URL")
	if postIsuConditionTargetBaseURL == "" {
		e.Logger.Fatalf("missing: POST_ISUCONDITION_TARGET_BASE_URL")
		return
	}

	serverPort := fmt.Sprintf(":%v", getEnv("SERVER_APP_PORT", "3000"))
	e.Logger.Fatal(e.Start(serverPort))
}

func sessionSignature(payload string) string {
	mac := hmac.New(sha256.New, sessionKey)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func setSessionCookie(w http.ResponseWriter, jiaUserID string) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(jiaUserID))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionName,
		Value:    payload + "." + sessionSignature(payload),
		Path:     "/",
		HttpOnly: true,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}

func getUserIDFromSession(c echo.Context) (string, int, error) {
	cookie, err := c.Cookie(sessionName)
	if err != nil {
		return "", http.StatusUnauthorized, fmt.Errorf("no session")
	}

	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 || !hmac.Equal([]byte(parts[1]), []byte(sessionSignature(parts[0]))) {
		return "", http.StatusUnauthorized, fmt.Errorf("invalid session")
	}
	jiaUserIDBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(jiaUserIDBytes) == 0 {
		return "", http.StatusUnauthorized, fmt.Errorf("invalid session")
	}
	jiaUserID := string(jiaUserIDBytes)

	// The session is stored in a signed cookie and is created only after the
	// user has been inserted.  There is no user-deletion/revocation operation,
	// so a database existence check on every request is redundant.  In
	// particular, /api/user/me can now complete without waiting on MySQL.
	return jiaUserID, 0, nil
}

func getJIAServiceURL() string {
	var url string
	err := db.Get(&url, "SELECT `url` FROM `isu_association_config` WHERE `name` = ?", "jia_service_url")
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Print(err)
		}
		return defaultJIAServiceURL
	}
	return url
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
// サービスを初期化
func postInitialize(c echo.Context) error {
	var request InitializeRequest
	err := c.Bind(&request)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad request body")
	}

	cmd := exec.Command("../sql/init.sh")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	err = cmd.Run()
	if err != nil {
		c.Logger().Errorf("exec init.sh error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	clearIsuExistenceCache()
	clearIsuOwnerCache()
	clearIsuMetadataCache()
	clearIsuLatestTimestampCache()
	clearIsuIconCache()

	_, err = db.Exec(
		"INSERT INTO `isu_association_config` (`name`, `url`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `url` = VALUES(`url`)",
		"jia_service_url",
		request.JIAServiceURL,
	)
	if err != nil {
		c.Logger().Errorf("db error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, InitializeResponse{
		Language: "go",
	})
}

// POST /api/auth
// サインアップ・サインイン
func postAuthentication(c echo.Context) error {
	reqJwt := strings.TrimPrefix(c.Request().Header.Get("Authorization"), "Bearer ")

	token, err := jwt.Parse(reqJwt, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodECDSA); !ok {
			return nil, jwt.NewValidationError(fmt.Sprintf("unexpected signing method: %v", token.Header["alg"]), jwt.ValidationErrorSignatureInvalid)
		}
		return jiaJWTSigningKey, nil
	})
	if err != nil {
		switch err.(type) {
		case *jwt.ValidationError:
			return c.String(http.StatusForbidden, "forbidden")
		default:
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.Logger().Errorf("invalid JWT payload")
		return c.NoContent(http.StatusInternalServerError)
	}
	jiaUserIDVar, ok := claims["jia_user_id"]
	if !ok {
		return c.String(http.StatusBadRequest, "invalid JWT payload")
	}
	jiaUserID, ok := jiaUserIDVar.(string)
	if !ok {
		return c.String(http.StatusBadRequest, "invalid JWT payload")
	}

	_, err = db.Exec("INSERT IGNORE INTO user (`jia_user_id`) VALUES (?)", jiaUserID)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	setSessionCookie(c.Response(), jiaUserID)

	return c.NoContent(http.StatusOK)
}

// POST /api/signout
// サインアウト
func postSignout(c echo.Context) error {
	_, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	clearSessionCookie(c.Response())

	return c.NoContent(http.StatusOK)
}

// GET /api/user/me
// サインインしている自分自身の情報を取得
func getMe(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	res := GetMeResponse{JIAUserID: jiaUserID}
	return c.JSON(http.StatusOK, res)
}

// GET /api/isu
// ISUの一覧を取得
func getIsuList(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	isuList := []Isu{}
	err = db.Select(
		&isuList,
		"SELECT `id`, `jia_isu_uuid`, `name`, `character`, `latest_timestamp`, `latest_is_sitting`, `latest_condition`, `latest_message`, `jia_user_id`, `created_at`, `updated_at`"+
			" FROM `isu` WHERE `jia_user_id` = ? ORDER BY `id` DESC",
		jiaUserID)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	responseList := make([]GetIsuListResponse, 0, len(isuList))
	for _, isu := range isuList {
		setCachedIsuOwner(isu.JIAIsuUUID, jiaUserID)
		setCachedIsuMetadata(isu.JIAIsuUUID, jiaUserID, isu.Name)
		var formattedCondition *GetIsuConditionResponse
		if isu.LatestTimestamp.Valid {
			conditionLevel, err := calculateConditionLevel(isu.LatestCondition.String)
			if err != nil {
				c.Logger().Error(err)
				return c.NoContent(http.StatusInternalServerError)
			}

			formattedCondition = &GetIsuConditionResponse{
				JIAIsuUUID:     isu.JIAIsuUUID,
				IsuName:        isu.Name,
				Timestamp:      isu.LatestTimestamp.Time.Unix(),
				IsSitting:      isu.LatestIsSitting.Bool,
				Condition:      isu.LatestCondition.String,
				ConditionLevel: conditionLevel,
				Message:        isu.LatestMessage.String,
			}
		}

		res := GetIsuListResponse{
			ID:                 isu.ID,
			JIAIsuUUID:         isu.JIAIsuUUID,
			Name:               isu.Name,
			Character:          isu.Character,
			LatestIsuCondition: formattedCondition}
		responseList = append(responseList, res)
	}

	return c.JSON(http.StatusOK, responseList)
}

// POST /api/isu
// ISUを登録
func postIsu(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	useDefaultImage := false

	jiaIsuUUID := c.FormValue("jia_isu_uuid")
	isuName := c.FormValue("isu_name")
	fh, err := c.FormFile("image")
	if err != nil {
		if !errors.Is(err, http.ErrMissingFile) {
			return c.String(http.StatusBadRequest, "bad format: icon")
		}
		useDefaultImage = true
	}

	var image []byte

	if useDefaultImage {
		image, err = ioutil.ReadFile(defaultIconFilePath)
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
	} else {
		file, err := fh.Open()
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
		defer file.Close()

		image, err = ioutil.ReadAll(file)
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
	}

	// JIAへの通信を先に完了させ、characterを含むISU行を短い
	// トランザクションで一度に公開する。
	targetURL := getJIAServiceURL() + "/api/activate"
	body := JIAServiceRequest{postIsuConditionTargetBaseURL, jiaIsuUUID}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	reqJIA, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewBuffer(bodyJSON))
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	reqJIA.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(reqJIA)
	if err != nil {
		c.Logger().Errorf("failed to request to JIAService: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer res.Body.Close()

	resBody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	if res.StatusCode != http.StatusAccepted {
		c.Logger().Errorf("JIAService returned error: status code %v, message: %v", res.StatusCode, string(resBody))
		return c.String(res.StatusCode, "JIAService returned error")
	}

	var isuFromJIA IsuFromJIA
	err = json.Unmarshal(resBody, &isuFromJIA)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	tx, err := db.Beginx()
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer tx.Rollback()

	_, err = tx.Exec("INSERT INTO `isu`"+
		"	(`jia_isu_uuid`, `name`, `image`, `jia_user_id`, `character`) VALUES (?, ?, ?, ?, ?)",
		jiaIsuUUID, isuName, image, jiaUserID, isuFromJIA.Character)
	if err != nil {
		mysqlErr, ok := err.(*mysql.MySQLError)

		if ok && mysqlErr.Number == uint16(mysqlErrNumDuplicateEntry) {
			return c.String(http.StatusConflict, "duplicated: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	var isu Isu
	err = tx.Get(
		&isu,
		"SELECT `id`, `jia_isu_uuid`, `name`, `character` FROM `isu`"+
			" WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
		jiaUserID, jiaIsuUUID)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	err = tx.Commit()
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	setCachedIsuExistence(jiaIsuUUID, true)
	setCachedIsuOwner(jiaIsuUUID, jiaUserID)
	setCachedIsuMetadata(jiaIsuUUID, jiaUserID, isuName)
	setCachedIsuIcon(jiaIsuUUID, jiaUserID, image)

	return c.JSON(http.StatusCreated, isu)
}

// GET /api/isu/:jia_isu_uuid
// ISUの情報を取得
func getIsuID(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")

	var res Isu
	err = db.Get(&res, "SELECT `id`, `jia_isu_uuid`, `name`, `character` FROM `isu`"+
		" WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
		jiaUserID, jiaIsuUUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.String(http.StatusNotFound, "not found: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	setCachedIsuOwner(jiaIsuUUID, jiaUserID)
	setCachedIsuMetadata(jiaIsuUUID, jiaUserID, res.Name)

	return c.JSON(http.StatusOK, res)
}

// GET /api/isu/:jia_isu_uuid/icon
// ISUのアイコンを取得
func getIsuIcon(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")

	if image, ok := getCachedIsuIcon(jiaIsuUUID, jiaUserID); ok {
		return c.Blob(http.StatusOK, "", image)
	}

	var image []byte
	err = db.Get(&image, "SELECT `image` FROM `isu` WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
		jiaUserID, jiaIsuUUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.String(http.StatusNotFound, "not found: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	setCachedIsuOwner(jiaIsuUUID, jiaUserID)
	setCachedIsuIconAsync(jiaIsuUUID, jiaUserID, image)

	return c.Blob(http.StatusOK, "", image)
}

// GET /api/isu/:jia_isu_uuid/graph
// ISUのコンディショングラフ描画のための情報を取得
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
	date := time.Unix(datetimeInt64, 0).Truncate(time.Hour)

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

	res, err := generateIsuGraphResponse(db, jiaIsuUUID, date)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, res)
}

// グラフのデータ点を一日分生成
func generateIsuGraphResponse(db *sqlx.DB, jiaIsuUUID string, graphDate time.Time) ([]GraphResponse, error) {
	dataPoints := []GraphDataPointWithInfo{}
	conditionsInThisHour := []IsuCondition{}
	timestampsInThisHour := []int64{}
	var startTimeInThisHour time.Time
	var row isuConditionGraphRow
	endTime := graphDate.Add(time.Hour * 24)

	rows, err := db.Queryx(
		"SELECT `timestamp`, `is_sitting`, `condition` FROM `isu_condition` WHERE `jia_isu_uuid` = ? AND `timestamp` >= ? AND `timestamp` < ? ORDER BY `timestamp` ASC",
		jiaIsuUUID, graphDate, endTime,
	)
	if err != nil {
		return nil, fmt.Errorf("db error: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		err = rows.StructScan(&row)
		if err != nil {
			return nil, err
		}
		condition := IsuCondition{
			Timestamp: row.Timestamp,
			IsSitting: row.IsSitting,
			Condition: row.Condition,
		}

		truncatedConditionTime := condition.Timestamp.Truncate(time.Hour)
		if truncatedConditionTime != startTimeInThisHour {
			if len(conditionsInThisHour) > 0 {
				data, err := calculateGraphDataPoint(conditionsInThisHour)
				if err != nil {
					return nil, err
				}

				dataPoints = append(dataPoints,
					GraphDataPointWithInfo{
						JIAIsuUUID:          jiaIsuUUID,
						StartAt:             startTimeInThisHour,
						Data:                data,
						ConditionTimestamps: timestampsInThisHour})
			}

			startTimeInThisHour = truncatedConditionTime
			conditionsInThisHour = []IsuCondition{}
			timestampsInThisHour = []int64{}
		}
		conditionsInThisHour = append(conditionsInThisHour, condition)
		timestampsInThisHour = append(timestampsInThisHour, condition.Timestamp.Unix())
	}

	if len(conditionsInThisHour) > 0 {
		data, err := calculateGraphDataPoint(conditionsInThisHour)
		if err != nil {
			return nil, err
		}

		dataPoints = append(dataPoints,
			GraphDataPointWithInfo{
				JIAIsuUUID:          jiaIsuUUID,
				StartAt:             startTimeInThisHour,
				Data:                data,
				ConditionTimestamps: timestampsInThisHour})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	responseList := []GraphResponse{}
	index := 0
	thisTime := graphDate

	for thisTime.Before(graphDate.Add(time.Hour * 24)) {
		var data *GraphDataPoint
		timestamps := []int64{}

		if index < len(dataPoints) {
			dataWithInfo := dataPoints[index]

			if dataWithInfo.StartAt.Equal(thisTime) {
				data = &dataWithInfo.Data
				timestamps = dataWithInfo.ConditionTimestamps
				index++
			}
		}

		resp := GraphResponse{
			StartAt:             thisTime.Unix(),
			EndAt:               thisTime.Add(time.Hour).Unix(),
			Data:                data,
			ConditionTimestamps: timestamps,
		}
		responseList = append(responseList, resp)

		thisTime = thisTime.Add(time.Hour)
	}

	return responseList, nil
}

// 複数のISUのコンディションからグラフの一つのデータ点を計算
func calculateGraphDataPoint(isuConditions []IsuCondition) (GraphDataPoint, error) {
	conditionsCount := map[string]int{"is_broken": 0, "is_dirty": 0, "is_overweight": 0}
	rawScore := 0
	for _, condition := range isuConditions {
		badConditionsCount := 0

		if !isValidConditionFormat(condition.Condition) {
			return GraphDataPoint{}, fmt.Errorf("invalid condition format")
		}

		for _, condStr := range strings.Split(condition.Condition, ",") {
			keyValue := strings.Split(condStr, "=")

			conditionName := keyValue[0]
			if keyValue[1] == "true" {
				conditionsCount[conditionName] += 1
				badConditionsCount++
			}
		}

		if badConditionsCount >= 3 {
			rawScore += scoreConditionLevelCritical
		} else if badConditionsCount >= 1 {
			rawScore += scoreConditionLevelWarning
		} else {
			rawScore += scoreConditionLevelInfo
		}
	}

	sittingCount := 0
	for _, condition := range isuConditions {
		if condition.IsSitting {
			sittingCount++
		}
	}

	isuConditionsLength := len(isuConditions)

	score := rawScore * 100 / 3 / isuConditionsLength

	sittingPercentage := sittingCount * 100 / isuConditionsLength
	isBrokenPercentage := conditionsCount["is_broken"] * 100 / isuConditionsLength
	isOverweightPercentage := conditionsCount["is_overweight"] * 100 / isuConditionsLength
	isDirtyPercentage := conditionsCount["is_dirty"] * 100 / isuConditionsLength

	dataPoint := GraphDataPoint{
		Score: score,
		Percentage: ConditionsPercentage{
			Sitting:      sittingPercentage,
			IsBroken:     isBrokenPercentage,
			IsOverweight: isOverweightPercentage,
			IsDirty:      isDirtyPercentage,
		},
	}
	return dataPoint, nil
}

// GET /api/condition/:jia_isu_uuid
// ISUのコンディションを取得
func getIsuConditions(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")
	if jiaIsuUUID == "" {
		return c.String(http.StatusBadRequest, "missing: jia_isu_uuid")
	}

	endTimeInt64, err := strconv.ParseInt(c.QueryParam("end_time"), 10, 64)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad format: end_time")
	}
	endTime := time.Unix(endTimeInt64, 0)
	conditionLevelCSV := c.QueryParam("condition_level")
	if conditionLevelCSV == "" {
		return c.String(http.StatusBadRequest, "missing: condition_level")
	}
	conditionLevel := map[string]interface{}{}
	for _, level := range strings.Split(conditionLevelCSV, ",") {
		conditionLevel[level] = struct{}{}
	}

	startTimeStr := c.QueryParam("start_time")
	var startTime time.Time
	if startTimeStr != "" {
		startTimeInt64, err := strconv.ParseInt(startTimeStr, 10, 64)
		if err != nil {
			return c.String(http.StatusBadRequest, "bad format: start_time")
		}
		startTime = time.Unix(startTimeInt64, 0)
	}

	isuName, cached := getCachedIsuMetadata(jiaIsuUUID, jiaUserID)
	if !cached {
		err = db.Get(&isuName,
			"SELECT name FROM `isu` WHERE `jia_isu_uuid` = ? AND `jia_user_id` = ?",
			jiaIsuUUID, jiaUserID,
		)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return c.String(http.StatusNotFound, "not found: isu")
			}

			c.Logger().Errorf("db error: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
		setCachedIsuOwner(jiaIsuUUID, jiaUserID)
		setCachedIsuMetadata(jiaIsuUUID, jiaUserID, isuName)
	}

	conditionsResponse, err := getIsuConditionsFromDB(db, jiaIsuUUID, endTime, conditionLevel, startTime, conditionLimit, isuName)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	body, err := jsonFast.Marshal(conditionsResponse)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}
	return c.JSONBlob(http.StatusOK, body)
}

// ISUのコンディションをDBから取得
func getIsuConditionsFromDB(db *sqlx.DB, jiaIsuUUID string, endTime time.Time, conditionLevel map[string]interface{}, startTime time.Time,
	limit int, isuName string) ([]GetIsuConditionResponse, error) {
	allowedConditions, levelByCondition := conditionStringsForLevels(conditionLevel)
	if len(allowedConditions) == 0 {
		return []GetIsuConditionResponse{}, nil
	}

	conditions := make([]isuConditionListRow, 0, limit)
	args := []interface{}{jiaIsuUUID, endTime}
	query := "SELECT `timestamp`, `is_sitting`, `condition`, `message` FROM `isu_condition` WHERE `jia_isu_uuid` = ?" +
		"	AND `timestamp` < ?"
	if !startTime.IsZero() {
		query += "	AND ? <= `timestamp`"
		args = append(args, startTime)
	}

	if len(allowedConditions) < 8 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(allowedConditions)), ",")
		query += "	AND `condition` IN (" + placeholders + ")"
		for _, condition := range allowedConditions {
			args = append(args, condition)
		}
	}
	query += "	ORDER BY `timestamp` DESC LIMIT ?"
	args = append(args, limit)

	err := db.Select(&conditions, query, args...)
	if err != nil {
		return nil, fmt.Errorf("db error: %v", err)
	}

	conditionsResponse := make([]GetIsuConditionResponse, 0, len(conditions))
	for _, row := range conditions {
		cLevel, ok := levelByCondition[row.Condition]
		if !ok {
			var err error
			cLevel, err = calculateConditionLevel(row.Condition)
			if err != nil {
				continue
			}
		}

		conditionsResponse = append(conditionsResponse, GetIsuConditionResponse{
			JIAIsuUUID:     jiaIsuUUID,
			IsuName:        isuName,
			Timestamp:      row.Timestamp.Unix(),
			IsSitting:      row.IsSitting,
			Condition:      row.Condition,
			ConditionLevel: cLevel,
			Message:        row.Message,
		})
	}

	return conditionsResponse, nil
}

func conditionStringsForLevels(conditionLevel map[string]interface{}) ([]string, map[string]string) {
	conditions := make([]string, 0, 8)
	levelByCondition := make(map[string]string, 8)
	for _, isDirty := range []bool{false, true} {
		for _, isOverweight := range []bool{false, true} {
			for _, isBroken := range []bool{false, true} {
				condition := fmt.Sprintf(
					"is_dirty=%t,is_overweight=%t,is_broken=%t",
					isDirty, isOverweight, isBroken,
				)
				level, _ := calculateConditionLevel(condition)
				if _, ok := conditionLevel[level]; ok {
					conditions = append(conditions, condition)
					levelByCondition[condition] = level
				}
			}
		}
	}
	return conditions, levelByCondition
}

// ISUのコンディションの文字列からコンディションレベルを計算
func calculateConditionLevel(condition string) (string, error) {
	var conditionLevel string

	warnCount := strings.Count(condition, "=true")
	switch warnCount {
	case 0:
		conditionLevel = conditionLevelInfo
	case 1, 2:
		conditionLevel = conditionLevelWarning
	case 3:
		conditionLevel = conditionLevelCritical
	default:
		return "", fmt.Errorf("unexpected warn count")
	}

	return conditionLevel, nil
}

// GET /api/trend
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
			trend = &TrendResponse{Character: row.Character}
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

func startConditionWriter() {
	conditionWriteQueues = make([]chan conditionWriteRequest, conditionWriterCount)
	for i := range conditionWriteQueues {
		conditionWriteQueues[i] = make(chan conditionWriteRequest, 1024)
		go conditionWriter(conditionWriteQueues[i])
	}
}

func conditionWriter(queue <-chan conditionWriteRequest) {
	for first := range queue {
		batch := []conditionWriteRequest{first}
		timer := time.NewTimer(conditionBatchWait)
	collect:
		for len(batch) < conditionBatchMaxRequests {
			select {
			case request := <-queue:
				batch = append(batch, request)
			case <-timer.C:
				break collect
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		if err := writeConditionBatch(batch); err != nil {
			log.Errorf("condition write batch error: %v", err)
		}
	}
}

func conditionWriterQueue(jiaIsuUUID string) chan conditionWriteRequest {
	// 同じISUは常に同じwriterへ送り、到着順に処理する。
	// これによりwriter間で同じisu行を競合させない。
	hash := uint32(2166136261)
	for i := 0; i < len(jiaIsuUUID); i++ {
		hash ^= uint32(jiaIsuUUID[i])
		hash *= 16777619
	}
	return conditionWriteQueues[int(hash%conditionWriterCount)]
}

func writeConditionBatch(batch []conditionWriteRequest) error {
	type latestCondition struct {
		jiaIsuUUID string
		condition  PostIsuConditionRequest
	}

	rowCount := 0
	for _, request := range batch {
		rowCount += len(request.conditions)
	}
	placeholders := make([]string, 0, rowCount)
	args := make([]interface{}, 0, rowCount*5)
	latestByIsu := make(map[string]latestCondition)
	for _, request := range batch {
		for _, condition := range request.conditions {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?)")
			args = append(args,
				request.jiaIsuUUID,
				time.Unix(condition.Timestamp, 0),
				condition.IsSitting,
				condition.Condition,
				condition.Message,
			)
			latest, ok := latestByIsu[request.jiaIsuUUID]
			if !ok || condition.Timestamp >= latest.condition.Timestamp {
				latestByIsu[request.jiaIsuUUID] = latestCondition{request.jiaIsuUUID, condition}
			}
		}
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		"INSERT IGNORE INTO `isu_condition`"+
			" (`jia_isu_uuid`, `timestamp`, `is_sitting`, `condition`, `message`) VALUES "+
			strings.Join(placeholders, ", "),
		args...,
	)
	if err != nil {
		return err
	}

	uuids := make([]string, 0, len(latestByIsu))
	for jiaIsuUUID := range latestByIsu {
		uuids = append(uuids, jiaIsuUUID)
	}
	sort.Strings(uuids)
	updatedLatest := make([]latestCondition, 0, len(uuids))
	for _, jiaIsuUUID := range uuids {
		latest := latestByIsu[jiaIsuUUID]
		cachedTimestamp, cached := getCachedIsuLatestTimestamp(jiaIsuUUID)
		if cached && latest.condition.Timestamp < cachedTimestamp {
			continue
		}
		updatedLatest = append(updatedLatest, latest)
	}
	if len(updatedLatest) > 0 {
		var updateQuery strings.Builder
		updateArgs := make([]interface{}, 0, len(updatedLatest)*11)
		appendCase := func(column string, value func(latestCondition) interface{}) {
			updateQuery.WriteString(" `")
			updateQuery.WriteString(column)
			updateQuery.WriteString("` = CASE `jia_isu_uuid`")
			for _, latest := range updatedLatest {
				updateQuery.WriteString(" WHEN ? THEN ?")
				updateArgs = append(updateArgs, latest.jiaIsuUUID, value(latest))
			}
			updateQuery.WriteString(" ELSE `")
			updateQuery.WriteString(column)
			updateQuery.WriteString("` END")
		}

		updateQuery.WriteString("UPDATE `isu` SET")
		appendCase("latest_timestamp", func(latest latestCondition) interface{} {
			return time.Unix(latest.condition.Timestamp, 0)
		})
		updateQuery.WriteString(",")
		appendCase("latest_is_sitting", func(latest latestCondition) interface{} {
			return latest.condition.IsSitting
		})
		updateQuery.WriteString(",")
		appendCase("latest_condition", func(latest latestCondition) interface{} {
			return latest.condition.Condition
		})
		updateQuery.WriteString(",")
		appendCase("latest_message", func(latest latestCondition) interface{} {
			return latest.condition.Message
		})

		updateQuery.WriteString(" WHERE `jia_isu_uuid` IN (")
		updateQuery.WriteString(strings.TrimRight(strings.Repeat("?,", len(updatedLatest)), ","))
		updateQuery.WriteString(") AND (`latest_timestamp` IS NULL OR `latest_timestamp` <= CASE `jia_isu_uuid`")
		for _, latest := range updatedLatest {
			updateArgs = append(updateArgs, latest.jiaIsuUUID)
		}
		for _, latest := range updatedLatest {
			updateQuery.WriteString(" WHEN ? THEN ?")
			updateArgs = append(updateArgs, latest.jiaIsuUUID, time.Unix(latest.condition.Timestamp, 0))
		}
		updateQuery.WriteString(" END)")

		if _, err = tx.Exec(updateQuery.String(), updateArgs...); err != nil {
			return err
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	for _, latest := range updatedLatest {
		setCachedIsuLatestTimestamp(latest.jiaIsuUUID, latest.condition.Timestamp)
	}
	return nil
}

// POST /api/condition/:jia_isu_uuid
// ISUからのコンディションを受け取る
func postIsuCondition(c echo.Context) error {
	// // TODO: 一定割合リクエストを落としてしのぐようにしたが、本来は全量さばけるようにすべき
	// 全部裁くぜ
	// dropProbability := 0.1
	// if rand.Float64() <= dropProbability {
	// 	return c.NoContent(http.StatusAccepted)
	// }

	jiaIsuUUID := c.Param("jia_isu_uuid")
	if jiaIsuUUID == "" {
		return c.String(http.StatusBadRequest, "missing: jia_isu_uuid")
	}

	body, err := ioutil.ReadAll(c.Request().Body)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad request body")
	}
	req := []PostIsuConditionRequest{}
	if err := jsonFast.Unmarshal(body, &req); err != nil {
		return c.String(http.StatusBadRequest, "bad request body")
	} else if len(req) == 0 {
		return c.String(http.StatusBadRequest, "bad request body")
	}

	isuExists, cached := getCachedIsuExistence(jiaIsuUUID)
	if !cached {
		var exists int
		err = db.Get(&exists, "SELECT EXISTS(SELECT 1 FROM `isu` WHERE `jia_isu_uuid` = ?)", jiaIsuUUID)
		if err != nil {
			c.Logger().Errorf("db error: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
		isuExists = exists == 1
		setCachedIsuExistence(jiaIsuUUID, isuExists)
	}
	if !isuExists {
		return c.String(http.StatusNotFound, "not found: isu")
	}

	for i := range req {
		if !isValidConditionFormat(req[i].Condition) {
			return c.String(http.StatusBadRequest, "bad request body")
		}
	}

	writeRequest := conditionWriteRequest{
		jiaIsuUUID: jiaIsuUUID,
		conditions: req,
	}

	// DBコミットは待たず、キュー投入成功時点で202を返す。
	// JIAクライアントは短いタイムアウトなので、書き込み完了待ちはCLOSE-WAITの原因になりやすい。
	requestContext := c.Request().Context()
	select {
	case conditionWriterQueue(jiaIsuUUID) <- writeRequest:
		return c.NoContent(http.StatusAccepted)
	case <-requestContext.Done():
		return nil
	}
}

// ISUのコンディションの文字列がcsv形式になっているか検証
func isValidConditionFormat(conditionStr string) bool {

	keys := []string{"is_dirty=", "is_overweight=", "is_broken="}
	const valueTrue = "true"
	const valueFalse = "false"

	idxCondStr := 0

	for idxKeys, key := range keys {
		if !strings.HasPrefix(conditionStr[idxCondStr:], key) {
			return false
		}
		idxCondStr += len(key)

		if strings.HasPrefix(conditionStr[idxCondStr:], valueTrue) {
			idxCondStr += len(valueTrue)
		} else if strings.HasPrefix(conditionStr[idxCondStr:], valueFalse) {
			idxCondStr += len(valueFalse)
		} else {
			return false
		}

		if idxKeys < (len(keys) - 1) {
			if conditionStr[idxCondStr] != ',' {
				return false
			}
			idxCondStr++
		}
	}

	return (idxCondStr == len(conditionStr))
}

func getIndex(c echo.Context) error {
	return c.File(frontendContentsPath + "/index.html")
}
