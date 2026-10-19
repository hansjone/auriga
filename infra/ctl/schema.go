package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// serviceSchemas lists platform service schemas (architecture table).
// Each gets role auriga_<schema> with rights only on that schema.
var serviceSchemas = []string{
	"iam", "org", "workspace", "resource", "knowledge", "collab", "audit", "ops", "ai",
}

type serviceRoleCred struct {
	User     string `json:"user"`
	Password string `json:"password"`
	Schema   string `json:"schema"`
}

type serviceRolesFile struct {
	Database string                     `json:"database"`
	Host     string                     `json:"host"`
	Port     uint32                     `json:"port"`
	Roles    map[string]serviceRoleCred `json:"roles"`
}

func serviceRolesPath(dataDir string) string {
	return filepath.Join(dataDir, "service-roles.json")
}

func roleName(schema string) string {
	return "auriga_" + schema
}

func loadOrCreateServicePasswords(dataDir string, port uint32) (serviceRolesFile, error) {
	path := serviceRolesPath(dataDir)
	body, err := os.ReadFile(path)
	if err == nil {
		var file serviceRolesFile
		if err := json.Unmarshal(body, &file); err != nil {
			return file, fmt.Errorf("invalid service-roles.json: %w", err)
		}
		if file.Roles == nil {
			return file, errors.New("invalid service-roles.json: missing roles")
		}
		for _, schema := range serviceSchemas {
			cred, ok := file.Roles[schema]
			if !ok || cred.User != roleName(schema) || cred.Schema != schema || cred.Password == "" {
				return file, fmt.Errorf("invalid service-roles.json: incomplete entry for %s", schema)
			}
			if _, decodeErr := hex.DecodeString(cred.Password); decodeErr != nil || len(cred.Password) != 64 {
				return file, fmt.Errorf("invalid service-roles.json: bad password for %s", schema)
			}
		}
		file.Database = "auriga"
		file.Host = "127.0.0.1"
		file.Port = port
		return file, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return serviceRolesFile{}, err
	}
	file := serviceRolesFile{
		Database: "auriga",
		Host:     "127.0.0.1",
		Port:     port,
		Roles:    make(map[string]serviceRoleCred, len(serviceSchemas)),
	}
	for _, schema := range serviceSchemas {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return serviceRolesFile{}, err
		}
		file.Roles[schema] = serviceRoleCred{
			User:     roleName(schema),
			Password: hex.EncodeToString(secret),
			Schema:   schema,
		}
	}
	encoded, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return serviceRolesFile{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return serviceRolesFile{}, err
	}
	_, writeErr := f.Write(append(encoded, '\n'))
	return file, errors.Join(writeErr, f.Close())
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

// ensureServiceRoles creates per-service schemas and login roles (idempotent).
// Roles are never superuser and never get BYPASSRLS or cross-schema grants.
func ensureServiceRoles(db *sql.DB, dataDir string, port uint32) error {
	file, err := loadOrCreateServicePasswords(dataDir, port)
	if err != nil {
		return err
	}
	for _, schema := range serviceSchemas {
		cred := file.Roles[schema]
		role := cred.User
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			sqlText := fmt.Sprintf(
				`CREATE ROLE %s LOGIN PASSWORD %s NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS`,
				quoteIdent(role), quoteLiteral(cred.Password),
			)
			if _, err := db.Exec(sqlText); err != nil {
				return fmt.Errorf("create role %s: %w", role, err)
			}
		} else {
			// Keep password in sync with the credentials file after restore/recreate.
			sqlText := fmt.Sprintf(`ALTER ROLE %s PASSWORD %s`, quoteIdent(role), quoteLiteral(cred.Password))
			if _, err := db.Exec(sqlText); err != nil {
				return fmt.Errorf("update role password %s: %w", role, err)
			}
			var bypass, super bool
			if err := db.QueryRow(
				`SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname = $1`, role,
			).Scan(&bypass, &super); err != nil {
				return err
			}
			if bypass || super {
				return fmt.Errorf("role %s has elevated privileges; refusing to continue", role)
			}
		}
		if _, err := db.Exec(fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s AUTHORIZATION %s`, quoteIdent(schema), quoteIdent(role))); err != nil {
			return fmt.Errorf("create schema %s: %w", schema, err)
		}
		if _, err := db.Exec(fmt.Sprintf(`ALTER SCHEMA %s OWNER TO %s`, quoteIdent(schema), quoteIdent(role))); err != nil {
			return fmt.Errorf("own schema %s: %w", schema, err)
		}
		if _, err := db.Exec(fmt.Sprintf(
			`GRANT USAGE, CREATE ON SCHEMA %s TO %s`, quoteIdent(schema), quoteIdent(role),
		)); err != nil {
			return err
		}
		if _, err := db.Exec(fmt.Sprintf(
			`ALTER ROLE %s SET search_path TO %s`, quoteIdent(role), quoteIdent(schema),
		)); err != nil {
			return err
		}
		// Future objects created by the role stay usable by that role only.
		if _, err := db.Exec(fmt.Sprintf(
			`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA %s GRANT ALL ON TABLES TO %s`,
			quoteIdent(role), quoteIdent(schema), quoteIdent(role),
		)); err != nil {
			return err
		}
		if _, err := db.Exec(fmt.Sprintf(
			`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA %s GRANT ALL ON SEQUENCES TO %s`,
			quoteIdent(role), quoteIdent(schema), quoteIdent(role),
		)); err != nil {
			return err
		}
	}
	// Persist port/host if the file was loaded from an older run.
	encoded, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(serviceRolesPath(dataDir), append(encoded, '\n'), 0600)
}
