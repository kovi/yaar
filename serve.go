package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func handleUpload(c *gin.Context, allowOverwrite bool) {

	name := c.Param("name")
	log.Info("upload of ", name, " with overwrite ", allowOverwrite)

	b := c.Request.Body
	if b == nil {
		log.Info("No body")
		c.Status(http.StatusBadRequest)
		return
	}

	dataFileName := filepath.Join(*dataDir, name)
	_, err := os.Stat(dataFileName)
	exists := !os.IsNotExist(err)
	if exists && !allowOverwrite {
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

	err = os.MkdirAll(filepath.Dir(dataFileName), 0755)
	if err != nil {
		log.Info("put error in mkdir: ", err)
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
}

func handleDirtreeGet(c *gin.Context) {
	name := c.Param("name")

	fs := http.Dir(*dataDir)
	file, err := fs.Open(name)
	if os.IsNotExist(err) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		c.Status(http.StatusOK)
		jsonDirList(c.Writer, c.Request, file, name)
		return
	}
}

func router() *gin.Engine {
	router := gin.Default()

	// browser
	router.GET("/browser.html", func(c *gin.Context) {
		http.ServeFile(c.Writer, c.Request, "browser.html")
	})
	router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/browser.html")
	})

	// /api
	api := router.Group("/api/v1")

	api.DELETE("/meta/*name", func(c *gin.Context) {
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

	api.GET("/dirtree", func(c *gin.Context) {
		c.Params = append(c.Params, gin.Param{Key: "name", Value: "/"})
		handleDirtreeGet(c)
	})
	api.GET("/dirtree/*name", handleDirtreeGet)

	// /api/repo - download/upload
	repo := api.Group("/repo")

	repo.GET("/*name", func(c *gin.Context) {
		name := c.Param("name")
		path := filepath.Join(*dataDir, name)
		stat, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		if stat.IsDir() {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		http.ServeFile(c.Writer, c.Request, path)
	})

	repo.PUT("/*name", func(c *gin.Context) {
		handleUpload(c, true)
	})

	repo.POST("/*name", func(c *gin.Context) {
		handleUpload(c, false)
	})

	return router
}

type Filter interface {
	matches(fs.DirEntry, Metadata) bool
}

type TagFilter struct {
	tag   string
	value string
}

func NewTagFilter(q string) (TagFilter, error) {
	if q == "" {
		return TagFilter{}, fmt.Errorf("invalid tag filter")
	}
	v := strings.SplitN(q, "=", 2)
	t := TagFilter{tag: v[0]}
	if len(v) > 1 {
		t.value = v[1]
	}
	return t, nil
}

// matches return true if metadata contains a tag with given value
func (t TagFilter) matches(_ fs.DirEntry, m Metadata) bool {
	for i := range m.Tags {
		tag := strings.SplitN(m.Tags[i], "=", 2)

		// skip this if tag name does not match
		if t.tag != tag[0] {
			continue
		}

		// when no need to match value - return ok
		if t.value == "" {
			return true
		}

		// otherwise expect values to match too
		if len(tag) == 2 && t.value == tag[1] {
			return true
		}
	}

	return false
}

type LockFilter struct {
	lock string
}

func NewLockFilter(q string) (LockFilter, error) {
	return LockFilter{lock: q}, nil
}

func (l LockFilter) matches(_ fs.DirEntry, m Metadata) bool {
	for i := range m.Locks {
		if m.Locks[i] == l.lock {
			return true
		}
	}
	return false
}

// NameFilter is a matcher for name prefix's
type NameFilter struct {
	name string
}

func NewNameFilter(q string) (NameFilter, error) {
	return NameFilter{name: q}, nil
}

func (l NameFilter) matches(e fs.DirEntry, _ Metadata) bool {
	return strings.HasPrefix(e.Name(), l.name)
}

type HttpError struct {
	message string
	code    int
}

func dirEntries(r *http.Request, f http.File, urlpath string) ([]fs.DirEntry, *HttpError) {
	// Prefer to use ReadDir instead of Readdir,
	// because the former doesn't require calling
	// Stat on every entry of a directory on Unix.
	var dirs []fs.DirEntry
	var err error
	if d, ok := f.(fs.ReadDirFile); ok {
		var list []fs.DirEntry
		list, err = d.ReadDir(-1)
		dirs = list
	}

	if err != nil {
		log.Infof("http: error reading directory: %v", err)
		// http.Error(w, "Error reading directory", http.StatusInternalServerError)
		return dirs, &HttpError{"Error reading directory", http.StatusInternalServerError}
	}

	// tag filtering
	var filters []Filter
	if r.URL.Query().Has("qt") || r.URL.Query().Has("qn") || r.URL.Query().Has("ql") {

		for key, values := range r.URL.Query() {
			if key == "qt" {
				for i := range values {
					f, err := NewTagFilter(values[i])
					if err != nil {
						log.Infof("http: %v", err)
						return dirs, &HttpError{"invalid filter", http.StatusBadRequest}
					}
					filters = append(filters, f)
				}
			}
			if key == "ql" {
				for i := range values {
					f, err := NewLockFilter(values[i])
					if err != nil {
						log.Infof("http: %v", err)
						return dirs, &HttpError{"invalid filter", http.StatusBadRequest}
					}
					filters = append(filters, f)
				}
			}
			if key == "qn" {
				for i := range values {
					f, err := NewNameFilter(values[i])
					if err != nil {
						log.Infof("http: %v", err)
						return dirs, &HttpError{"invalid filter", http.StatusBadRequest}
					}
					filters = append(filters, f)
				}
			}
		}
	}

	if filters != nil {
		// need to load meta for each entries
		tbd := 0
		for i := 0; i < len(dirs)-tbd; i++ {
			e := dirs[i]
			filename := path.Join(urlpath, e.Name())
			m, ok := GetMetadata(filename)
			// metadata load fail can be ok when there is no metadata
			log.Debugf("get metadata for %v - ok: %v", filename, ok)

			matches := true
			// all filter must match
			for i := range filters {
				matches = filters[i].matches(e, m)
				if !matches {
					break
				}
			}
			log.Debugf("checking %v %v", e, matches)

			if !matches {
				tbd += 1
				j := len(dirs) - tbd
				dirs[i], dirs[j] = dirs[j], dirs[i]
				// step cycle back to redo current item after swap
				i -= 1
			}
		}

		log.Debugf("Filter did not match %v entry", tbd)
		dirs = dirs[:len(dirs)-tbd]
	}

	// get ordering, by default newest to oldest
	ordering := "m"
	asc := false
	c := strings.ToLower(r.URL.Query().Get("c"))
	if c != "" {
		ordering = c
		o := strings.ToLower(r.URL.Query().Get("o"))
		if o != "" {
			asc = o == "a"
		}
		log.Debugf("Setting ordering as '%v' %v", o, asc)
	}

	if ordering != "" {
		log.Debugf("ordering %v asc: %v", ordering, asc)
		sort.Slice(dirs, func(i, j int) bool {
			r := false // is i is after j
			if ordering == "m" {
				// last modification
				fi, err := dirs[i].Info()
				if err != nil {
					return false
				}
				fj, err := dirs[j].Info()
				if err != nil {
					return false
				}
				r = fi.ModTime().Before(fj.ModTime())
			} else {
				r = dirs[i].Name() < dirs[j].Name()
			}
			if !asc {
				// ordering is not descending, reverse return
				r = !r
			}
			return r
		})
	}
	return dirs, nil
}

type DirEntry struct {
	Name       string
	Size       int64
	ModTime    uint64 // seconds since epoch
	ExpiryTime uint64
	IsDir      bool
	FullPath   string
	Url        string
	Locks      []string
	Tags       []string
}

func jsonDirList(w gin.ResponseWriter, r *http.Request, f http.File, urlpath string) {
	log.Info("jsonDirList()")
	dirs, httpErr := dirEntries(r, f, urlpath)
	if httpErr != nil {
		http.Error(w, httpErr.message, httpErr.code)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	entries := make([]DirEntry, 0, len(dirs))
	for _, e := range dirs {
		info, err := e.Info()
		if err == nil {
			fullpath := filepath.Join(urlpath, e.Name())
			var url string
			if e.IsDir() {
				url = "/api/v1/dirtree" + fullpath
			} else {
				url = "/api/v1/repo" + fullpath
			}

			metadata, ok := GetMetadata(fullpath)
			expiryTime := uint64(0)
			lockName := []string{}
			tags := []string{}
			if ok {
				if metadata.Expires == 0 {
					expiryTime = 0
				} else {
					expiryTime = uint64(metadata.Added.Add(metadata.Expires).Unix())
				}
				lockName = metadata.Locks
				tags = metadata.Tags
			}

			entry := DirEntry{
				Name:       e.Name(),
				Size:       info.Size(),
				ModTime:    uint64(info.ModTime().Unix()),
				ExpiryTime: expiryTime,
				IsDir:      e.IsDir(),
				FullPath:   fullpath,
				Url:        url,
				Locks:      lockName,
				Tags:       tags,
			}
			// normalize so empty slices are [] not null in json
			if entry.Locks == nil {
				entry.Locks = []string{}
			}
			if entry.Tags == nil {
				entry.Tags = []string{}
			}
			entries = append(entries, entry)
		}
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err == nil {
		w.Write(b)
	} else {
		http.Error(w, "dirs marshal error: "+err.Error(), http.StatusInternalServerError)
	}
}
