package postgres_test

import (
	"testing"

	"github.com/swchck/director/storage"
	pgstorage "github.com/swchck/director/storage/postgres"
)

func TestStorage_ImplementsStorageInterface(t *testing.T) {
	var _ storage.Storage = (*pgstorage.Storage)(nil)
}

func TestStorage_Construction(t *testing.T) {
	s := pgstorage.NewStorage(nil)
	if s == nil {
		t.Error("NewStorage returned nil")
	}
}

func TestMigrationSQL_IsNotEmpty(t *testing.T) {
	if pgstorage.MigrationSQL == "" {
		t.Error("MigrationSQL should not be empty")
	}
}
