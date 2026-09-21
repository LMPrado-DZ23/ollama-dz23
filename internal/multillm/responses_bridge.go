package multillm

import "github.com/gin-gonic/gin"

// NativeChatMiddleware runs after Ollama's existing Responses converter. The
// converter owns Responses input/tool parsing and SSE output; this handler
// sends the resulting native chat to a configured provider instead of trying
// to load its namespaced ID as a local model. Native Responses passthrough
// remains available for providers explicitly configured for that protocol.
func (g *Gateway) NativeChatMiddleware() gin.HandlerFunc {
	forward := g.Middleware()
	return func(c *gin.Context) {
		original := c.Request
		request := original.Clone(original.Context())
		request.URL.Path = "/api/chat"
		request.URL.RawPath = ""
		c.Request = request
		defer func() { c.Request = original }()
		forward(c)
	}
}
