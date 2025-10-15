package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

var (
	// Pattern matching for WebRTC-related paths
	reWebRTCPath = regexp.MustCompile(`^/[^/]+/(whip|whep|publish|publisher\.js|reader\.js)`)
)

// onWebRTCFallback handles WebRTC requests that don't match API routes
// This is registered with router.NoRoute() to catch WebRTC-specific paths
func (a *APIV2) onWebRTCFallback(ctx *gin.Context) {
	path := ctx.Request.URL.Path

	// Check if this is a WebRTC-related request
	// WebRTC paths: /<path>/whip, /<path>/whep, /<path>/publish, /<path>/publisher.js, /<path>/reader.js
	// Also handle /<path>/ for read page and /<path> for redirects
	if isWebRTCRequest(path) {
		// Get WebRTC handler from WebRTC server
		if a.WebRTCServer != nil {
			handler := a.WebRTCServer.GetHTTPHandler()
			if handler != nil {
				// Proxy request to WebRTC handler
				handler(ctx)
				return
			}
		}

		// Fallback: WebRTC server not available or handler not accessible
		// This is the old behavior - warn and return error
		/*
			a.Log(logger.Warn, "WebRTC request received at API port: %s", path)
			a.Log(logger.Warn, "Please configure WebRTC to use a separate port, or access WebRTC at its dedicated port")

			a.writeError(ctx, http.StatusNotFound, fmt.Errorf(
				"WebRTC requests should be sent to the WebRTC server port. "+
				"This is the API port. Please check your WebRTC server configuration."))
		*/

		// If we get here, WebRTC server is not initialized properly
		a.writeError(ctx, http.StatusServiceUnavailable, fmt.Errorf(
			"WebRTC service is not available"))
		return
	}

	// Not a WebRTC request, return 404
	ctx.JSON(404, gin.H{
		"error": "Not Found",
	})
}

// isWebRTCRequest checks if the request path is WebRTC-related
func isWebRTCRequest(path string) bool {
	// Match WebRTC patterns
	if reWebRTCPath.MatchString(path) {
		return true
	}

	// Also check for read page (/<path>/) or path redirect (/<path>)
	// But exclude API paths that start with /v2/
	if strings.HasPrefix(path, "/v2/") || strings.HasPrefix(path, "/res/") {
		return false
	}

	// If it's a path with 1-2 segments and ends with / or no trailing slash
	// it could be a WebRTC read/publish page
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) <= 2 && len(segments) > 0 {
		// Could be /<path>/ or /<path>
		return true
	}

	return false
}
