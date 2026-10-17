package database

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// RetiredKeys are settings the panel stored once and stores no longer, each
// with why. They are taken out at start (DropRetired), and the upgrade check
// does not expect them to survive an update: every other setting must.
var RetiredKeys = map[string]string{
	// Since 3.0 the session key lives in keys/session.key (keyfile), out of
	// the database and so out of every backup: whoever held a backup held
	// the key, and could sign in to the panel it came from as its owner. A
	// new key is made then, so a backup sent anywhere before carries a key
	// that opens nothing.
	"auth.jwt_secret": "the session key moved out of the database in 3.0",
}

// DropRetired removes every retired setting that is still stored.
func DropRetired(db *gorm.DB) error {
	keys := make([]string, 0, len(RetiredKeys))
	for k := range RetiredKeys {
		keys = append(keys, k)
	}
	if err := db.Where("key IN ?", keys).Delete(&model.Setting{}).Error; err != nil {
		return fmt.Errorf("database: drop retired settings: %w", err)
	}
	return nil
}

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
