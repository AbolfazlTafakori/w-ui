package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/logger"
	"github.com/abolfazl/w-ui/internal/service"
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
	case "token":
		return true, cmdToken(args[1:])
	case "keygen":
		return true, keygenCommand(args[1:])
	case "sign":
		return true, signCommand(args[1:])
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
  wui setting set [flags]          change where the panel answers (applied
                                   at the next start)
  wui setting reset                forget every panel setting; the admin
                                   account and the customers are kept
  wui admin reset [flags]          reset the administrator account
  wui token issue --name NAME      mint an API token, printed once
  wui version                      print the version
  wui keygen                       make a release-signing key pair
  wui sign <binary>                sign a build, for a release

Flags for "setting set":
  --listen ADDR      the address to bind (0.0.0.0 for every address)
  --port N           the port
  --base-path PATH   the secret path the panel is served under
  --cert FILE        certificate to serve TLS with
  --key FILE         its private key
  --no-tls           forget the certificate and serve plain HTTP

Flags for "admin reset":
  --username NAME    the administrator's name (default: keep the current one)
  --password PASS    the new password (default: generate one and print it)
  --password-stdin   read the password from standard input instead, so it
                     never appears in the process list
  --quiet            print nothing on success

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
	if sub == "set" {
		return cmdSettingSet(args)
	}
	if sub == "reset" {
		return cmdSettingReset()
	}
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("setting: %w", err)
	}
	if sub != "" && sub != "show" {
		return fmt.Errorf("setting: unknown subcommand %q, want \"show\" or \"set\"", sub)
	}

	db, cfg, err := openDatabase()
	if err != nil {
		return err
	}

	// What the panel actually answers on: the settings page's values win
	// over the environment at start, so they are what is shown.
	stored, _ := service.NewSettings(db, cfg.DefaultLocale).Get(context.Background())
	if stored.WebListen != "" || stored.WebPort > 0 {
		host := stored.WebListen
		port := stored.WebPort
		if port == 0 {
			port = 2096
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		cfg.Listen = host + ":" + strconv.Itoa(port)
	}
	if stored.WebBasePath != "" {
		cfg.BasePath = stored.WebBasePath
	}
	if stored.WebCertFile != "" && stored.WebKeyFile != "" {
		cfg.TLSCert, cfg.TLSKey = stored.WebCertFile, stored.WebKeyFile
	}
	listenIP, _, _ := net.SplitHostPort(cfg.Listen)
	if listenIP == "" {
		listenIP = "0.0.0.0"
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
			`"scheme":%q,"tls":%t,"tlsCert":%q,"tlsKey":%q,"basePath":%q,`+
			`"admin":%q,"interfaces":%d,"clients":%d,"accounts":%d,"activeClients":%d}`+"\n",
			cfg.Listen, cfg.DataDir, cfg.DBDriver, cfg.DBSource,
			cfg.CollectInterval, cfg.DefaultLocale, cfg.LogLevel, cfg.LogFormat,
			cfg.Scheme(), cfg.TLS(), cfg.TLSCert, cfg.TLSKey, cfg.BasePath,
			adminName, c.Interfaces, c.Clients, c.Accounts, c.Active)
		return nil
	}

	fmt.Printf("listen: %s\n", cfg.Listen)
	fmt.Printf("listenIP: %s\n", listenIP)
	fmt.Printf("port: %s\n", portOf(cfg.Listen))
	fmt.Printf("basePath: %s\n", cfg.BasePath)
	fmt.Printf("scheme: %s\n", cfg.Scheme())
	fmt.Printf("cert: %s\n", cfg.TLSCert)
	fmt.Printf("key: %s\n", cfg.TLSKey)
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

// cmdToken mints an API token from the shell: what the installer prints
// at the end, as 3x-ui prints its apiToken, so automation has one from
// the first minute without anybody signing in to make it.
func cmdToken(args []string) error {
	if len(args) == 0 || args[0] != "issue" {
		return errors.New("token: want \"issue --name NAME\"")
	}
	fs := flag.NewFlagSet("token issue", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "what the token is for")
	quiet := fs.Bool("quiet", false, "print only the token")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("token issue: %w", err)
	}
	db, _, err := openDatabase()
	if err != nil {
		return err
	}
	tok, err := service.NewNodes(db, slog.New(slog.NewTextHandler(io.Discard, nil))).IssueToken(context.Background(), *name)
	if err != nil {
		return err
	}
	if *quiet {
		fmt.Println(tok.Token)
		return nil
	}
	fmt.Printf("token %q issued; it is shown once:\n\n  %s\n\nSend it as: Authorization: Bearer <token>\n", *name, tok.Token)
	return nil
}

// cmdSettingReset forgets every panel setting -- port, path, certificate,
// session length, defaults -- and leaves the administrator and every
// customer alone, as `x-ui setting -reset` does.
func cmdSettingReset() error {
	db, _, err := openDatabase()
	if err != nil {
		return err
	}
	res := db.Where("key LIKE ?", "panel.%").Delete(&model.Setting{})
	if res.Error != nil {
		return res.Error
	}
	fmt.Printf("reset %d panel settings; the environment's values apply from the next start\n", res.RowsAffected)
	return nil
}

// cmdSettingSet is what the installer and the management script call once
// they have a certificate or a port to hand the panel: the same knobs as
// the settings page, without a browser. The panel's own stored settings
// are what win over the environment at start, so this is where a change
// has to land to stick.
func cmdSettingSet(args []string) error {
	fs := flag.NewFlagSet("setting set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", "", "")
	port := fs.Int("port", 0, "")
	basePath := fs.String("base-path", "", "")
	cert := fs.String("cert", "", "")
	key := fs.String("key", "", "")
	noTLS := fs.Bool("no-tls", false, "")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("setting set: %w", err)
	}
	if fs.NFlag() == 0 {
		return errors.New("setting set: nothing to change; see wui help")
	}
	if (*cert == "") != (*key == "") {
		return errors.New("setting set: --cert and --key go together")
	}

	db, cfg, err := openDatabase()
	if err != nil {
		return err
	}
	ctx := context.Background()
	settings := service.NewSettings(db, cfg.DefaultLocale)
	cur, err := settings.Get(ctx)
	if err != nil {
		return err
	}
	if *listen != "" {
		cur.WebListen = *listen
	}
	if *port != 0 {
		cur.WebPort = *port
	}
	if *basePath != "" {
		cur.WebBasePath = *basePath
	}
	if *cert != "" {
		for _, f := range []string{*cert, *key} {
			if _, err := os.Stat(f); err != nil {
				return fmt.Errorf("setting set: %w", err)
			}
		}
		cur.WebCertFile, cur.WebKeyFile = *cert, *key
	}
	if *noTLS {
		cur.WebCertFile, cur.WebKeyFile = "", ""
	}
	saved, err := settings.Save(ctx, cur)
	if err != nil {
		return err
	}
	scheme := "http"
	if saved.WebCertFile != "" {
		scheme = "https"
	}
	host := saved.WebListen
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "<this server>"
	}
	p := saved.WebPort
	if p == 0 {
		p = 2096
	}
	if saved.WebBasePath == "" {
		saved.WebBasePath = "/"
	}
	fmt.Printf("saved; from the next start the panel answers at %s://%s:%d%s\n", scheme, host, p, saved.WebBasePath)
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
	// A password passed as an argument is readable by every user on the box
	// for as long as the command runs, via ps. Scripts should pipe it instead.
	fromStdin := fs.Bool("password-stdin", false, "read the password from stdin")
	quiet := fs.Bool("quiet", false, "print nothing on success")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("admin reset: %w", err)
	}

	if *fromStdin {
		if *password != "" {
			return fmt.Errorf("admin reset: use --password or --password-stdin, not both")
		}
		raw, rerr := io.ReadAll(os.Stdin)
		if rerr != nil {
			return fmt.Errorf("admin reset: read password from stdin: %w", rerr)
		}
		// One trailing newline is what an echo or a heredoc adds; anything
		// else the operator typed is theirs to keep.
		*password = strings.TrimRight(string(raw), "\r\n")
		if *password == "" {
			return fmt.Errorf("admin reset: --password-stdin was given nothing to read")
		}
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

	if *quiet {
		return nil
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
