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
		logger := logrus.WithFields(logrus.Fields{
			"request_id": requestID,
			"method":     c.Request.Method,
			"path":       c.Request.URL.Path,
		})

		c.Set("logger", logger)
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)

		c.Next()

		latency := time.Since(start)
		entry := logger.WithFields(logrus.Fields{
			"status":  c.Writer.Status(),
			"method":  c.Request.Method,
			"path":    c.Request.URL.Path,
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
