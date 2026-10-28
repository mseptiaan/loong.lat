package platform

import (
	"slices"
	"testing"
	"testing/fstest"
)

func TestMigrationsFromFSReadsSQLFilesInNameOrder(t *testing.T) {
	fsys := fstest.MapFS{
		"0010_late.sql":       {Data: []byte("SELECT 10;")},
		"0002_second.sql":     {Data: []byte("SELECT 2;")},
		"0001_first.sql":      {Data: []byte("SELECT 1;")},
		"README.md":           {Data: []byte("not a migration")},
		"sub/0003_nested.sql": {Data: []byte("SELECT 3;")},
	}
	got, err := MigrationsFromFS("console", fsys)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if want := []string{"console/0001_first", "console/0002_second", "console/0010_late"}; !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	if got[0].SQL != "SELECT 1;" {
		t.Errorf("SQL = %q", got[0].SQL)
	}
}

func TestPlatformMigrationsHaveStableUniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Migrations() {
		if m.ID == "" || m.SQL == "" || seen[m.ID] {
			t.Errorf("bad migration %+v", m)
		}
		seen[m.ID] = true
	}
	if len(seen) != 2 {
		t.Errorf("want the sessions and idempotency migrations, got %v", seen)
	}
}
