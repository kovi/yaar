package main

import (
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func ServeRoot(urlPrefix, root string) gin.HandlerFunc {
	return Serve(urlPrefix, root)
}

func Serve(urlPrefix string, root string) gin.HandlerFunc {
	fs := http.Dir(root)

	return func(c *gin.Context) {
		upath := c.Request.URL.Path
		if !strings.HasPrefix(upath, "/") {
			upath = "/" + upath
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
			dirList(c.Writer, c.Request, d)
			return
		}

		http.ServeFile(c.Writer, c.Request, path.Join(root, c.Request.URL.Path[1:]))
	}
}

func dirList(w http.ResponseWriter, r *http.Request, f http.File) {
	// Prefer to use ReadDir instead of Readdir,
	// because the former doesn't require calling
	// Stat on every entry of a directory on Unix.
	var dirs []fs.DirEntry
	var err error
	if d, ok := f.(fs.ReadDirFile); ok {
		var list []fs.DirEntry
		list, err = d.ReadDir(-1)
		dirs = list
		// } else {
		// 	var list fileInfoDirs
		// 	list, err = f.Readdir(-1)
		// 	dirs = list
	}

	if err != nil {
		log.Infof("http: error reading directory: %v", err)
		http.Error(w, "Error reading directory", http.StatusInternalServerError)
		return
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })

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
