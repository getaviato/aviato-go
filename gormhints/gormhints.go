// Package gormhints derives Aviato relation hints from GORM models, for databases whose
// relations are declared in code rather than with foreign key constraints.
package gormhints

import (
	"fmt"
	"sync"

	"gorm.io/gorm/schema"

	aviato "github.com/getaviato/aviato-go"
)

// RelationHintsFromGORM parses models (pointers to GORM model structs) with gorm's schema
// parser and returns their belongs-to, has-one and has-many relations as relation hints.
// Collections are table names and relation names the snake_case field names, following gorm's
// default naming strategy. Many-to-many and polymorphic relations are skipped: the agent
// introspects join tables itself.
//
//	hints, err := gormhints.RelationHintsFromGORM(&Customer{}, &Invoice{})
//	plugin.RelationHints(hints...)
func RelationHintsFromGORM(models ...any) ([]aviato.RelationHint, error) {
	return RelationHintsWithNamer(schema.NamingStrategy{}, models...)
}

// RelationHintsWithNamer is [RelationHintsFromGORM] with the naming strategy your gorm.Config
// uses (table prefixes, singular tables…).
func RelationHintsWithNamer(namer schema.Namer, models ...any) ([]aviato.RelationHint, error) {
	cache := &sync.Map{}
	var hints []aviato.RelationHint
	for _, model := range models {
		parsed, err := schema.Parse(model, cache, namer)
		if err != nil {
			return nil, fmt.Errorf("gormhints: parse %T: %w", model, err)
		}
		groups := []struct {
			kind          aviato.RelationKind
			relationships []*schema.Relationship
		}{
			{aviato.BelongsTo, parsed.Relationships.BelongsTo},
			{aviato.HasOne, parsed.Relationships.HasOne},
			{aviato.HasMany, parsed.Relationships.HasMany},
		}
		for _, group := range groups {
			for _, relationship := range group.relationships {
				if hint, ok := hintOf(namer, parsed, group.kind, relationship); ok {
					hints = append(hints, hint)
				}
			}
		}
	}
	return hints, nil
}

func hintOf(namer schema.Namer, owner *schema.Schema, kind aviato.RelationKind, relationship *schema.Relationship) (aviato.RelationHint, bool) {
	if relationship.Polymorphic != nil || relationship.FieldSchema == nil || len(relationship.References) == 0 {
		return aviato.RelationHint{}, false
	}
	hint := aviato.RelationHint{
		Collection: owner.Table,
		Name:       namer.ColumnName("", relationship.Name),
		Kind:       kind,
		Target:     relationship.FieldSchema.Table,
	}
	// References pair the referencing column (ForeignKey) with the referenced one (PrimaryKey),
	// whichever side declares the relation, which is what the protocol expects.
	for _, reference := range relationship.References {
		if reference.ForeignKey == nil || reference.PrimaryKey == nil {
			return aviato.RelationHint{}, false
		}
		hint.ForeignKey = append(hint.ForeignKey, reference.ForeignKey.DBName)
		hint.TargetKey = append(hint.TargetKey, reference.PrimaryKey.DBName)
	}
	return hint, true
}
