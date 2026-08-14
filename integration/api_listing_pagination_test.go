package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetMeta used to read an entire directory into memory and issue one
// GetFileMeta query per entry. Listings are now paged, and the page's metadata
// is resolved in a single batched query.
func TestDirectoryListingPagination(t *testing.T) {
	session := PrepareAuth(t, db, "pagination_user", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)

	dir := "/paged"
	const total = 25

	// os.ReadDir sorts by name, so zero-padding makes the expected order explicit.
	for i := range total {
		p := fmt.Sprintf("%s/file-%02d.txt", dir, i)
		w := Perform(t, router, "PUT", p, WithSession(session), WithBody([]byte("x")))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}

	// list returns the page and the X-Total-Count header value.
	list := func(t *testing.T, query string) ([]api.ResourceResponse, string) {
		t.Helper()
		w := Perform(t, router, "GET", "/_/api/v1/fs"+dir+query, WithSession(session))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		var resp []api.ResourceResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return resp, w.Header().Get("X-Total-Count")
	}

	t.Run("Unpaged request returns everything", func(t *testing.T) {
		resp, hdr := list(t, "")
		assert.Len(t, resp, total)
		assert.Equal(t, strconv.Itoa(total), hdr,
			"X-Total-Count reports the full directory size")
	})

	t.Run("Limit returns the first page", func(t *testing.T) {
		resp, hdr := list(t, "?limit=10")
		require.Len(t, resp, 10)
		assert.Equal(t, "file-00.txt", resp[0].Name)
		assert.Equal(t, "file-09.txt", resp[9].Name)
		assert.Equal(t, strconv.Itoa(total), hdr,
			"X-Total-Count is the directory size, not the page size")
	})

	t.Run("Offset walks the directory", func(t *testing.T) {
		resp, _ := list(t, "?limit=10&offset=10")
		require.Len(t, resp, 10)
		assert.Equal(t, "file-10.txt", resp[0].Name)
		assert.Equal(t, "file-19.txt", resp[9].Name)
	})

	t.Run("Final page is short, not padded", func(t *testing.T) {
		resp, _ := list(t, "?limit=10&offset=20")
		require.Len(t, resp, 5)
		assert.Equal(t, "file-20.txt", resp[0].Name)
		assert.Equal(t, "file-24.txt", resp[4].Name)
	})

	t.Run("Offset past the end returns an empty page", func(t *testing.T) {
		resp, hdr := list(t, "?offset=1000")
		assert.Empty(t, resp)
		assert.Equal(t, strconv.Itoa(total), hdr)
	})

	t.Run("Paging preserves metadata", func(t *testing.T) {
		// The batched lookup must return the same resource fields the
		// per-entry query did.
		resp, _ := list(t, "?limit=1&offset=3")
		require.Len(t, resp, 1)
		assert.Equal(t, "file-03.txt", resp[0].Name)
		assert.Equal(t, dir+"/file-03.txt", resp[0].Path)
		assert.Equal(t, api.ResourceTypeFile, resp[0].Type)
		assert.Equal(t, int64(1), resp[0].Size)
		assert.NotEmpty(t, resp[0].Checksums.SHA256,
			"checksums come from the DB, so a batched lookup must still populate them")
	})

	t.Run("Malformed pagination is rejected", func(t *testing.T) {
		for _, q := range []string{"?limit=abc", "?offset=abc", "?limit=-1", "?offset=-5"} {
			w := Perform(t, router, "GET", "/_/api/v1/fs"+dir+q, WithSession(session))
			assert.Equal(t, http.StatusBadRequest, w.Code, "query %q should be rejected", q)
		}
	})

	// The UI's listFiles walks pages until a short page or X-Total-Count says
	// it is done, then sorts client-side across the whole result. Both signals
	// must therefore be consistent enough to terminate that loop exactly once.
	t.Run("Walking pages reassembles the whole directory", func(t *testing.T) {
		const pageSize = 4

		var seen []string
		offset := 0
		requests := 0

		for {
			resp, hdr := list(t, fmt.Sprintf("?limit=%d&offset=%d", pageSize, offset))
			requests++
			require.Less(t, requests, 50, "page walk must terminate")

			for _, r := range resp {
				seen = append(seen, r.Name)
			}
			offset += len(resp)

			reported, err := strconv.Atoi(hdr)
			require.NoError(t, err, "X-Total-Count must always be a number")
			assert.Equal(t, total, reported, "X-Total-Count must not drift between pages")

			if len(resp) == 0 || len(resp) < pageSize || offset >= reported {
				break
			}
		}

		require.Len(t, seen, total, "the walk must recover every entry exactly once")
		assert.True(t, sort.StringsAreSorted(seen), "entries must not repeat or reorder across pages")
		assert.Equal(t, total, len(uniqueStrings(seen)), "pages must not overlap")
		assert.Equal(t, (total+pageSize-1)/pageSize, requests, "walk should take exactly ceil(total/pageSize) requests")
	})
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
