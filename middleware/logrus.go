package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

func LogrusMiddleware(logger *logrus.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		requestID := uuid.New().String()
		// Built from the injected logger rather than the package-level one, so a
		// caller passing a configured *logrus.Logger actually gets used.
		base := logrus.NewEntry(logger).WithFields(logrus.Fields{
			"request_id": requestID,
			"method":     c.Request.Method,
			"path":       c.Request.URL.Path,
		})

		c.Set("logger", base)
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)

		c.Next()

		// Re-read the logger from the context: auth.Identify replaces it with an
		// identity-enriched entry, so reading `base` here would log the completion
		// line without the user that made the request.
		final := base
		if v, ok := c.Get("logger"); ok {
			if e, ok := v.(*logrus.Entry); ok {
				final = e
			}
		}

		latency := time.Since(start)
		entry := final.WithFields(logrus.Fields{
			"status":  c.Writer.Status(),
			"query":   c.Request.URL.RawQuery,
			"ip":      c.ClientIP(),
			"latency": latency.String(),
		})

		if len(c.Errors) > 0 {
			entry.Error(c.Errors.String())
		} else {
			entry.Info("request completed")
		}
	}
}
