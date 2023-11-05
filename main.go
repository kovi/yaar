package main

import (
	"flag"
	"strconv"

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

	router := router()
	router.Run(":" + strconv.Itoa(*port))

	close(qTriggers)
	close(qExpiry)
}
