package main

import (
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
)

func Delete(name string) error {
	log.Info("delete")
	DeleteMetadata(name)
	log.Info("deletemeta done")

	dataFileName := filepath.Join(*dataDir, name)
	err := os.Remove(dataFileName)
	if err != nil {
		log.Info("failed os.remove: ", err)
		return err
	}

	triggers <- name
	log.Info("deleted ", name)
	return nil
}
