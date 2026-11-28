package store_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cliDB runs each statement through the sqlite3 command on a database file,
// binding ?N parameters with .parameter set. It is a real SQLite behind the
// store.DB shape; tests using it skip when sqlite3 is not installed.
type cliDB struct {
	t    testing.TB
	path string
}

func newCLIDB(t testing.TB) *cliDB {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not installed")
	}
	return &cliDB{t: t, path: filepath.Join(t.TempDir(), "plugin.db")}
}

func (c *cliDB) run(script string) ([]byte, error) {
	cmd := exec.Command("sqlite3", "-bail", "-json", c.path)
	cmd.Stdin = strings.NewReader(script)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sqlite3: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

func bind(args []any) string {
	var sb strings.Builder
	for i, a := range args {
		switch v := a.(type) {
		case string:
			fmt.Fprintf(&sb, ".parameter set ?%d \"CAST(x'%s' AS TEXT)\"\n", i+1, hex.EncodeToString([]byte(v)))
		case int, int64, bool:
			n := 0
			switch x := v.(type) {
			case int:
				n = x
			case int64:
				n = int(x)
			case bool:
				if x {
					n = 1
				}
			}
			fmt.Fprintf(&sb, ".parameter set ?%d %d\n", i+1, n)
		default:
			panic(fmt.Sprintf("cliDB: arg type %T", a))
		}
	}
	return sb.String()
}

// exec applies a migration or any script without parameters.
func (c *cliDB) script(sql string) error { _, err := c.run(sql); return err }

func (c *cliDB) Exec(_ context.Context, q string, args ...any) (int64, error) {
	out, err := c.run(bind(args) + q + ";\nSELECT changes() AS n;\n")
	if err != nil {
		return 0, err
	}
	var n int64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), `[{"n":%d}]`, &n); err != nil {
		return 0, fmt.Errorf("cliDB: changes() output %q", out)
	}
	return n, nil
}

func (c *cliDB) Query(_ context.Context, q string, args ...any) ([]byte, error) {
	out, err := c.run(bind(args) + q + ";\n")
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return []byte("[]"), nil
	}
	return out, nil
}
