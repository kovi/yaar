package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
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

	clearDataDir(t)

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

func clearDataDir(t *testing.T) {
	assert.NoError(t, os.RemoveAll(*dataDir))
	assert.NoError(t, os.MkdirAll(*dataDir, 0766))
}

func toEntries(body *bytes.Buffer) []DirEntry {
	var entries []DirEntry
	if err := json.Unmarshal(body.Bytes(), &entries); err != nil {
		return nil
	}
	return entries
}

func fileNames(body *bytes.Buffer) []string {
	entries := toEntries(body)
	if entries == nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names
}

func TestListingEntries(t *testing.T) {

	clearDataDir(t)

	qTriggers := StartTriggers()
	defer func() {
		close(qTriggers)
	}()

	router := router()
	// prepare special data
	req, _ := http.NewRequest("POST", "/api/v1/repo/file1", strings.NewReader("file1"))
	router.ServeHTTP(httptest.NewRecorder(), req)
	req, _ = http.NewRequest("POST", "/api/v1/repo/fileExpireLockTag", strings.NewReader("file2"))
	req.Header.Set("x-expire", "1h")
	req.Header.Set("x-lock", "lock1")
	req.Header.Set("x-tag", "tag1")
	router.ServeHTTP(httptest.NewRecorder(), req)
	req, _ = http.NewRequest("POST", "/api/v1/repo/subdir/fileInSubdir", strings.NewReader("file3"))
	router.ServeHTTP(httptest.NewRecorder(), req)

	// test listing

	req, _ = http.NewRequest("GET", "/api/v1/dirtree?c=m&o=a", nil) // modtime asc
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	entries := toEntries(w.Body)

	assert.Equal(t, 3, len(entries))

	file1 := entries[0]
	expected1 := DirEntry{
		Name:       "file1",
		Size:       5,
		ModTime:    file1.ModTime, // testing time is hard..
		ExpiryTime: 0,
		IsDir:      false,
		FullPath:   "/file1",
		Url:        "/api/v1/repo/file1",
		Locks:      []string{},
		Tags:       []string{},
	}
	assert.Equal(t, expected1, file1)

	file2 := entries[1]
	expected2 := DirEntry{
		Name:       "fileExpireLockTag",
		Size:       5,
		ModTime:    file2.ModTime,
		ExpiryTime: uint64(time.Unix(int64(file2.ModTime), 0).Add(time.Hour).Unix()),
		IsDir:      false,
		FullPath:   "/fileExpireLockTag",
		Url:        "/api/v1/repo/fileExpireLockTag",
		Locks:      []string{"lock1"},
		Tags:       []string{"tag1"},
	}
	assert.Equal(t, expected2, file2)

	subdir := entries[2]
	expected3 := DirEntry{
		Name:       "subdir",
		Size:       subdir.Size,
		ModTime:    subdir.ModTime,
		ExpiryTime: 0,
		IsDir:      true,
		FullPath:   "/subdir",
		Url:        "/api/v1/dirtree/subdir",
		Locks:      []string{},
		Tags:       []string{},
	}
	assert.Equal(t, expected3, subdir)

	// list subdir
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/subdir", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	entries = toEntries(w.Body)
	assert.Equal(t, 1, len(entries))
	fileInSubdir := entries[0]
	expected4 := DirEntry{
		Name:       "fileInSubdir",
		Size:       5,
		ModTime:    fileInSubdir.ModTime,
		ExpiryTime: 0,
		IsDir:      false,
		FullPath:   "/subdir/fileInSubdir",
		Url:        "/api/v1/repo/subdir/fileInSubdir",
		Locks:      []string{},
		Tags:       []string{},
	}
	assert.Equal(t, expected4, fileInSubdir)
}

func TestUrlIsCorrect(t *testing.T) {
	// test that its possible to download the file from the URL
	err := prepareDataDir(t, "urltest")
	assert.NoError(t, err)

	router := router()

	req, _ := http.NewRequest("GET", "/api/v1/dirtree/urltest", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	entries := toEntries(w.Body)

	entry := entries[0] // c2.bin
	req, _ = http.NewRequest("GET", entry.Url, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "c2", w.Body.String())
}

func TestListingOrdering(t *testing.T) {
	err := prepareDataDir(t, "listing")
	assert.NoError(t, err)

	router := router()

	// default - modtime desc
	req, _ := http.NewRequest("GET", "/api/v1/dirtree/listing", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"c2.txt", "c.txt", "a.bin", "b.txt"}, fileNames(w.Body))

	// test ordering - name desc
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listing?c=n&o=d", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"c2.txt", "c.txt", "b.txt", "a.bin"}, fileNames(w.Body))

	// // test ordering - name asc
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listing?c=n&o=a", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"a.bin", "b.txt", "c.txt", "c2.txt"}, fileNames(w.Body))

	// test ordering - modification desc
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listing?c=m&o=d", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"c2.txt", "c.txt", "a.bin", "b.txt"}, fileNames(w.Body))

	// test ordering - modification time asc
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listing?c=m&o=a", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"b.txt", "a.bin", "c.txt", "c2.txt"}, fileNames(w.Body))
}

func TestListingFilter(t *testing.T) {
	err := prepareDataDir(t, "listingfilter")
	assert.NoError(t, err)

	router := router()

	// nothing matches
	req, _ := http.NewRequest("GET", "/api/v1/dirtree/listingfilter/?qt=nothing", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{}, fileNames(w.Body))

	// tag with no value checks existence of tags
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listingfilter?qt=branch&c=n", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"c2.txt", "c.txt", "a.bin"}, fileNames(w.Body))

	// tag with value needs matching value
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listingfilter?qt=branch=dev&C=n", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"c.txt"}, fileNames(w.Body))

	// lock filter
	// filter for lock string whihc does not exists
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listingfilter?ql=devel-notexist", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{}, fileNames(w.Body))

	// lock with matching
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listingfilter?ql=devel", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"c.txt"}, fileNames(w.Body))

	// name prefix filter
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listingfilter?qn=notexist", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{}, fileNames(w.Body))

	// matching
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listingfilter?qn=c", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"c2.txt", "c.txt"}, fileNames(w.Body))

	// matching
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listingfilter?qn=b.", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"b.txt"}, fileNames(w.Body))

	// combination - one not matching
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listingfilter?qt=devel&qn=c", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{}, fileNames(w.Body))

	// combination - one matching
	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listingfilter?qt=branch=dev&qn=c", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"c.txt"}, fileNames(w.Body))
}

func TestRepoGet(t *testing.T) {
	// Test that can GET a file
	// Test that getting a non-existing file returns NotFound
	// Test that getting a directory returns BadRequest
	err := prepareDataDir(t, "getrequest")
	assert.NoError(t, err)
	qTriggers := StartTriggers()
	defer func() {
		close(qTriggers)
	}()
	router := router()

	req, _ := http.NewRequest("GET", "/api/v1/repo/getrequest/a.bin", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "a", w.Body.String(), "Expected file does not match")

	req, _ = http.NewRequest("GET", "/api/v1/repo/getrequest/nonexisting.bin", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code, "Getting non-existing file did not return NotFound")

	req, _ = http.NewRequest("GET", "/api/v1/repo/getrequest", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code, "Getting a directory did not return BadRequest")
	// with trailing slash
	req, _ = http.NewRequest("GET", "/api/v1/repo/getrequest/", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code, "Getting a directory did not return BadRequest")
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

	req, _ := http.NewRequest("POST", "/api/v1/repo/listing/create/me/hello.txt", strings.NewReader("hello"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	req, _ = http.NewRequest("POST", "/api/v1/repo/listing/create/me/world.txt", strings.NewReader("world"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	req, _ = http.NewRequest("GET", "/api/v1/dirtree/listing/create/me", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, []string{"world.txt", "hello.txt"}, fileNames(w.Body))

	req, _ = http.NewRequest("GET", "/api/v1/repo/listing/create/me/hello.txt", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "hello", w.Body.String())

	req, _ = http.NewRequest("GET", "/api/v1/repo/listing/create/me/world.txt", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "world", w.Body.String())
}

func TestPostExisting(t *testing.T) {
	// testing that POSTing an already existing file does not overwrite it
	err := prepareDataDir(t, "postrequest")
	assert.NoError(t, err)
	qTriggers := StartTriggers()
	defer func() {
		close(qTriggers)
	}()
	router := router()

	req, _ := http.NewRequest("POST", "/api/v1/repo/postrequest/a.bin", strings.NewReader("not-a"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code, "Incorrect request when POSTing existing file")

	req, _ = http.NewRequest("GET", "/api/v1/repo/postrequest/a.bin", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "a", w.Body.String(), "POST to existing file changed its content")
}

func TestPut(t *testing.T) {
	// Test that PUT requests create or overwrite existing resource
	err := prepareDataDir(t, "putrequest")
	assert.NoError(t, err)
	qTriggers := StartTriggers()
	defer func() {
		close(qTriggers)
	}()
	router := router()

	// verify that file exists
	req, _ := http.NewRequest("GET", "/api/v1/repo/putrequest/a.bin", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "a", w.Body.String(), "Expected file does not match")

	req, _ = http.NewRequest("PUT", "/api/v1/repo/putrequest/a.bin", strings.NewReader("not-a"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "PUT rejected on existing resource")

	req, _ = http.NewRequest("GET", "/api/v1/repo/putrequest/a.bin", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "not-a", w.Body.String(), "File was not overwritten")

	// PUT a new file

	req, _ = http.NewRequest("PUT", "/api/v1/repo/putrequest/newfile", strings.NewReader("newfile"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "PUT rejected on new resource")

	req, _ = http.NewRequest("GET", "/api/v1/repo/putrequest/newfile", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "newfile", w.Body.String(), "File was not created")
}
