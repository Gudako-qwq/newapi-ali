package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Enabled  bool   `json:"enabled"`
}

type Channel struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"user_id"`
	Username     string `json:"username"`
	Name         string `json:"name"`
	TemplateID   string `json:"template_id"`
	Models       string `json:"models"`
	State        string `json:"state"`
	CanArchive   bool   `json:"can_archive"`
	Status       int    `json:"status"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	UpstreamID   int64  `json:"-"`
	UpstreamName string `json:"-"`
	RequestHash  string `json:"-"`
}

func openDatabase(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	_, err = database.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS users (
 id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE COLLATE NOCASE,
 password_hash TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('admin','user')),
 enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
 token_hash TEXT PRIMARY KEY, csrf TEXT NOT NULL, user_id INTEGER NOT NULL REFERENCES users(id),
 expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS local_bootstrap_passwords (
 user_id INTEGER PRIMARY KEY REFERENCES users(id)
);
CREATE TABLE IF NOT EXISTS audit (
 id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, action TEXT NOT NULL,
 channel_id INTEGER NOT NULL DEFAULT 0, outcome TEXT NOT NULL, created_at INTEGER NOT NULL
);
DELETE FROM sessions WHERE expires_at <= unixepoch();
`)
	if err == nil {
		err = migrateChannelNames(database)
	}
	if err != nil {
		database.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
}

func migrateChannelNames(database *sql.DB) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists, uniqueName bool
	err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='channels'), EXISTS(SELECT 1 FROM pragma_index_list('channels') idx JOIN pragma_index_info(idx.name) col WHERE idx."unique"=1 AND col.name='upstream_name')`).Scan(&exists, &uniqueName)
	if err != nil {
		return err
	}
	if !exists || uniqueName {
		if exists {
			if _, err = tx.Exec(`ALTER TABLE channels RENAME TO channels_before_original_names`); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`CREATE TABLE channels (
 id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), name TEXT NOT NULL,
 template_id TEXT NOT NULL, models TEXT NOT NULL, upstream_id INTEGER UNIQUE,
 upstream_name TEXT NOT NULL,
 request_key TEXT NOT NULL, request_hash TEXT NOT NULL, state TEXT NOT NULL,
 status INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(user_id, request_key))`); err != nil {
			return err
		}
		if exists {
			if _, err = tx.Exec(`INSERT INTO channels(id,user_id,name,template_id,models,upstream_id,upstream_name,request_key,request_hash,state,status,created_at,updated_at)
 SELECT id,user_id,name,template_id,models,upstream_id,upstream_name,request_key,request_hash,state,status,created_at,updated_at FROM channels_before_original_names;
 DROP TABLE channels_before_original_names`); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS channels_owner ON channels(user_id,id); UPDATE channels SET state='uncertain' WHERE state='creating'`); err != nil {
		return err
	}
	return tx.Commit()
}

func bootstrapAdmin(database *sql.DB, config Config) error {
	localException := localBootstrapAllowed(config)
	if config.AllowWeakBootstrapPassword && !localException {
		return errors.New("local bootstrap password exception cannot be used outside loopback development")
	}
	var localOnly int
	if err := database.QueryRow("SELECT COUNT(*) FROM local_bootstrap_passwords").Scan(&localOnly); err != nil {
		return err
	}
	if localOnly > 0 && !localException {
		return errors.New("change the local development administrator password before deploying this database to a server")
	}
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	strongPassword := validPassword(config.AdminPassword)
	localPassword := localException && utf8.RuneCountInString(config.AdminPassword) >= 8 && utf8.RuneCountInString(config.AdminPassword) <= 128 && len(config.AdminPassword) <= 512
	if !usernamePattern.MatchString(config.AdminUsername) || (!strongPassword && !localPassword) {
		return errors.New("first launch requires a valid BOOTSTRAP_ADMIN_USERNAME and a 15-128 character password (8-128 only with the explicit loopback development exception)")
	}
	hash, err := hashPassword(config.AdminPassword)
	if err != nil {
		return err
	}
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.Exec("INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,'admin',?)", config.AdminUsername, hash, time.Now().Unix())
	if err != nil {
		return err
	}
	if !strongPassword {
		id, idErr := result.LastInsertId()
		if idErr != nil {
			return idErr
		}
		if _, err := transaction.Exec("INSERT INTO local_bootstrap_passwords(user_id) VALUES(?)", id); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

const channelSelect = `SELECT c.id,c.user_id,u.username,c.name,c.template_id,c.models,c.state,c.status,c.created_at,c.updated_at,COALESCE(c.upstream_id,0),c.upstream_name,c.request_hash FROM channels c JOIN users u ON u.id=c.user_id`

type scanner interface{ Scan(...any) error }

func scanChannel(row scanner) (Channel, error) {
	var channel Channel
	err := row.Scan(&channel.ID, &channel.UserID, &channel.Username, &channel.Name, &channel.TemplateID, &channel.Models, &channel.State, &channel.Status, &channel.CreatedAt, &channel.UpdatedAt, &channel.UpstreamID, &channel.UpstreamName, &channel.RequestHash)
	channel.CanArchive = channel.State == "rejected" && channel.UpstreamID == 0
	return channel, err
}

func (app *App) ownedChannel(user User, id int64) (Channel, error) {
	return scanChannel(app.database.QueryRow(channelSelect+" WHERE c.id=? AND (c.user_id=? OR ?='admin')", id, user.ID, user.Role))
}

func (app *App) audit(userID int64, action string, channelID int64, outcome string) {
	if _, err := app.database.Exec("INSERT INTO audit(user_id,action,channel_id,outcome,created_at) VALUES(?,?,?,?,?)", userID, action, channelID, outcome, time.Now().Unix()); err != nil {
		app.logger.Print("audit persistence failed")
	}
}
