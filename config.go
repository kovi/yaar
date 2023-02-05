package main

import (
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

type Trigger struct {
	Tag     string
	File    string
	Execute string
}

type Configuration struct {
	Triggers []Trigger
}

var Config = Configuration{}

func LoadConfig() error {
	log.Info("reading config.yml")
	b, err := os.ReadFile(filepath.Join(*configDir, "config.yaml"))
	if os.IsNotExist(err) {
		log.Info("config.yaml does not exist")
		return nil
	}
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(b, &Config)
	if err != nil {
		log.Fatal("failed to load config yaml")
		return err
	}

	log.Info("config is ", Config)
	return nil
}
