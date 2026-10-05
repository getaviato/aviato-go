// Package echoadapter mounts an Aviato plugin on an echo (v5) server.
package echoadapter

import (
	"github.com/labstack/echo/v5"

	aviato "github.com/getaviato/aviato-go"
)

// Router is implemented by *echo.Echo and *echo.Group.
type Router interface {
	Any(path string, handler echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
}

// Mount serves plugin under its base path (for example /aviato/*) on router.
func Mount(router Router, plugin *aviato.Plugin) {
	router.Any(plugin.BasePath()+"/*", echo.WrapHandler(plugin.Handler()))
}
