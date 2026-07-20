package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/dgrijalva/jwt-go"
	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
)

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
