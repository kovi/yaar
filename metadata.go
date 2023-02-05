package main

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

type Metadata struct {
	Added   time.Time
	Expires time.Duration
	Tags    []string

	// Locks can prevent removing an item
	// Currently a single lock is supported, which is an exclusive lock and can be applied to one item only.
	// When an item gets the lock that given lock is removed from the other item, making that no longer locked by the given key
	Locks []string
}

var metadb = map[string]Metadata{}
var metalock = sync.RWMutex{}

func deleteKey(name string) error {
	metalock.Lock()
	defer metalock.Unlock()
	_, ok := metadb[name]
	if !ok {
		return nil
	}

	delete(metadb, name)
	return nil
}

func DeleteMetadata(name string) error {
	if err := deleteKey(name); err != nil {
		return err
	}
	return save()
}

func removeLocksUnsafe(locks []string) bool {
	if len(locks) == 0 {
		return false
	}

	found := false
	for f, m := range metadb {
		del := 0
		for i := 0; i < len(m.Locks)-del; i++ {
			for l := range locks {
				if m.Locks[i] == locks[l] {
					del += 1
					m.Locks[i], m.Locks[len(m.Locks)-del] = m.Locks[len(m.Locks)-del], m.Locks[i]
					continue
				}
			}
		}

		if del > 0 {
			log.Info("Removing ", del, " locks from ", f, ": ", m.Locks[len(m.Locks)-del:])
			m.Locks = m.Locks[:len(m.Locks)-del]
			metadb[f] = m
			found = true
		}
	}

	return found
}

func SetMetadata(filename string, m Metadata) error {
	metalock.Lock()
	someRemoved := removeLocksUnsafe(m.Locks)
	metadb[filename] = m
	metalock.Unlock()

	if someRemoved || m.Expires != 0 {
		resetTimer()
	}

	return save()
}

func GetMetadata(filename string) (Metadata, bool) {
	metalock.RLock()
	defer metalock.RUnlock()
	m, ok := metadb[filename]
	return m, ok
}

func save() error {
	f, err := os.Create(filepath.Join(*configDir, "metadata"))
	if err != nil {
		return err
	}
	defer f.Close()

	var b []byte
	{
		log.Info("metadata: about to save")
		metalock.RLock()
		defer metalock.RUnlock()

		b, err = yaml.Marshal(&metadb)
		if err != nil {
			log.Fatal("failed to convert metadata yaml")
			return err
		}
	}

	_, err = f.Write(b)
	if err != nil {
		return err
	}

	log.Info("metadata saved")
	return nil
}

func LoadMetadata() error {
	log.Info("reading metadata")
	b, err := os.ReadFile(filepath.Join(*configDir, "metadata"))
	if err != nil {
		return err
	}

	metalock.Lock()
	defer metalock.Unlock()
	err = yaml.Unmarshal(b, &metadb)
	if err != nil {
		log.Fatal("failed to load metadata yaml")
		return err
	}

	log.Info("read metadata")
	return nil
}
