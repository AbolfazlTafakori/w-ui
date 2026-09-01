package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/logger"
)

// The subcommands below exist for the `w-ui` management script.
//
// Anything the script needs from the database — the admin account, the
// configured port, what the panel is actually carrying — goes through the binary
// rather than through a shell reading the SQLite file directly. A shell parsing
// the database would be a second implementation of the schema, and it would rot
// the first time a column moved.

// dispatch runs a subcommand and reports whether it handled one.
func dispatch(args []string) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}

	switch args[0] {
	case "setting":
		return true, cmdSetting(args[1:])
	case "admin":
		return true, cmdAdmin(args[1:])
	case "version", "-v", "--version":
		fmt.Println(version)
		return true, nil
	case "help", "-h", "--help":
		usage(os.Stdout)
		return true, nil
	}
	return false, nil
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `W-UI %s

Usage:
  wui                              run the panel
  wui setting show                 print the effective configuration
  wui setting show --json          the same, as JSON
  wui admin reset [flags]          reset the administrator account
  wui version                      print the version

Flags for "admin reset":
  --username NAME    the administrator's name (default: keep the current one)
  --password PASS    the new password (default: generate one and print it)

Configuration is read from WUI_* environment variables. The management script
keeps them in /etc/wui/wui.env.
`, version)
}

// openDatabase connects using the same configuration the panel itself would.
func openDatabase() (*gorm.DB, config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cfg, err
	}
	// Subcommands are read by an operator in a terminal, and by this script's
	// own parsing. Both the panel's logger and GORM's are silenced so that the
	// only thing on stdout is the answer to what was asked.
	log, err := logger.New("error", "text")
	if err != nil {
		return nil, cfg, err
	}
	db, err := database.Open(cfg, log)
	if err != nil {
		return nil, cfg, err
	}
	return db.Session(&gorm.Session{Logger: gormlogger.Discard}), cfg, nil
}

func cmdSetting(args []string) error {
	fs := flag.NewFlagSet("setting", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print as JSON")

	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("setting: %w", err)
	}
	if sub != "" && sub != "show" {
		return fmt.Errorf("setting: unknown subcommand %q, want \"show\"", sub)
	}

	db, cfg, err := openDatabase()
	if err != nil {
		return err
	}

	// Find rather than First: an install with no administrator yet is a normal
	// state, and First reports it as an error the operator would have to read
	// past.
	var admins []model.Admin
	adminName := "(none)"
	if err := db.Order("id").Limit(1).Find(&admins).Error; err == nil && len(admins) > 0 {
		adminName = admins[0].Username
	}

	type counts struct {
		Interfaces int64
		Clients    int64
		Accounts   int64
		Active     int64
	}
	var c counts
	db.Model(&model.Interface{}).Count(&c.Interfaces)
	db.Model(&model.Client{}).Count(&c.Clients)
	db.Model(&model.Account{}).Count(&c.Accounts)
	db.Model(&model.Client{}).Where("status = ?", string(model.StatusActive)).Count(&c.Active)

	if *asJSON {
		fmt.Printf(`{"listen":%q,"dataDir":%q,"dbDriver":%q,"dbSource":%q,`+
			`"collectInterval":%q,"defaultLocale":%q,"logLevel":%q,"logFormat":%q,`+
			`"admin":%q,"interfaces":%d,"clients":%d,"accounts":%d,"activeClients":%d}`+"\n",
			cfg.Listen, cfg.DataDir, cfg.DBDriver, cfg.DBSource,
			cfg.CollectInterval, cfg.DefaultLocale, cfg.LogLevel, cfg.LogFormat,
			adminName, c.Interfaces, c.Clients, c.Accounts, c.Active)
		return nil
	}

	fmt.Printf("listen: %s\n", cfg.Listen)
	fmt.Printf("port: %s\n", portOf(cfg.Listen))
	fmt.Printf("dataDir: %s\n", cfg.DataDir)
	fmt.Printf("dbDriver: %s\n", cfg.DBDriver)
	fmt.Printf("dbSource: %s\n", cfg.DBSource)
	fmt.Printf("collectInterval: %s\n", cfg.CollectInterval)
	fmt.Printf("defaultLocale: %s\n", cfg.DefaultLocale)
	fmt.Printf("logLevel: %s\n", cfg.LogLevel)
	fmt.Printf("logFormat: %s\n", cfg.LogFormat)
	fmt.Printf("admin: %s\n", adminName)
	fmt.Printf("interfaces: %d\n", c.Interfaces)
	fmt.Printf("clients: %d\n", c.Clients)
	fmt.Printf("activeClients: %d\n", c.Active)
	fmt.Printf("accounts: %d\n", c.Accounts)
	return nil
}

// portOf pulls the port out of a listen address, for a script that wants to
// print a URL without parsing the address itself.
func portOf(listen string) string {
	if i := strings.LastIndex(listen, ":"); i >= 0 && i+1 < len(listen) {
		return listen[i+1:]
	}
	return listen
}

func cmdAdmin(args []string) error {
	if len(args) == 0 || args[0] != "reset" {
		return fmt.Errorf("admin: unknown subcommand, want \"reset\"")
	}

	fs := flag.NewFlagSet("admin reset", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	username := fs.String("username", "", "administrator name")
	password := fs.String("password", "", "new password")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("admin reset: %w", err)
	}

	db, cfg, err := openDatabase()
	if err != nil {
		return err
	}

	generated := false
	if *password == "" {
		*password, err = randomPassword()
		if err != nil {
			return err
		}
		generated = true
	}
	if len(*password) < 8 {
		return fmt.Errorf("admin reset: password must be at least 8 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("admin reset: hash password: %w", err)
	}

	var existing []model.Admin
	if err := db.Order("id").Limit(1).Find(&existing).Error; err != nil {
		return fmt.Errorf("admin reset: read administrator: %w", err)
	}

	var admin model.Admin
	switch {
	case len(existing) == 0:
		// Resetting on an install that has no administrator has to create one,
		// or an operator who deleted the row would be locked out with no way
		// back in short of deleting the database.
		admin = model.Admin{
			Username: firstNonEmpty(*username, "admin"),
			Locale:   cfg.DefaultLocale,
		}
		admin.PasswordHash = string(hash)
		if err := db.Create(&admin).Error; err != nil {
			return fmt.Errorf("admin reset: create administrator: %w", err)
		}
	default:
		admin = existing[0]
		updates := map[string]any{
			"password_hash": string(hash),
			"updated_at":    time.Now().UTC(),
		}
		if *username != "" {
			updates["username"] = *username
		}
		if err := db.Model(&admin).Updates(updates).Error; err != nil {
			return fmt.Errorf("admin reset: update administrator: %w", err)
		}
		if *username != "" {
			admin.Username = *username
		}
	}

	fmt.Printf("username: %s\n", admin.Username)
	fmt.Printf("password: %s\n", *password)
	if generated {
		fmt.Println("note: this password was generated and is shown once")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func randomPassword() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// binaryDir is where this executable lives, used only in messages.
func binaryDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}
