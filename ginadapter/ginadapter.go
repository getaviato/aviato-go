// Package ginadapter mounts an Aviato plugin on a gin engine.
package ginadapter

import (
	"github.com/gin-gonic/gin"

	aviato "github.com/getaviato/aviato-go"
)

// Mount serves plugin under its base path (for example /aviato/*) on router, which may be a
// *gin.Engine or a *gin.RouterGroup. The plugin needs a non-empty [aviato.Options.BasePath]
// unless it is the only route of the router.
func Mount(router gin.IRoutes, plugin *aviato.Plugin) {
	router.Any(plugin.BasePath()+"/*aviato", gin.WrapH(plugin.Handler()))
}
