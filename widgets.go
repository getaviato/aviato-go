package aviato

import (
	"fmt"
	"regexp"
)

// IntegrationPlugin declares a customer-side provider integration. Use snapshots its widgets.
type IntegrationPlugin interface {
	Widgets() []WidgetDefinition
}

// WidgetDefinition is the common metadata contract for integration authors, never credentials.
type WidgetDefinition struct {
	Collection      string
	Name            string
	Title           string
	Provider        string
	Resource        string
	BindingField    string
	BindingRelation string
	CredentialEnv   string
	ReadOnly        bool
	Account         string
}

// RecordBinding selects a column on this record or on one belongsTo relation.
type RecordBinding struct {
	Field    string
	Relation string
}

var credentialEnvPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
var providerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func validateWidget(definition WidgetDefinition) error {
	if !definition.ReadOnly || !credentialEnvPattern.MatchString(definition.CredentialEnv) {
		return fmt.Errorf("widgets require read-only credentials referenced by environment variable name")
	}
	for _, value := range []string{definition.Collection, definition.Name, definition.BindingField} {
		if value == "" || len(value) > 128 || value == "__proto__" || value == "constructor" || value == "prototype" {
			return fmt.Errorf("invalid widget collection, name or binding field")
		}
	}
	if len(definition.Account) > 128 || len(definition.Title) > 160 || len(definition.BindingRelation) > 128 || !providerNamePattern.MatchString(definition.Provider) || !providerNamePattern.MatchString(definition.Resource) {
		return fmt.Errorf("invalid widget metadata")
	}
	return nil
}

// Use atomically installs a snapshot of an integration. Re-registering replaces named widgets.
func (p *Plugin) Use(integration IntegrationPlugin) error {
	definitions := append([]WidgetDefinition(nil), integration.Widgets()...)
	for _, definition := range definitions {
		if err := validateWidget(definition); err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, definition := range definitions {
		p.widgets.set(definition.Collection+"\x00"+definition.Name, definition)
	}
	return nil
}
