package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func init() {
	log.SetLevel(log.DebugLevel)
}

func prepareDataDir(t *testing.T, baseDir string) error {
	baseDir = "/" + baseDir // metadata names are saved with / prefixed

	assert.NoError(t, os.RemoveAll(*dataDir))
	d := path.Join(*dataDir, baseDir)
	assert.NoError(t, os.MkdirAll(d, 0766))

	currentTime := time.Now().Local()
	// order is b-a-c so name and modification time is not in any order
	name := path.Join(baseDir, "b.txt")
	file := path.Join(*dataDir, name)
	assert.NoError(t, os.WriteFile(file, []byte("b"), 0666))
	assert.NoError(t, os.Chtimes(file, currentTime.Add(-5), currentTime.Add(-5)))

	name = path.Join(baseDir, "a.bin")
	file = path.Join(*dataDir, name)
	assert.NoError(t, os.WriteFile(file, []byte("a"), 0666))
	assert.NoError(t, os.Chtimes(file, currentTime, currentTime))
	assert.NoError(t, SetMetadata(name, Metadata{Tags: []string{"abc", "branch"}}))

	name = path.Join(baseDir, "c.txt")
	file = path.Join(*dataDir, name)
	assert.NoError(t, os.WriteFile(file, []byte("c"), 0666))
	assert.NoError(t, os.Chtimes(file, currentTime.Add(1), currentTime.Add(1)))
	assert.NoError(t, SetMetadata(name, Metadata{
		Tags:  []string{"branch=dev", "def", "release=no"},
		Locks: []string{"devel"}}))

	name = path.Join(baseDir, "c2.txt")
	file = path.Join(*dataDir, name)
	assert.NoError(t, os.WriteFile(file, []byte("c2"), 0666))
	assert.NoError(t, os.Chtimes(file, currentTime.Add(2), currentTime.Add(2)))
	assert.NoError(t, SetMetadata(name, Metadata{
		Tags:  []string{"branch=dev2"},
		Locks: []string{"develx"}}))
	return nil
}

func filesOf(body string) []string {
	fm := regexp.MustCompile(`href="([^"]+)"`)
	files := fm.FindAllStringSubmatch(body, -1)
	r := make([]string, 0)
	for i := range files {
		r = append(r, files[i][1])
	}
	return r
}
func TestListing(t *testing.T) {
	err := prepareDataDir(t, "listing")
	assert.NoError(t, err)

	router := router()

	// default - no ordering
	req, _ := http.NewRequest("GET", "/listing", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.ElementsMatch(t, []string{"../", "b.txt", "a.bin", "c2.txt", "c.txt"}, filesOf(w.Body.String()))

	// test ordering - name desc
	req, _ = http.NewRequest("GET", "/listing?c=n&o=d", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../", "c2.txt", "c.txt", "b.txt", "a.bin"}, filesOf(w.Body.String()))

	// // test ordering - name asc
	req, _ = http.NewRequest("GET", "/listing?c=n&o=a", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../", "a.bin", "b.txt", "c.txt", "c2.txt"}, filesOf(w.Body.String()))

	// test ordering - modification desc
	req, _ = http.NewRequest("GET", "/listing?c=m&o=d", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../", "c2.txt", "c.txt", "a.bin", "b.txt"}, filesOf(w.Body.String()))

	// test ordering - modification time asc
	req, _ = http.NewRequest("GET", "/listing?c=m&o=a", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../", "b.txt", "a.bin", "c.txt", "c2.txt"}, filesOf(w.Body.String()))
}

func TestListingFilter(t *testing.T) {
	err := prepareDataDir(t, "listingfilter")
	assert.NoError(t, err)

	router := router()

	// nothing matches
	req, _ := http.NewRequest("GET", "listingfilter/?qt=nothing", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../"}, filesOf(w.Body.String()))

	// tag with no value checks existence of tags
	req, _ = http.NewRequest("GET", "/listingfilter?qt=branch&c=n", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../", "a.bin", "c.txt", "c2.txt"}, filesOf(w.Body.String()))

	// tag with value needs matching value
	req, _ = http.NewRequest("GET", "/listingfilter?qt=branch=dev&C=n", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../", "c.txt"}, filesOf(w.Body.String()))

	// lock filter
	// filter for lock string whihc does not exists
	req, _ = http.NewRequest("GET", "/listingfilter?ql=devel-notexist", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../"}, filesOf(w.Body.String()))

	// lock with matching
	req, _ = http.NewRequest("GET", "/listingfilter?ql=devel", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../", "c.txt"}, filesOf(w.Body.String()))

	// name prefix filter
	req, _ = http.NewRequest("GET", "/listingfilter?qn=notexist", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../"}, filesOf(w.Body.String()))

	// matching
	req, _ = http.NewRequest("GET", "/listingfilter?qn=c", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.ElementsMatch(t, []string{"../", "c.txt", "c2.txt"}, filesOf(w.Body.String()))

	// matching
	req, _ = http.NewRequest("GET", "/listingfilter?qn=b.", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../", "b.txt"}, filesOf(w.Body.String()))

	// combination - one not matching
	req, _ = http.NewRequest("GET", "/listingfilter?qt=devel&qn=c", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../"}, filesOf(w.Body.String()))

	// combination - one matching
	req, _ = http.NewRequest("GET", "/listingfilter?qt=branch=dev&qn=c", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"../", "c.txt"}, filesOf(w.Body.String()))

}

func TestPostWithMkdir(t *testing.T) {
	// Test that POST-ing a file to a directory that does not exist creates the directory
	// Test that POST-ing a file to a directory that does exist adds the file to the directory
	err := prepareDataDir(t, "listing")
	assert.NoError(t, err)

	qTriggers := StartTriggers()

	defer func() {
		close(qTriggers)
	}()

	router := router()

	req, _ := http.NewRequest("POST", "/listing/create/me/hello.txt", strings.NewReader("hello"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	req, _ = http.NewRequest("POST", "/listing/create/me/world.txt", strings.NewReader("world"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	req, _ = http.NewRequest("GET", "/listing/create/me", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.ElementsMatch(t, []string{"../", "hello.txt", "world.txt"}, filesOf(w.Body.String()))

	req, _ = http.NewRequest("GET", "/listing/create/me/hello.txt", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "hello", w.Body.String())

	req, _ = http.NewRequest("GET", "/listing/create/me/world.txt", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "world", w.Body.String())
}
