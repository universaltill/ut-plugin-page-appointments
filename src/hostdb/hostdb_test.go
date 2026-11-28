//go:build !wasip1

package hostdb_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/universaltill/universal-till/sdk/plugin"
	"github.com/universaltill/ut-plugin-page-appointments/src/hostdb"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
	"github.com/universaltill/ut-plugin-page-appointments/src/store/memdb"
)

// The adapter is a pure pass-through: the repository running over the SDK's
// fake host (backed by memdb) behaves as it does over memdb directly.
func TestRepositoryOverHostFunctions(t *testing.T) {
	h := plugin.NewFakeHost()
	mem := memdb.New()
	h.DBExec = func(sql string, args []any) (int64, error) { return mem.Exec(context.Background(), sql, args...) }
	h.DBQuery = func(sql string, args []any) ([]byte, error) { return mem.Query(context.Background(), sql, args...) }
	plugin.UseFakeHost(t, h)

	var db store.DB = hostdb.New()
	if _, err := db.Exec(context.Background(), store.SQLUpsertSetting, "currency", "EUR"); err != nil {
		t.Fatal(err)
	}
	b, err := db.Query(context.Background(), store.SQLListSettings)
	if err != nil || !strings.Contains(string(b), `"EUR"`) {
		t.Fatalf("query = %s %v", b, err)
	}
}

func TestDeniedAndCancelled(t *testing.T) {
	h := plugin.NewFakeHost()
	h.DBExec = func(string, []any) (int64, error) { return 0, errors.New("boom") }
	plugin.UseFakeHost(t, h)
	if _, err := hostdb.New().Exec(context.Background(), "x"); err == nil {
		t.Fatal("expected an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hostdb.New().Query(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx: %v", err)
	}
}
