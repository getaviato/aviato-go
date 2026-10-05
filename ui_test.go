package aviato_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	aviato "github.com/getaviato/aviato-go"
)

func TestUIWireParity(t *testing.T) {
	for _, fixture := range []string{"ui-v1.json", "ui-overview-v1.json"} {
		t.Run(fixture, func(t *testing.T) {
			content, err := os.ReadFile("testdata/" + fixture)
			require.NoError(t, err)
			var document map[string]any
			require.NoError(t, json.Unmarshal(content, &document))
			p, err := aviato.New(aviato.Options{Secret: secret})
			require.NoError(t, err)
			p.Summary("customers", document).Action("customers", "Example", aviato.ScopeSingle,
				func(_ context.Context, a *aviato.ActionContext) (aviato.Result, error) { return a.Success("ok"), nil },
				aviato.WithForm(map[string]any{"type": "object"}), aviato.WithFormUI(aviato.UIForm(document)))
			manifest, err := p.Manifest()
			require.NoError(t, err)
			require.Equal(t, document, manifest.Summaries[0].Document.AsMap())
			require.Equal(t, document, manifest.Actions[0].FormUiSchema.AsMap()["document"])
			p.Summary("customers", aviato.UIDocument(aviato.UIComponent("Text", map[string]any{"value": aviato.UIBind("/record/name")})))
			manifest, err = p.Manifest()
			require.NoError(t, err)
			require.Len(t, manifest.Summaries, 1)
		})
	}
}
