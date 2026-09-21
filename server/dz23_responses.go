package server

import (
	"github.com/gin-gonic/gin"
	"github.com/ollama/ollama/internal/multillm"
	"net"
	"net/http"
)

func (s *Server) responsesProviderMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.multiProvider == nil {
			c.Next()
			return
		}
		s.multiProvider.NativeChatMiddleware()(c)
	}
}

func (s *Server) dz23RegistryStatus(c *gin.Context) {
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	c.Header("Cache-Control", "no-store")
	models := []multillm.Model{}
	if s.multiRegistry != nil {
		models = s.multiRegistry.Models()
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}
