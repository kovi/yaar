package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

var dirTreePrefix = "/api/dirtree"
var repoPrefix = "/repo"

func router() *gin.Engine {
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

		allowOverwrite := c.DefaultQuery("overwrite", "false") == "true"

		dataFileName := filepath.Join(*dataDir, name)
		_, err := os.Stat(dataFileName)
		exists := !os.IsNotExist(err)
		if exists && !allowOverwrite {
			log.Info("File ", dataFileName, " already exists")
			c.String(http.StatusBadRequest, "already exists")
			return
		} else if exists {
			log.Info("File ", dataFileName, " already exists and will be overwritten")
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
	})
	router.Use(Serve("/", *dataDir))

	return router
}

// GET method: serve files as is and directories as html pages.
//
// entry points:
// / -- redirect to browser.html
// /browser.html -- AJAX based HTML client for directory browsing
// /repo -- download files, TODO directories
// /api -- REST API
// /api/dirtree/ -- directory listing
func Serve(urlPrefix string, root string) gin.HandlerFunc {
	fs := http.Dir(root)

	return func(c *gin.Context) {
		upath := c.Request.URL.Path
		if !strings.HasPrefix(upath, "/") {
			upath = "/" + upath
		}

		if upath == "/" {
			c.Redirect(http.StatusMovedPermanently, "/browser.html")
		}

		if upath == "/browser.html" {
			http.ServeFile(c.Writer, c.Request, "browser.html")
			return
		}

		serveAsJson := false

		if strings.HasPrefix(upath, dirTreePrefix+"/") {
			upath = strings.TrimPrefix(upath, dirTreePrefix)
			serveAsJson = true
		} else if strings.HasPrefix(upath, repoPrefix+"/") {
			upath = strings.TrimPrefix(upath, repoPrefix)
		}

		d, err := fs.Open(upath)
		if os.IsNotExist(err) {
			c.AbortWithError(http.StatusNotFound, err)
			return
		}
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		s, err := d.Stat()
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		if s.IsDir() {
			c.Status(http.StatusOK)
			if serveAsJson {
				jsonDirList(c.Writer, c.Request, d, upath)
			} else {
				dirList(c.Writer, c.Request, d, upath)
			}
			return
		}
		if serveAsJson {
			c.AbortWithError(http.StatusNotFound, fmt.Errorf("path is not a directory"))
			return
		}
		http.ServeFile(c.Writer, c.Request, path.Join(root, upath[1:]))
	}
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

	// get ordering
	ordering := ""
	asc := true
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

func dirList(w http.ResponseWriter, r *http.Request, f http.File, urlpath string) {
	dirs, err := dirEntries(r, f, urlpath)
	if err != nil {
		http.Error(w, err.message, err.code)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	current := r.URL.Path
	fmt.Fprintf(w, "<html><head><title>Index of %s</title></head><body><h1>%s</h1><hr><pre>\n", current, current)
	if current != "/" {
		fmt.Fprintf(w, "<a href=\"../\">../</a>\n")
	}
	for i, n := 0, len(dirs); i < n; i++ {
		name := dirs[i].Name()
		if dirs[i].IsDir() {
			name += "/"
		}
		// name may contain '?' or '#', which must be escaped to remain
		// part of the URL path, and not indicate the start of a query
		// string or fragment.
		url := url.URL{Path: name}
		info, err := dirs[i].Info()
		if err != nil {
			log.Info("error reading entry ", i, ": ", err)
		}
		fmt.Fprintf(w, "<a href=\"%s\">%s</a>%-*s %20s  %20d\n", url.String(), html.EscapeString(name), 50-len(name), "", info.ModTime().Format(time.RFC3339), info.Size())
	}
	fmt.Fprintf(w, "<hr>%s</pre></body></html>\n", time.Now().Format(time.RFC3339))
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
				url = dirTreePrefix + fullpath
			} else {
				url = repoPrefix + fullpath
			}

			metadata, ok := GetMetadata(fullpath)
			expiryTime := uint64(0)
			lockName := make([]string, 0)
			tags := make([]string, 0)
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
