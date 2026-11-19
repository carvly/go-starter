//go:build integration

package postgres_test

import (
	"testing"

	"app.local/internal/modules/staff"
	"app.local/internal/modules/staff/domain"
	"app.local/internal/modules/staff/domain/domaintest"
	"app.local/internal/modules/staff/postgres"
	"app.local/internal/platform/postgres/pgtest"
)

func TestRepository_Contract(t *testing.T) {
	domaintest.RunRepositoryContract(t, func(t *testing.T) domain.Repository {
		return postgres.NewRepository(pgtest.Handle(t, staff.Migrations))
	})
}
