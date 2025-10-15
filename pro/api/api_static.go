package api

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
)

//go:embed web
var webFS embed.FS

//go:embed API_DOCS.md
var apiV2DocsMarkdown []byte

//go:embed doc/mrls.js
var mrlsJS []byte

//go:embed doc/apiv2_docs_public.html
var apiV2DocsHTML []byte

// mustFS returns the embedded web filesystem
func mustFS() http.FileSystem {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}

// setupStaticRoutes sets up static file routes for the admin panel and docs
func (a *APIV2) setupStaticRoutes(router *gin.Engine) {
	// Check if admin page is enabled
	a.mutex.RLock()
	enableAdminPage := a.Conf.API
	a.mutex.RUnlock()

	if !enableAdminPage {
		return
	}

	// Admin panel routes with authentication
	adminGroup := router.Group("/admin")
	adminGroup.Use(a.middlewareAuth) // Reuse existing auth middleware

	// Serve embedded React app (or simple HTML for now)
	adminGroup.StaticFS("/", mustFS())

	// MRLS JavaScript library endpoint (no additional auth, uses main middleware)
	router.GET("/mrls.js", func(ctx *gin.Context) {
		ctx.Header("Content-Type", "application/javascript; charset=utf-8")
		ctx.Data(http.StatusOK, "application/javascript; charset=utf-8", mrlsJS)
	})

	// API documentation HTML endpoint (no additional auth, uses main middleware)
	router.GET("/docs", func(ctx *gin.Context) {
		ctx.Header("Content-Type", "text/html; charset=utf-8")
		ctx.Data(http.StatusOK, "text/html; charset=utf-8", apiV2DocsHTML)
	})

	// Legacy markdown docs endpoint
	router.GET("/docs/markdown", func(ctx *gin.Context) {
		ctx.Header("Content-Type", "text/markdown; charset=utf-8")
		ctx.Data(http.StatusOK, "text/markdown; charset=utf-8", apiV2DocsMarkdown)
	})

	a.Log(logger.Info, "Static routes registered: /admin/, /mrls.js, /docs, /docs/markdown")
}
