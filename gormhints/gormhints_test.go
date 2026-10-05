package gormhints_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"

	aviato "github.com/getaviato/aviato-go"
	"github.com/getaviato/aviato-go/gormhints"
)

type Company struct {
	ID        uint
	Name      string
	Customers []Customer
}

type Customer struct {
	ID             uint
	CompanyID      uint
	Company        Company
	BillingAddress *Address `gorm:"foreignKey:OwnerID"`
	Invoices       []Invoice
	Tags           []Tag  `gorm:"many2many:customer_tags"`
	Notes          []Note `gorm:"polymorphic:Owner"`
}

type Address struct {
	ID      uint
	OwnerID uint
}

type Invoice struct {
	ID         uint
	CustomerID uint
}

type Tag struct {
	ID   uint
	Name string
}

type Note struct {
	ID        uint
	OwnerID   uint
	OwnerType string
}

func TestRelationHintsFromGORM(t *testing.T) {
	hints, err := gormhints.RelationHintsFromGORM(&Customer{}, &Company{})
	require.NoError(t, err)
	assert.Equal(t, []aviato.RelationHint{
		{Collection: "customers", Name: "company", Kind: aviato.BelongsTo, ForeignKey: []string{"company_id"}, Target: "companies", TargetKey: []string{"id"}},
		{Collection: "customers", Name: "billing_address", Kind: aviato.HasOne, ForeignKey: []string{"owner_id"}, Target: "addresses", TargetKey: []string{"id"}},
		{Collection: "customers", Name: "invoices", Kind: aviato.HasMany, ForeignKey: []string{"customer_id"}, Target: "invoices", TargetKey: []string{"id"}},
		{Collection: "companies", Name: "customers", Kind: aviato.HasMany, ForeignKey: []string{"company_id"}, Target: "customers", TargetKey: []string{"id"}},
	}, hints, "many-to-many and polymorphic relations are skipped")
}

func TestRelationHintsWithNamer(t *testing.T) {
	hints, err := gormhints.RelationHintsWithNamer(schema.NamingStrategy{TablePrefix: "app_", SingularTable: true}, &Invoice{}, &Company{})
	require.NoError(t, err)
	require.Len(t, hints, 1)
	assert.Equal(t, "app_company", hints[0].Collection)
	assert.Equal(t, "app_customer", hints[0].Target)
}

func TestRelationHintsRejectsNonModels(t *testing.T) {
	_, err := gormhints.RelationHintsFromGORM(42)
	assert.Error(t, err)
}
