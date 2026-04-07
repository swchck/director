package postgres_test

import (
	"testing"

	"github.com/swchck/director/registry"
	pgregistry "github.com/swchck/director/registry/postgres"
)

func TestRegistry_ImplementsRegistryInterface(t *testing.T) {
	var _ registry.Registry = (*pgregistry.Registry)(nil)
}
