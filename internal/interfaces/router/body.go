package router

import (
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
)

func httpMax(c *gin.Context) io.ReadCloser {
	return http.MaxBytesReader(c.Writer, c.Request.Body, 6<<20)
}
