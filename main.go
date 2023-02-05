package main

import (
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

var (
	port      = flag.Int("port", 8080, "port to listen on")
	configDir = flag.String("config", "yaar", "config directory")
	dataDir   = flag.String("data", "yaar/data", "data directory")
)

func main() {
	flag.Parse()

	log.SetLevel(log.DebugLevel)
	log.Info("Starting...")

	LoadConfig()
	LoadMetadata()

	qTriggers := StartTriggers()
	qExpiry := StartExpiry()

	router := gin.Default()

	router.DELETE("/meta/*name", func(c *gin.Context) {
		name := c.Param("name")
		slash := strings.LastIndex(name, "/")
		if slash < 0 {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		key := name[slash+1:]
		name = name[:slash]

		if key != "locks" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		m, ok := GetMetadata(name)
		if !ok {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		m.Locks = m.Locks[:0]
		SetMetadata(name, m)
	})

	router.POST("/*name", func(c *gin.Context) {
		name := c.Param("name")
		log.Info("post of ", name)

		b := c.Request.Body
		if b == nil {
			log.Info("No body")
			c.Status(http.StatusBadRequest)
			return
		}

		dataFileName := filepath.Join(*dataDir, name)
		_, err := os.Stat(dataFileName)
		if !os.IsNotExist(err) {
			log.Info("File ", dataFileName, " already exists")
			c.String(http.StatusBadRequest, "already exists")
			return
		}

		m := Metadata{
			Added: time.Now(),
		}

		m.Tags = c.Request.Header.Values("x-tag")
		m.Locks = c.Request.Header.Values("x-lock")

		expire := c.Request.Header.Get("x-expire")
		if expire != "" {
			d, err := time.ParseDuration(expire)
			if err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			m.Expires = d
		}

		err = SetMetadata(name, m)
		if err != nil {
			log.Info("put metadata error: ", err)
			c.Status(http.StatusInternalServerError)
			return
		}

		f, err := os.Create(dataFileName)
		if err != nil {
			log.Info("put error: ", err)
			c.Status(http.StatusInternalServerError)
			return
		}
		defer f.Close()

		n, err := io.Copy(f, b)
		if err != nil {
			log.Info("put error in copy: ", err)
			c.Status(http.StatusInternalServerError)
			return
		}

		log.Print("Wrote ", n, " bytes")
		c.Status(http.StatusOK)

		triggers <- name
	})
	router.Use(Serve("/", *dataDir))

	router.Run(":" + strconv.Itoa(*port))

	close(qTriggers)
	close(qExpiry)
}
