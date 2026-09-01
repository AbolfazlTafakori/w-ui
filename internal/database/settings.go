package database

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Setting keys the panel manages itself.
const (
	KeyJWTSecret = "auth.jwt_secret"
)

// GetSetting reads a setting, returning ok=false when it is unset.
//
// A missing row is an ordinary outcome here — on first boot none of these keys
// exist — so the query runs with the logger silenced rather than reporting
// "record not found" as an error on every fresh install.
func GetSetting(db *gorm.DB, key string) (value string, ok bool, err error) {
	var s model.Setting
	dbErr := db.Session(&gorm.Session{Logger: gormlogger.Discard}).
		Where("key = ?", key).First(&s).Error
	if errors.Is(dbErr, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if dbErr != nil {
		return "", false, fmt.Errorf("database: read setting %q: %w", key, dbErr)
	}
	return s.Value, true, nil
}

// PutSetting writes a setting.
func PutSetting(db *gorm.DB, key, value string) error {
	s := model.Setting{Key: key, Value: value}
	if err := db.Save(&s).Error; err != nil {
		return fmt.Errorf("database: write setting %q: %w", key, err)
	}
	return nil
}

// EnsureSecret returns the stored secret for key, generating and persisting one
// on first use.
//
// Persisting it is what keeps sessions valid across a panel restart; a secret
// regenerated at every boot would sign every operator out on every deploy.
func EnsureSecret(db *gorm.DB, key string, nbytes int) ([]byte, error) {
	if v, ok, err := GetSetting(db, key); err != nil {
		return nil, err
	} else if ok {
		raw, derr := base64.StdEncoding.DecodeString(v)
		if derr != nil {
			return nil, fmt.Errorf("database: stored secret %q is malformed: %w", key, derr)
		}
		return raw, nil
	}

	raw := make([]byte, nbytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("database: generate secret %q: %w", key, err)
	}
	if err := PutSetting(db, key, base64.StdEncoding.EncodeToString(raw)); err != nil {
		return nil, err
	}
	return raw, nil
}
