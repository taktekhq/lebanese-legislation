package releasedb

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

// conninfo parses a libpq connection string, "key=value ..." or a
// postgres:// URL, into its settings.
func conninfo(dsn string) (map[string]string, error) {
	out := map[string]string{}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, errors.New("invalid database URL")
		}
		if u.User != nil {
			out["user"] = u.User.Username()
			if p, ok := u.User.Password(); ok {
				out["password"] = p
			}
		}
		if h := u.Hostname(); h != "" {
			out["host"] = h
		}
		if p := u.Port(); p != "" {
			out["port"] = p
		}
		if db := strings.TrimPrefix(u.Path, "/"); db != "" {
			out["dbname"] = db
		}
		for k, v := range u.Query() {
			if len(v) > 0 {
				out[k] = v[0]
			}
		}
		return out, nil
	}
	s := strings.TrimSpace(dsn)
	for s != "" {
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			return nil, errors.New("invalid connection string")
		}
		key := strings.TrimSpace(s[:eq])
		s = strings.TrimLeft(s[eq+1:], " \t\n")
		var val strings.Builder
		if strings.HasPrefix(s, "'") {
			s = s[1:]
			closed := false
			for len(s) > 0 {
				c := s[0]
				s = s[1:]
				if c == '\\' && len(s) > 0 {
					val.WriteByte(s[0])
					s = s[1:]
					continue
				}
				if c == '\'' {
					closed = true
					break
				}
				val.WriteByte(c)
			}
			if !closed {
				return nil, errors.New("invalid connection string")
			}
		} else {
			for len(s) > 0 && s[0] != ' ' && s[0] != '\t' && s[0] != '\n' {
				if s[0] == '\\' && len(s) > 1 {
					s = s[1:]
				}
				val.WriteByte(s[0])
				s = s[1:]
			}
		}
		out[key] = val.String()
		s = strings.TrimLeft(s, " \t\n")
	}
	return out, nil
}

var pgEnvNames = map[string]string{
	"dbname": "PGDATABASE", "user": "PGUSER", "password": "PGPASSWORD", "host": "PGHOST",
	"port": "PGPORT", "sslmode": "PGSSLMODE", "options": "PGOPTIONS", "connect_timeout": "PGCONNECT_TIMEOUT",
	"sslrootcert": "PGSSLROOTCERT", "sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY", "application_name": "PGAPPNAME",
}

// clientEnv is the environment for pg_dump or pg_restore: the current one
// with the connection's settings as PG* variables, so credentials never
// appear on a command line or in its error output.
func clientEnv(dsn string) ([]string, string, error) {
	settings, err := conninfo(dsn)
	if err != nil {
		return nil, "", err
	}
	env := os.Environ()
	for k, v := range settings {
		name, ok := pgEnvNames[k]
		if !ok {
			name = "PG" + strings.ToUpper(k)
		}
		env = append(env, name+"="+v)
	}
	db := settings["dbname"]
	if db == "" {
		db = "postgres"
	}
	return env, db, nil
}
