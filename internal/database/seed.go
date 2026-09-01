package database

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// LocalNodeName is the name of the node the panel itself runs on.
const LocalNodeName = "local"

// Bootstrap creates the rows a fresh install needs: the local node, and a first
// admin when none exists. It is safe to run on every boot.
//
// The generated admin password is returned so the caller can print it once. It
// is never stored in the clear and cannot be recovered afterwards.
func Bootstrap(db *gorm.DB, locale string, log *slog.Logger) (password string, err error) {
	if err := ensureLocalNode(db); err != nil {
		return "", err
	}

	var admins int64
	if err := db.Model(&model.Admin{}).Count(&admins).Error; err != nil {
		return "", fmt.Errorf("database: count admins: %w", err)
	}
	if admins > 0 {
		return "", nil
	}

	password, err = randomPassword()
	if err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("database: hash admin password: %w", err)
	}

	admin := model.Admin{
		Username:     "admin",
		PasswordHash: string(hash),
		Locale:       locale,
	}
	if err := db.Create(&admin).Error; err != nil {
		return "", fmt.Errorf("database: create first admin: %w", err)
	}

	log.Warn("created first admin account; this password is shown once")
	return password, nil
}

func ensureLocalNode(db *gorm.DB) error {
	var n int64
	if err := db.Model(&model.Node{}).Where("name = ?", LocalNodeName).Count(&n).Error; err != nil {
		return fmt.Errorf("database: count local node: %w", err)
	}
	if n > 0 {
		return nil
	}

	node := model.Node{
		Name:    LocalNodeName,
		Kind:    model.KindLocal,
		Address: "",
		Enabled: true,
	}
	if err := db.Create(&node).Error; err != nil {
		return fmt.Errorf("database: create local node: %w", err)
	}
	return nil
}

// LocalNode returns the node the panel runs on.
func LocalNode(db *gorm.DB) (*model.Node, error) {
	var node model.Node
	if err := db.Where("name = ?", LocalNodeName).First(&node).Error; err != nil {
		return nil, fmt.Errorf("database: load local node: %w", err)
	}
	return &node, nil
}

func randomPassword() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("database: generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
