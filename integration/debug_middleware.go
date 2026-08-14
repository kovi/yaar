package integration

import (
	"bytes"
	"io"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
)

func DebugMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Log request
		var reqBody []byte
		if c.Request.Body != nil {
			reqBody, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(reqBody)) // restore
		}

		if len(reqBody) < 1024 || isJSON(c.ContentType()) {
			log.Printf("--> %s %s\nbody: %s", c.Request.Method, c.Request.URL, string(reqBody))
		} else {
			log.Printf("--> %s %s (body: %d bytes)", c.Request.Method, c.Request.URL, len(reqBody))
		}

		// Capture response
		rw := &responseCapture{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = rw

		c.Next()

		body := rw.body.Bytes()
		if len(body) < 1024 || isJSON(rw.Header().Get("Content-Type")) {
			log.Printf("<-- %d %s\nbody: %s", rw.status, c.Request.URL, string(body))
		} else {
			log.Printf("<-- %d %s (body: %d bytes)", rw.status, c.Request.URL, len(body))
		}
	}
}

func isJSON(ct string) bool {
	return strings.Contains(ct, "application/json")
}

type responseCapture struct {
	gin.ResponseWriter
	body   *bytes.Buffer
	status int
}

func (r *responseCapture) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func (r *responseCapture) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
