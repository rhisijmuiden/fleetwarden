package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PostgreSQL client tools. Debian installs them per major version under
// /usr/lib/postgresql/<n>/bin; the newest can dump and restore older servers.
func pgTool(name string) (string, error) {
	dirs, _ := filepath.Glob("/usr/lib/postgresql/*/bin/" + name)
	sort.Slice(dirs, func(i, j int) bool { return pgMajor(dirs[i]) > pgMajor(dirs[j]) })
	if len(dirs) > 0 {
		return dirs[0], nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found (install postgresql-client)", name)
	}
	return p, nil
}

func pgMajor(p string) int {
	n, _ := strconv.Atoi(filepath.Base(filepath.Dir(filepath.Dir(p))))
	return n
}

// pgEnv moves the password from the connection URL into PGPASSWORD, so it
// does not show up in the process list.
func pgEnv(dbURL string) (string, []string, error) {
	u, err := url.Parse(dbURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", nil, errors.New("invalid database URL")
	}
	env := append(os.Environ(), "PGCONNECT_TIMEOUT=15")
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
			u.User = url.User(u.User.Username())
		}
	}
	return u.String(), env, nil
}

// Tables whose content is not backed up: sessions (a restore signs everyone
// out), the backup history (it would be stale after a restore) and the
// public vulnerability data (read again from the sources after a restore).
var dumpExcludeData = []string{"sessions", "backup_runs", "vulnerabilities", "vuln_ranges", "vuln_debian", "vuln_msrc", "vuln_sources"}

// DumpDatabase writes a pg_dump custom-format archive (uncompressed, so
// chunks deduplicate between backups; chunks are compressed afterwards).
func DumpDatabase(ctx context.Context, dbURL, out string) error {
	tool, err := pgTool("pg_dump")
	if err != nil {
		return err
	}
	conn, env, err := pgEnv(dbURL)
	if err != nil {
		return err
	}
	args := []string{"--format=custom", "--compress=0", "--no-owner", "--no-privileges", "--file=" + out}
	for _, t := range dumpExcludeData {
		args = append(args, "--exclude-table-data=public."+t)
	}
	args = append(args, "--dbname="+conn)
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dump: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// RestoreDatabase replaces the public schema with the content of a dump, in
// one transaction: on any error the database stays as it was.
func RestoreDatabase(ctx context.Context, dbURL, dump string) error {
	pgRestore, err := pgTool("pg_restore")
	if err != nil {
		return err
	}
	psql, err := pgTool("psql")
	if err != nil {
		return err
	}
	conn, env, err := pgEnv(dbURL)
	if err != nil {
		return err
	}
	gen := exec.CommandContext(ctx, pgRestore, "--no-owner", "--no-privileges", "--file=-", dump)
	gen.Env = env
	var genErr bytes.Buffer
	gen.Stderr = &genErr
	sqlOut, err := gen.StdoutPipe()
	if err != nil {
		return err
	}
	apply := exec.CommandContext(ctx, psql, "--no-psqlrc", "--quiet", "--single-transaction", "--set=ON_ERROR_STOP=1",
		"--dbname="+conn, "--file=-")
	apply.Env = env
	var applyErr bytes.Buffer
	apply.Stderr = &applyErr
	apply.Stdout = io.Discard
	stdin, err := apply.StdinPipe()
	if err != nil {
		return err
	}
	if err := apply.Start(); err != nil {
		return err
	}
	if err := gen.Start(); err != nil {
		stdin.Close()
		_ = apply.Wait()
		return err
	}
	// Start from an empty schema so tables from newer migrations do not survive.
	_, werr := io.WriteString(stdin, "DROP SCHEMA IF EXISTS public CASCADE;\nCREATE SCHEMA public;\n")
	if werr == nil {
		_, werr = io.Copy(stdin, sqlOut)
	}
	stdin.Close()
	genWait := gen.Wait()
	applyWait := apply.Wait()
	if genWait != nil {
		return fmt.Errorf("pg_restore: %v: %s", genWait, strings.TrimSpace(genErr.String()))
	}
	if applyWait != nil || werr != nil {
		msg := strings.TrimSpace(applyErr.String())
		if msg == "" && werr != nil {
			msg = werr.Error()
		}
		return fmt.Errorf("restore database: %s", msg)
	}
	return nil
}
